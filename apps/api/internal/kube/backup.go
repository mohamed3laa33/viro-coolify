package kube

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// Database backup / restore (logical dumps as Kubernetes Jobs/CronJobs)
//
// v1 data-durability foundation: an engine-appropriate logical dump
// (pg_dump / mysqldump / mongodump / redis SAVE) runs as a one-shot Job (or a
// daily CronJob) writing to a per-database backup PVC; restore is a Job that
// replays a named dump file back into the live database. This is intentionally
// the simple, durable baseline (no object-store streaming yet) the task scoped:
// "even a daily pg_dump CronJob + manual restore is acceptable for v1".
//
// SECURITY: the backup pod connects to the database over the cluster network as
// a CLIENT (it never mounts the database's volume), runs under the locked-down
// per-tenant ServiceAccount (no auto-mounted API token), drops ALL capabilities,
// and receives the DB password via env (PGPASSWORD/MYSQL_PWD/...) rather than on
// the command line so it never appears in `ps`/Job spec args.
// ---------------------------------------------------------------------------

const (
	// backupPVCName is the per-database PersistentVolumeClaim every backup/restore
	// Job mounts (created idempotently). One PVC per database holds all its dumps.
	backupPVCName = "vortex-backups"
	// backupMountPath is where the backup PVC is mounted in the dump/restore pod.
	backupMountPath = "/backups"
	// minBackupStorageGB is the floor for the backup PVC when spec.StorageGB is
	// unset, so a backup Job never fails to schedule for want of a sized volume.
	minBackupStorageGB = 1
	// backupJobTTL is how long a finished backup/restore Job (and its pod) lingers
	// before Kubernetes garbage-collects it, so logs/status stay inspectable for a
	// while without accumulating forever.
	backupJobTTLSeconds = 24 * 60 * 60
	// backupActiveDeadlineSeconds bounds a single dump/restore run.
	backupActiveDeadlineSeconds = 60 * 60
	// backupHistoryLimit bounds how many finished CronJob-spawned Jobs are kept.
	backupHistoryLimit = 7

	// backupLabel marks every backup/restore object for selection + cleanup.
	backupLabel = "vortex.io/backup"
	// backupReleaseLabel ties a backup object to its database release so
	// ListDatabaseBackups can select a single database's backups.
	backupReleaseLabel = "vortex.io/db-release"
	// backupKindLabel distinguishes a "backup" run from a "restore" run.
	backupKindLabel = "vortex.io/backup-kind"
	// backupScheduledAnnotation, set by the CronJob's job template, marks a run as
	// produced by the schedule (vs. an on-demand BackupDatabase call).
	backupScheduledAnnotation = "vortex.io/backup-scheduled"
)

// BackupDatabase submits a one-shot logical-backup Job for the database. It
// ensures the per-database backup PVC, renders the engine-appropriate dump
// command, and creates a uniquely-named Job (so repeated calls never collide).
// It does NOT wait for completion — the caller polls ListDatabaseBackups.
func (b *KubeBackend) BackupDatabase(ctx context.Context, spec BackupSpec) (DatabaseBackup, error) {
	engine := dbEngine(spec.Engine)
	if engine == "" {
		return DatabaseBackup{}, fmt.Errorf("kube: unsupported backup engine %q", spec.Engine)
	}
	if strings.TrimSpace(spec.Release) == "" || strings.TrimSpace(spec.Namespace) == "" {
		return DatabaseBackup{}, fmt.Errorf("kube: backup requires namespace and release")
	}
	spec.Host, spec.Port = backupConn(spec.Host, spec.Port, spec.Release, engine)
	if err := b.ensureBackupPVC(ctx, spec); err != nil {
		return DatabaseBackup{}, err
	}

	name := backupJobName(spec.Release)
	file := name + dumpExtension(engine)
	job := b.backupJob(name, file, spec, false)
	if _, err := b.client.BatchV1().Jobs(spec.Namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return DatabaseBackup{}, fmt.Errorf("kube: create backup job %s/%s: %w", spec.Namespace, name, err)
	}
	return DatabaseBackup{
		Name:      name,
		Phase:     "Pending",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Engine:    engine,
	}, nil
}

