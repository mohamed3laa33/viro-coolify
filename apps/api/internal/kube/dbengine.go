package kube

import "strings"

// dbEngine normalizes a database engine / service-template key to one of the
// canonical engines this package knows how to wire (creds env + data dir).
func dbEngine(key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "postgresql", "postgres":
		return "postgresql"
	case "mysql":
		return "mysql"
	case "mariadb":
		return "mariadb"
	case "mongodb", "mongo":
		return "mongodb"
	case "redis":
		return "redis"
	default:
		return ""
	}
}

// dataDir returns the on-disk directory the engine persists to, used as the
// data volume's mountPath. An unknown engine returns "" (no data mount).
func dataDir(key string) string {
	switch dbEngine(key) {
	case "postgresql":
		return "/var/lib/postgresql/data"
	case "mysql", "mariadb":
		return "/var/lib/mysql"
	case "mongodb":
		return "/data/db"
	case "redis":
		return "/data"
	default:
		return ""
	}
}

// DatabaseEnv returns the engine-appropriate initialization environment for a
// managed database, given the engine/template key and the generated
// credentials. The container images read these on first boot to create the
// database, user, and password. Unknown engines get an empty map (the caller
// still deploys, just without injected creds).
//
// redis has no standard *_PASSWORD env honored by the official image; its
// password is enforced via the container command (see redisArgs). We still
// return REDIS_PASSWORD so the value is discoverable, but auth is wired through
// args.
func DatabaseEnv(key, dbName, user, password string) map[string]string {
	switch dbEngine(key) {
	case "postgresql":
		return map[string]string{
			"POSTGRES_DB":       dbName,
			"POSTGRES_USER":     user,
			"POSTGRES_PASSWORD": password,
			// The official postgres image refuses to initialize into a non-empty
			// mounted data dir unless data lives in a subdirectory; PGDATA points at
			// a subpath of the PVC mount so the volume root (lost+found etc.) is fine.
			"PGDATA": "/var/lib/postgresql/data/pgdata",
		}
	case "mysql", "mariadb":
		return map[string]string{
			"MYSQL_ROOT_PASSWORD": password,
			"MYSQL_DATABASE":      dbName,
			"MYSQL_USER":          user,
			"MYSQL_PASSWORD":      password,
		}
	case "mongodb":
		return map[string]string{
			"MONGO_INITDB_ROOT_USERNAME": user,
			"MONGO_INITDB_ROOT_PASSWORD": password,
			"MONGO_INITDB_DATABASE":      dbName,
		}
	case "redis":
		return map[string]string{
			"REDIS_PASSWORD": password,
		}
	default:
		return map[string]string{}
	}
}

// redisArgs returns the container command/args that enforce a password on the
// official redis image (which has no password env). Empty when not redis or no
// password is set.
func redisArgs(key, password string) []string {
	if dbEngine(key) != "redis" || password == "" {
		return nil
	}
	return []string{"redis-server", "--requirepass", password}
}
