package platform

import (
	"context"
	"strings"
	"testing"
)

// TestCreateDatabaseInjectsCredsAndStorage asserts CreateDatabase generates
// engine creds, injects the engine-appropriate ENV into the Workload, sets a
// persistent-volume size, and persists the creds on the record.
func TestCreateDatabaseInjectsCredsAndStorage(t *testing.T) {
	svc, fb := newSvcWithFake()
	ctx := context.Background()

	db, err := svc.CreateDatabase(ctx, "org-1", CreateDatabaseInput{Name: "Main DB", Engine: "postgresql"})
	if err != nil {
		t.Fatalf("create db: %v", err)
	}

	// Credentials persisted on the record.
	if db.Username == "" || db.Password == "" || db.DatabaseName == "" {
		t.Fatalf("credentials not persisted: %+v", db)
	}
	if db.StorageGB != defaultDBStorageGB {
		t.Fatalf("storage default not applied: got %d want %d", db.StorageGB, defaultDBStorageGB)
	}
	// Stored password must be strong (random hex of 24 bytes => 48 chars).
	if len(db.Password) != 48 {
		t.Fatalf("unexpected password length %d", len(db.Password))
	}

	w, ok := fb.Applied[db.Namespace+"/"+db.Release]
	if !ok {
		t.Fatalf("backend did not record the database apply")
	}
	// Engine env injected into the workload.
	if w.Env["POSTGRES_PASSWORD"] != db.Password ||
		w.Env["POSTGRES_DB"] != db.DatabaseName ||
		w.Env["POSTGRES_USER"] != db.Username {
		t.Fatalf("postgres env not injected: %+v", w.Env)
	}
	// Persistence carried on the workload.
	if w.StorageGB != db.StorageGB {
		t.Fatalf("workload storage not set: got %d want %d", w.StorageGB, db.StorageGB)
	}
}

// TestCreateDatabaseEnginesEnv asserts engine-appropriate env for mysql, mongo,
// and redis.
func TestCreateDatabaseEnginesEnv(t *testing.T) {
	cases := []struct {
		engine string
		want   []string
	}{
		{"mysql", []string{"MYSQL_ROOT_PASSWORD", "MYSQL_DATABASE", "MYSQL_USER", "MYSQL_PASSWORD"}},
		{"mariadb", []string{"MYSQL_ROOT_PASSWORD", "MYSQL_DATABASE"}},
		{"mongodb", []string{"MONGO_INITDB_ROOT_USERNAME", "MONGO_INITDB_ROOT_PASSWORD", "MONGO_INITDB_DATABASE"}},
		{"redis", []string{"REDIS_PASSWORD"}},
	}
	for _, tc := range cases {
		t.Run(tc.engine, func(t *testing.T) {
			svc, fb := newSvcWithFake()
			db, err := svc.CreateDatabase(context.Background(), "org-e", CreateDatabaseInput{Name: "d", Engine: tc.engine})
			if err != nil {
				t.Fatalf("create %s: %v", tc.engine, err)
			}
			w := fb.Applied[db.Namespace+"/"+db.Release]
			for _, k := range tc.want {
				if w.Env[k] == "" {
					t.Fatalf("%s: missing env %q in %+v", tc.engine, k, w.Env)
				}
			}
		})
	}
}