// EnsureBackupSchedule installs/updates (or, for an empty Schedule, removes) the
// per-database backup CronJob. The CronJob's pod template is identical to an
// on-demand backup except it stamps a timestamped dump file per run and marks the
// run scheduled. It is upserted so a settings change (schedule/size/image) takes
// effect on the next reconcile.
func (b *KubeBackend) EnsureBackupSchedule(ctx context.Context, spec BackupSpec) error {
	engine := dbEngine(spec.Engine)
	if engine == "" {
		return fmt.Errorf("kube: unsupported backup engine %q", spec.Engine)
	}
	cronName := backupCronName(spec.Release)
	// An empty schedule disables scheduled backups: delete any existing CronJob.
	if strings.TrimSpace(spec.Schedule) == "" {
		err := b.client.BatchV1().CronJobs(spec.Namespace).Delete(ctx, cronName, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("kube: delete backup cronjob %s/%s: %w", spec.Namespace, cronName, err)
		}
		return nil
	}
	spec.Host, spec.Port = backupConn(spec.Host, spec.Port, spec.Release, engine)
	if err := b.ensureBackupPVC(ctx, spec); err != nil {
		return err
	}

	cron := b.backupCronJob(cronName, spec)
	return upsert(ctx,
		func() error {
			_, err := b.client.BatchV1().CronJobs(spec.Namespace).Create(ctx, cron, metav1.CreateOptions{})
			return err
		},
		func() error {
			cur, err := b.client.BatchV1().CronJobs(spec.Namespace).Get(ctx, cronName, metav1.GetOptions{})
			if err != nil {
				return err
			}
			cur.Spec = cron.Spec
			cur.Labels = cron.Labels
			_, err = b.client.BatchV1().CronJobs(spec.Namespace).Update(ctx, cur, metav1.UpdateOptions{})
			return err
		},
	)
}

// ListDatabaseBackups returns the database's backup Jobs (on-demand and
// CronJob-spawned), newest first, each with its observed phase. It selects ONLY
// backup runs (not restore runs) for the given release. An absent set returns an
// empty slice rather than an error so the UI shows "no backups yet".
func (b *KubeBackend) ListDatabaseBackups(ctx context.Context, namespace, release string) ([]DatabaseBackup, error) {
	sel := fmt.Sprintf("%s=true,%s=%s,%s=backup",
		backupLabel, backupReleaseLabel, sanitize(release), backupKindLabel)
	jobs, err := b.client.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return nil, fmt.Errorf("kube: list backup jobs %s/%s: %w", namespace, release, err)
	}
	out := make([]DatabaseBackup, 0, len(jobs.Items))
	for i := range jobs.Items {
		out = append(out, backupFromJob(&jobs.Items[i]))
	}
	// Newest first by creation time (Job names embed a timestamp, but sort on the
	// authoritative CreationTimestamp).
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// RestoreDatabase submits a Job that replays spec.BackupName (a dump file on the
// backup PVC) into the live database. It returns once the Job is SUBMITTED; the
// caller polls for completion. DESTRUCTIVE — the platform/handler layer must gate
// it behind an explicit confirmation.
func (b *KubeBackend) RestoreDatabase(ctx context.Context, spec RestoreSpec) error {
	engine := dbEngine(spec.Engine)
	if engine == "" {
		return fmt.Errorf("kube: unsupported restore engine %q", spec.Engine)
	}
	if strings.TrimSpace(spec.BackupName) == "" {
		return fmt.Errorf("kube: restore requires a backup name")
	}
	if strings.TrimSpace(spec.Release) == "" || strings.TrimSpace(spec.Namespace) == "" {
		return fmt.Errorf("kube: restore requires namespace and release")
	}
	spec.Host, spec.Port = backupConn(spec.Host, spec.Port, spec.Release, engine)
	// The dump file is the backup Job name plus the engine extension.
	file := sanitize(spec.BackupName) + dumpExtension(engine)
	name := restoreJobName(spec.Release)
	job := b.restoreJob(name, file, spec)
	if _, err := b.client.BatchV1().Jobs(spec.Namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("kube: create restore job %s/%s: %w", spec.Namespace, name, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Job/CronJob/PVC rendering
// ---------------------------------------------------------------------------

// ensureBackupPVC creates (idempotently) the per-database backup PVC the
// dump/restore pods mount. The size/class are admin/DB-driven (BackupSpec);
// StorageGB<=0 clamps to a safe minimum so a backup never fails for an unsized
// volume.
func (b *KubeBackend) ensureBackupPVC(ctx context.Context, spec BackupSpec) error {
	size := spec.StorageGB
	if size <= 0 {
		size = minBackupStorageGB
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupPVCName,
			Namespace: spec.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "vortex",
				backupLabel:                    "true",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(gib(size)),
				},
			},
		},
	}
	if sc := strings.TrimSpace(spec.StorageClass); sc != "" {
		pvc.Spec.StorageClassName = &sc
	}
	// A PVC's spec is immutable after creation (other than expansion), so on
	// AlreadyExists we keep the existing claim rather than attempting an update.
	_, err := b.client.CoreV1().PersistentVolumeClaims(spec.Namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("kube: ensure backup PVC %s/%s: %w", spec.Namespace, backupPVCName, err)
	}
	return nil
}

