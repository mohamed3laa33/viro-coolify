package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
)

// Managed-database backup & restore HTTP surface.
//
// These handlers delegate the actual backup work to the kube backend
// (BackupDatabase / ListDatabaseBackups / RestoreDatabase), which the kube-backup
// agent implements against the real cluster (and as no-ops on FakeBackend). The
// handlers here resolve the database org-scoped (cross-tenant access is hidden as
// 404 by the platform layer, which also DECRYPTS the stored credentials on read so
// the dump/restore client can authenticate), enforce the "must be deployed"
// precondition, and map backend/store errors to status codes. They never fabricate
// a backup record (invariant #6). Admin/DB-driven backup tuning (client image,
// PVC size/class) is left to the kube layer's engine defaults here — those are
// platform-settings concerns, never hardcoded business values (invariant #1).

// handleBackupDatabase triggers an on-demand backup of a managed database and
// returns the created backup descriptor (admin+). A database with no Helm release
// has never been deployed, so there is nothing to back up — we reject that with
// 409 rather than asking the backend to back up a non-existent workload.
func (s *Server) handleBackupDatabase(w http.ResponseWriter, r *http.Request) {
	orgID, dbID := chi.URLParam(r, "orgID"), chi.URLParam(r, "databaseID")
	db, err := s.platform.GetDatabase(r.Context(), orgID, dbID)
	if err != nil {
		s.writePlatformError(w, "backup database", err)
		return
	}
	if db.Release == "" {
		writeError(w, http.StatusConflict, "database is not deployed; nothing to back up")
		return
	}
	backup, err := s.backend.BackupDatabase(r.Context(), kube.BackupSpec{
		Namespace: db.Namespace,
		Release:   db.Release,
		Engine:    db.Engine,
		Database:  db.DatabaseName,
		Username:  db.Username,
		Password:  db.Password,
	})
	if err != nil {
		// Triggering a backup is an upstream/backend operation; a failure is a
		// dependency fault (not a 404) and is never reported as success.
		s.logger.Error("backup database", "org", orgID, "database", dbID, "err", err)
		writeError(w, http.StatusBadGateway, "could not trigger database backup")
		return
	}
	s.audit(r.Context(), orgID, "database.backup", "database", dbID, "engine="+db.Engine)
	writeJSON(w, http.StatusAccepted, backup)
}

// handleListDatabaseBackups lists the available backups for a managed database
// (member+, so a viewer can see backup/restore points). A database that has never
// been deployed has no backups: return an empty list rather than an error.
func (s *Server) handleListDatabaseBackups(w http.ResponseWriter, r *http.Request) {
	orgID, dbID := chi.URLParam(r, "orgID"), chi.URLParam(r, "databaseID")
	db, err := s.platform.GetDatabase(r.Context(), orgID, dbID)
	if err != nil {
		s.writePlatformError(w, "list database backups", err)
		return
	}
	if db.Release == "" {
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
		return
	}
	backups, err := s.backend.ListDatabaseBackups(r.Context(), db.Namespace, db.Release)
	if err != nil {
		s.logger.Error("list database backups", "org", orgID, "database", dbID, "err", err)
		writeError(w, http.StatusBadGateway, "could not list database backups")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": backups})
}

type restoreDatabaseRequest struct {
	// BackupName is the dump file / backup descriptor name (DatabaseBackup.Name)
	// to restore from, as returned by the list-backups endpoint. Required — we
	// never silently "restore the latest".
	BackupName string `json:"backupName"`
}

// handleRestoreDatabase restores a managed database from one of its backups
// (admin+, DESTRUCTIVE — it overwrites current data). The target backup name is
// required and the database must be deployed.
func (s *Server) handleRestoreDatabase(w http.ResponseWriter, r *http.Request) {
	orgID, dbID := chi.URLParam(r, "orgID"), chi.URLParam(r, "databaseID")
	var req restoreDatabaseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.BackupName == "" {
		writeError(w, http.StatusBadRequest, "backupName is required")
		return
	}
	db, err := s.platform.GetDatabase(r.Context(), orgID, dbID)
	if err != nil {
		s.writePlatformError(w, "restore database", err)
		return
	}
	if db.Release == "" {
		writeError(w, http.StatusConflict, "database is not deployed; nothing to restore into")
		return
	}
	if err := s.backend.RestoreDatabase(r.Context(), kube.RestoreSpec{
		Namespace:  db.Namespace,
		Release:    db.Release,
		Engine:     db.Engine,
		Database:   db.DatabaseName,
		Username:   db.Username,
		Password:   db.Password,
		BackupName: req.BackupName,
	}); err != nil {
		s.logger.Error("restore database", "org", orgID, "database", dbID, "err", err)
		writeError(w, http.StatusBadGateway, "could not restore database backup")
		return
	}
	s.audit(r.Context(), orgID, "database.restore", "database", dbID, "backupName="+req.BackupName)
	w.WriteHeader(http.StatusAccepted)
}