// TestDatabaseConnectionInfo asserts the connection-info detail returns the
// in-cluster service DNS host, engine port, and a ready-to-use connection
// string, and is tenant-scoped (cross-tenant => ErrNotFound).
func TestDatabaseConnectionInfo(t *testing.T) {
	svc, _ := newSvcWithFake()
	ctx := context.Background()

	db, err := svc.CreateDatabase(ctx, "org-1", CreateDatabaseInput{Name: "maindb", Engine: "postgresql"})
	if err != nil {
		t.Fatalf("create db: %v", err)
	}

	detail, err := svc.GetDatabaseDetail(ctx, "org-1", db.ID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	ci := detail.Connection
	wantHost := db.Release + "." + db.Namespace + ".svc.cluster.local"
	if ci.Host != wantHost {
		t.Fatalf("host: got %q want %q", ci.Host, wantHost)
	}
	if ci.Port != 5432 {
		t.Fatalf("port: got %d want 5432", ci.Port)
	}
	if ci.Database != db.DatabaseName || ci.Username != db.Username || ci.Password != db.Password {
		t.Fatalf("conn creds mismatch: %+v", ci)
	}
	wantPrefix := "postgres://" + db.Username + ":" + db.Password + "@" + wantHost + ":5432/" + db.DatabaseName
	if ci.ConnectionString != wantPrefix {
		t.Fatalf("connectionString: got %q want %q", ci.ConnectionString, wantPrefix)
	}

	// Cross-tenant access is hidden as not-found.
	if _, err := svc.GetDatabaseDetail(ctx, "org-2", db.ID); err == nil {
		t.Fatal("cross-tenant detail should fail")
	}
}

// TestDatabaseConnectionStringEngines asserts the URI scheme per engine.
func TestDatabaseConnectionStringEngines(t *testing.T) {
	cases := map[string]string{
		"mysql":   "mysql://",
		"mariadb": "mysql://",
		"mongodb": "mongodb://",
		"redis":   "redis://",
	}
	for engine, scheme := range cases {
		svc, _ := newSvcWithFake()
		db, err := svc.CreateDatabase(context.Background(), "org-c", CreateDatabaseInput{Name: "d", Engine: engine})
		if err != nil {
			t.Fatalf("create %s: %v", engine, err)
		}
		detail, err := svc.GetDatabaseDetail(context.Background(), "org-c", db.ID)
		if err != nil {
			t.Fatalf("detail %s: %v", engine, err)
		}
		if !strings.HasPrefix(detail.Connection.ConnectionString, scheme) {
			t.Fatalf("%s: connectionString %q lacks scheme %q", engine, detail.Connection.ConnectionString, scheme)
		}
		// Mongo's root user lives in the admin db, so the URI must select it via
		// authSource=admin or authentication fails.
		if strings.HasPrefix(engine, "mongo") &&
			!strings.Contains(detail.Connection.ConnectionString, "authSource=admin") {
			t.Fatalf("mongo connectionString %q missing authSource=admin", detail.Connection.ConnectionString)
		}
	}
}

// TestDatabaseLifecycle asserts stop/start/deploy drive the backend and keep the
// release, and that the persistence (StorageGB) survives a stop (the retained
// PVC is rendered on every re-apply).
func TestDatabaseLifecycle(t *testing.T) {
	svc, fb := newSvcWithFake()
	ctx := context.Background()

	db, err := svc.CreateDatabase(ctx, "org-1", CreateDatabaseInput{Name: "db", Engine: "postgresql", StorageGB: 5})
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	rel := db.Release
	k := db.Namespace + "/" + db.Release

	// Stop: backend scales to 0 but keeps the release (retained PVC).
	stopped, err := svc.StopDatabase(ctx, "org-1", db.ID)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if stopped.Status != "stopped" || stopped.Release != rel {
		t.Fatalf("stop changed release/status: %+v", stopped)
	}
	if fb.Replicas[k] != 0 {
		t.Fatalf("stop did not scale to 0: %d", fb.Replicas[k])
	}
	// The applied workload (and thus its persistence) is still recorded after stop.
	if w, ok := fb.Applied[k]; !ok || w.StorageGB != 5 {
		t.Fatalf("stopped db lost its volume config: %+v ok=%v", w, ok)
	}

	// Start: scales back to 1, release unchanged.
	started, err := svc.StartDatabase(ctx, "org-1", db.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if started.Release != rel || fb.Replicas[k] != 1 {
		t.Fatalf("start did not scale up / changed release: %+v reps=%d", started, fb.Replicas[k])
	}

	// Restart: drives the backend, release unchanged.
	if _, err := svc.RestartDatabase(ctx, "org-1", db.ID); err != nil {
		t.Fatalf("restart: %v", err)
	}

	// Deploy: re-applies with the same release + the stored storage size.
	deployed, err := svc.DeployDatabase(ctx, "org-1", db.ID)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if deployed.Release != rel || deployed.Status != "deploying" {
		t.Fatalf("deploy changed release/status: %+v", deployed)
	}
	if w := fb.Applied[k]; w.StorageGB != 5 || w.Env["POSTGRES_PASSWORD"] != db.Password {
		t.Fatalf("redeploy lost storage/creds: %+v", w)
	}

	// Cross-tenant lifecycle is hidden.
	if _, err := svc.StopDatabase(ctx, "org-2", db.ID); err == nil {
		t.Fatal("cross-tenant stop should fail")
	}
}