// backupJob renders a one-shot (or CronJob-templated) backup Job: an engine
// client container that runs the dump shell command, mounts the backup PVC, and
// gets the DB password via env (never on the args). scheduled marks a
// CronJob-spawned run.
func (b *KubeBackend) backupJob(name, file string, spec BackupSpec, scheduled bool) *batchv1.Job {
	engine := dbEngine(spec.Engine)
	annotations := map[string]string{}
	if scheduled {
		annotations[backupScheduledAnnotation] = "true"
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   spec.Namespace,
			Labels:      backupLabels(spec.Release, "backup"),
			Annotations: annotations,
		},
		Spec: backupJobSpec(
			name, engine,
			backupContainer("backup", file, spec.Host, spec.Port, spec.Database, spec.Username, dumpCommand(engine)),
			spec.Image, spec.Release, "backup", scheduled, spec.Password,
		),
	}
	return job
}

// restoreJob renders the restore Job: an engine client container that replays the
// named dump file back into the live database.
func (b *KubeBackend) restoreJob(name, file string, spec RestoreSpec) *batchv1.Job {
	engine := dbEngine(spec.Engine)
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: spec.Namespace,
			Labels:    backupLabels(spec.Release, "restore"),
		},
		Spec: backupJobSpec(
			name, engine,
			backupContainer("restore", file, spec.Host, spec.Port, spec.Database, spec.Username, restoreCommand(engine)),
			spec.Image, spec.Release, "restore", false, spec.Password,
		),
	}
}

// backupCronJob renders the scheduled-backup CronJob wrapping the same backup pod
// template (with a timestamped per-run dump file).
func (b *KubeBackend) backupCronJob(name string, spec BackupSpec) *batchv1.CronJob {
	engine := dbEngine(spec.Engine)
	// Each scheduled run writes a timestamped file so history is retained on the PVC.
	file := backupCronName(spec.Release) + "-$(date +%Y%m%d%H%M%S)" + dumpExtension(engine)
	jobSpec := backupJobSpec(
		name, engine,
		backupContainer("backup", file, spec.Host, spec.Port, spec.Database, spec.Username, dumpCommand(engine)),
		spec.Image, spec.Release, "backup", true, spec.Password,
	)
	forbid := batchv1.ForbidConcurrent
	history := int32(backupHistoryLimit)
	suspend := false
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: spec.Namespace,
			Labels:    backupLabels(spec.Release, "backup"),
		},
		Spec: batchv1.CronJobSpec{
			Schedule:                   spec.Schedule,
			ConcurrencyPolicy:          forbid,
			SuccessfulJobsHistoryLimit: &history,
			FailedJobsHistoryLimit:     &history,
			Suspend:                    &suspend,
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      backupLabels(spec.Release, "backup"),
					Annotations: map[string]string{backupScheduledAnnotation: "true"},
				},
				Spec: jobSpec,
			},
		},
	}
}

// backupJobSpec builds the shared JobSpec (backoff/TTL/deadline + a locked-down
// pod template) for both backup and restore Jobs. The dump/restore container and
// the DB password env are injected by the caller.
func backupJobSpec(jobName, engine string, c corev1.Container, image, release, kind string, scheduled bool, password string) batchv1.JobSpec {
	backoff := int32(2)
	ttl := int32(backupJobTTLSeconds)
	deadline := int64(backupActiveDeadlineSeconds)
	automount := false
	runAsNonRoot := true

	// Inject the DB password via env (PGPASSWORD/MYSQL_PWD/...) so it never appears
	// in the Job's args / `ps` output.
	if pwEnv := passwordEnvName(engine); pwEnv != "" && password != "" {
		c.Env = append(c.Env, corev1.EnvVar{Name: pwEnv, Value: password})
	}
	c.Image = clientImage(engine, image)

	podLabels := backupLabels(release, kind)
	podLabels["job-name"] = jobName
	podAnnotations := map[string]string{}
	if scheduled {
		podAnnotations[backupScheduledAnnotation] = "true"
	}

	return batchv1.JobSpec{
		BackoffLimit:            &backoff,
		TTLSecondsAfterFinished: &ttl,
		ActiveDeadlineSeconds:   &deadline,
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      podLabels,
				Annotations: podAnnotations,
			},
			Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				// Run under the locked-down per-tenant SA (no auto-mounted API token);
				// the backup pod is a network client only, it never touches the cluster API.
				ServiceAccountName:           tenantServiceAccount,
				AutomountServiceAccountToken: &automount,
				SecurityContext: &corev1.PodSecurityContext{
					RunAsNonRoot: &runAsNonRoot,
					SeccompProfile: &corev1.SeccompProfile{
						Type: corev1.SeccompProfileTypeRuntimeDefault,
					},
				},
				Containers: []corev1.Container{c},
				Volumes: []corev1.Volume{{
					Name: "backups",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: backupPVCName,
						},
					},
				}},
			},
		},
	}
}

// backupContainer renders the engine client container that runs the dump/restore
// shell command against the database. The password is added by backupJobSpec via
// env; non-secret connection params are set here.
func backupContainer(role, file, host string, port int, database, username, shellCmd string) corev1.Container {
	noEsc := false
	c := corev1.Container{
		Name:    role,
		Command: []string{"/bin/sh", "-ec"},
		Args:    []string{shellCmd},
		Env: []corev1.EnvVar{
			{Name: "DB_HOST", Value: host},
			{Name: "DB_PORT", Value: strconv.Itoa(port)},
			{Name: "DB_NAME", Value: database},
			{Name: "DB_USER", Value: username},
			{Name: "BACKUP_FILE", Value: backupMountPath + "/" + file},
		},
		VolumeMounts: []corev1.VolumeMount{{
			Name:      "backups",
			MountPath: backupMountPath,
		}},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &noEsc,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
	}
	return c
}

// ---------------------------------------------------------------------------
// Engine-appropriate dump/restore commands & client images
// ---------------------------------------------------------------------------

// dumpCommand returns the engine-appropriate dump shell command. It reads
// connection params from env (DB_HOST/DB_PORT/DB_NAME/DB_USER, password via the
// engine's password env) and writes to $BACKUP_FILE on the mounted PVC. DB_HOST
// falls back to the release name (the in-cluster Service) when unset.
func dumpCommand(engine string) string {
	switch engine {
	case "postgresql":
		return `pg_dump -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" -F c -f "${BACKUP_FILE}"`
	case "mysql", "mariadb":
		return `mysqldump -h "${DB_HOST}" -P "${DB_PORT}" -u "${DB_USER}" --single-transaction --routines --triggers "${DB_NAME}" > "${BACKUP_FILE}"`
	case "mongodb":
		return `mongodump --host "${DB_HOST}" --port "${DB_PORT}" --username "${DB_USER}" --password "${MONGO_PASSWORD}" --db "${DB_NAME}" --archive="${BACKUP_FILE}" --gzip`
	case "redis":
		// redis-cli --rdb dumps the dataset to a local RDB file.
		return `redis-cli -h "${DB_HOST}" -p "${DB_PORT}" -a "${REDISCLI_AUTH}" --no-auth-warning --rdb "${BACKUP_FILE}"`
	default:
		return `echo "kube: no dump command for this engine" >&2; exit 1`
	}
}

// restoreCommand returns the engine-appropriate restore shell command, replaying
// $BACKUP_FILE back into the live database.
func restoreCommand(engine string) string {
	switch engine {
	case "postgresql":
		// --clean --if-exists drops existing objects before recreating them so the
		// restore is idempotent over the current data.
		return `pg_restore -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" --clean --if-exists "${BACKUP_FILE}"`
	case "mysql", "mariadb":
		return `mysql -h "${DB_HOST}" -P "${DB_PORT}" -u "${DB_USER}" "${DB_NAME}" < "${BACKUP_FILE}"`
	case "mongodb":
		return `mongorestore --host "${DB_HOST}" --port "${DB_PORT}" --username "${DB_USER}" --password "${MONGO_PASSWORD}" --db "${DB_NAME}" --archive="${BACKUP_FILE}" --gzip --drop`
	case "redis":
		return `echo "kube: redis restore is a manual RDB swap (copy ${BACKUP_FILE} to the data dir and restart)" >&2; exit 1`
	default:
		return `echo "kube: no restore command for this engine" >&2; exit 1`
	}
}

// backupConn fills in the connection host/port defaults: an empty host falls
// back to the release name (the in-cluster ClusterIP Service the chart renders
// for the database), and a non-positive port falls back to the engine's default
// port (via the shared defaultPort recipe, keyed by the engine/template).
func backupConn(host string, port int, release, engine string) (string, int) {
	if strings.TrimSpace(host) == "" {
		host = sanitize(release)
	}
	if port <= 0 {
		port = defaultPort(Workload{ServiceTemplateKey: engine})
	}
	return host, port
}

// passwordEnvName returns the env var the engine's dump/restore client reads the
// password from, so the password is delivered out-of-band of the args.
func passwordEnvName(engine string) string {
	switch engine {
	case "postgresql":
		return "PGPASSWORD"
	case "mysql", "mariadb":
		return "MYSQL_PWD"
	case "mongodb":
		return "MONGO_PASSWORD"
	case "redis":
		return "REDISCLI_AUTH"
	default:
		return ""
	}
}

// clientImage resolves the dump/restore client image: the admin/DB-driven
// override when set, else a conservative engine-default client image so a backup
// still runs in dev. The defaults are client tooling, NOT business policy
// (invariant #1 governs plans/pricing/quotas, not the dump binary's image).
func clientImage(engine, override string) string {
	if o := strings.TrimSpace(override); o != "" {
		return o
	}
	switch engine {
	case "postgresql":
		return "postgres:16-alpine"
	case "mysql":
		return "mysql:8"
	case "mariadb":
		return "mariadb:11"
	case "mongodb":
		return "mongo:7"
	case "redis":
		return "redis:7-alpine"
	default:
		return "busybox:1.36"
	}
}

// dumpExtension is the dump file suffix per engine (custom-format for pg, gzip
// archive for mongo, sql for mysql, rdb for redis).
func dumpExtension(engine string) string {
	switch engine {
	case "postgresql":
		return ".dump"
	case "mysql", "mariadb":
		return ".sql"
	case "mongodb":
		return ".archive.gz"
	case "redis":
		return ".rdb"
	default:
		return ".bak"
	}
}

// ---------------------------------------------------------------------------
// Naming, labels, status derivation
// ---------------------------------------------------------------------------

// backupLabels are the selector labels stamped on backup/restore objects so they
// can be listed (per release) and cleaned up.
func backupLabels(release, kind string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/managed-by": "vortex",
		backupLabel:                    "true",
		backupReleaseLabel:             sanitize(release),
		backupKindLabel:                kind,
	}
}

// backupJobName is a unique on-demand backup Job name: "bkp-<release>-<ts>".
func backupJobName(release string) string {
	return short("bkp-"+sanitize(release), 40) + "-" + timestampSuffix()
}

// restoreJobName is a unique restore Job name: "rst-<release>-<ts>".
func restoreJobName(release string) string {
	return short("rst-"+sanitize(release), 40) + "-" + timestampSuffix()
}

// backupCronName is the stable per-database backup CronJob name.
func backupCronName(release string) string {
	return short("bkp-cron-"+sanitize(release), 52)
}

// timestampSuffix returns a compact, DNS-safe timestamp for unique Job names.
func timestampSuffix() string {
	return time.Now().UTC().Format("20060102150405")
}

// short trims s to at most n chars and re-trims a trailing separator so the
// result stays a valid DNS-1123 label segment.
func short(s string, n int) string {
	if len(s) > n {
		s = strings.Trim(s[:n], "-")
	}
	return s
}

// backupFromJob derives a DatabaseBackup descriptor from a backup Job's observed
// status. Phase reflects the Job's REAL condition — no fabricated success.
func backupFromJob(job *batchv1.Job) DatabaseBackup {
	db := DatabaseBackup{
		Name:      job.Name,
		Phase:     jobPhase(job),
		CreatedAt: job.CreationTimestamp.UTC().Format(time.RFC3339),
		Scheduled: job.Annotations[backupScheduledAnnotation] == "true",
	}
	if job.Status.CompletionTime != nil {
		db.CompletedAt = job.Status.CompletionTime.UTC().Format(time.RFC3339)
	}
	return db
}

// jobPhase maps a Job's status onto a coarse phase string. It checks the
// Complete/Failed conditions first (authoritative), then falls back to the
// active/succeeded/failed counts.
func jobPhase(job *batchv1.Job) string {
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			return "Succeeded"
		}
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return "Failed"
		}
	}
	switch {
	case job.Status.Active > 0:
		return "Running"
	case job.Status.Succeeded > 0:
		return "Succeeded"
	case job.Status.Failed > 0:
		return "Failed"
	default:
		return "Pending"
	}
}
