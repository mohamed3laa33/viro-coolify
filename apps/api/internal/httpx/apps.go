package httpx

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/identity"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/kube"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/platform"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// orgAuthz returns middleware that requires the caller to be a member of the
// {orgID} organization with at least the given role.
func (s *Server) orgAuthz(min domain.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := userFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			orgID := chi.URLParam(r, "orgID")
			if _, err := s.identity.Authorize(r.Context(), user.ID, orgID, min); err != nil {
				if errors.Is(err, identity.ErrForbidden) {
					writeError(w, http.StatusForbidden, "you do not have access to this organization")
					return
				}
				s.logger.Error("authorize", "err", err)
				writeError(w, http.StatusInternalServerError, "authorization error")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writePlatformError maps platform/store errors to HTTP codes.
func (s *Server) writePlatformError(w http.ResponseWriter, action string, err error) {
	switch {
	case errors.Is(err, platform.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrInvalid):
		// A referential/shape violation the DB rejected (FK/not-null/check) — a
		// client/data error, not a backend fault.
		writeError(w, http.StatusUnprocessableEntity, "invalid reference or value")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, platform.ErrPaymentRequired):
		writeError(w, http.StatusPaymentRequired, err.Error())
	case errors.Is(err, platform.ErrQuotaExceeded):
		writeError(w, http.StatusPaymentRequired, err.Error())
	case errors.Is(err, platform.ErrInvalidTemplate):
		writeError(w, http.StatusBadRequest, "unknown catalog template")
	case errors.Is(err, platform.ErrInvalidRegion):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, platform.ErrInvalidDomain):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, platform.ErrDomainTaken):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, platform.ErrNoImage):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, platform.ErrNoRelease):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, platform.ErrInvalidScale):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.logger.Error(action, "err", err)
		writeError(w, http.StatusBadGateway, "upstream error from deploy backend")
	}
}

type createAppRequest struct {
	Name          string  `json:"name"`
	ProjectID     string  `json:"projectId"`
	Image         string  `json:"image"`
	GitRepository string  `json:"gitRepository"`
	GitBranch     string  `json:"gitBranch"`
	BuildPack     string  `json:"buildPack"`
	CPU           float64 `json:"cpu"`
	MemoryMB      int     `json:"memoryMb"`
	Region        string  `json:"region"`
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	orgID := chi.URLParam(r, "orgID")
	apps, err := s.platform.ListApps(r.Context(), orgID)
	if err != nil {
		s.writePlatformError(w, "list apps", err)
		return
	}
	ids, isAdmin, err := s.identity.AccessibleProjectIDs(r.Context(), user.ID, orgID)
	if err != nil {
		s.logger.Error("list apps: accessible projects", "err", err)
		writeError(w, http.StatusInternalServerError, "authorization error")
		return
	}
	if !isAdmin {
		filtered := apps[:0:0]
		for _, a := range apps {
			if ids[a.ProjectID] {
				filtered = append(filtered, a)
			}
		}
		apps = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": apps})
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var req createAppRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	orgID := chi.URLParam(r, "orgID")
	// Apps belong to a project; default to the org's default project when unspecified.
	projectID := req.ProjectID
	if projectID == "" {
		if p, err := s.identity.DefaultProject(r.Context(), orgID); err == nil {
			projectID = p.ID
		}
	}
	app, err := s.platform.CreateApp(r.Context(), orgID, platform.CreateAppInput{
		Name:          req.Name,
		ProjectID:     projectID,
		Image:         req.Image,
		GitRepository: req.GitRepository,
		GitBranch:     req.GitBranch,
		BuildPack:     req.BuildPack,
		CPU:           req.CPU,
		MemoryMB:      req.MemoryMB,
		Region:        req.Region,
	})
	if err != nil {
		s.writePlatformError(w, "create app", err)
		return
	}
	writeJSON(w, http.StatusCreated, app)
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	orgID, appID := chi.URLParam(r, "orgID"), chi.URLParam(r, "appID")
	app, err := s.platform.GetApp(r.Context(), orgID, appID)
	if err != nil {
		s.writePlatformError(w, "get app", err)
		return
	}
	// Surface the currently-active release on the app detail (best-effort: a
	// release lookup error is logged, not fatal — the app itself still renders).
	cur, cerr := s.platform.CurrentRelease(r.Context(), appID)
	if cerr != nil {
		s.logger.Warn("get app: current release", "app", appID, "err", cerr)
	}
	// Embed *domain.App so its JSON flattens TOP-LEVEL (preserving the existing web
	// client contract that reads the app fields, e.g. "id", at the top level) and add
	// currentRelease as a sibling field rather than nesting the app under {"app":...}.
	writeJSON(w, http.StatusOK, appDetailResponse{App: app, CurrentRelease: cur})
}

// appDetailResponse is the GET /apps/{id} body: the app's fields are flattened to
// the top level (embedded *domain.App) and currentRelease rides alongside them, so
// the response stays backward-compatible with clients reading app fields top-level.
type appDetailResponse struct {
	*domain.App
	CurrentRelease *domain.Release `json:"currentRelease,omitempty"`
}

type updateAppRequest struct {
	Image         *string  `json:"image"`
	CPU           *float64 `json:"cpu"`
	MemoryMB      *int     `json:"memoryMb"`
	GitRepository *string  `json:"gitRepository"`
	GitBranch     *string  `json:"gitBranch"`
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	var req updateAppRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	app, err := s.platform.UpdateApp(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"),
		platform.UpdateAppInput{
			Image:         req.Image,
			CPU:           req.CPU,
			MemoryMB:      req.MemoryMB,
			GitRepository: req.GitRepository,
			GitBranch:     req.GitBranch,
		})
	if err != nil {
		s.writePlatformError(w, "update app", err)
		return
	}
	writeJSON(w, http.StatusOK, app)
}

type scaleAppRequest struct {
	MinReplicas *int `json:"minReplicas"`
	MaxReplicas *int `json:"maxReplicas"`
}

func (s *Server) handleScaleApp(w http.ResponseWriter, r *http.Request) {
	var req scaleAppRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	app, err := s.platform.ScaleApp(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"),
		platform.ScaleAppInput{MinReplicas: req.MinReplicas, MaxReplicas: req.MaxReplicas})
	if err != nil {
		s.writePlatformError(w, "scale app", err)
		return
	}
	writeJSON(w, http.StatusAccepted, app)
}

func (s *Server) handleListReleases(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	rels, err := s.platform.ListReleases(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"), page)
	if err != nil {
		s.writePlatformError(w, "list releases", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": rels,
		"page": pageMeta(page, len(rels), -1),
	})
}

type rollbackRequest struct {
	Revision int `json:"revision"`
}

func (s *Server) handleRollbackApp(w http.ResponseWriter, r *http.Request) {
	var req rollbackRequest
	// The body is optional (default: previous release); tolerate an empty body.
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	app, err := s.platform.RollbackApp(r.Context(),
		chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"), req.Revision)
	if err != nil {
		s.writePlatformError(w, "rollback app", err)
		return
	}
	writeJSON(w, http.StatusAccepted, app)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	if err := s.platform.Delete(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID")); err != nil {
		s.writePlatformError(w, "delete app", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeployApp(w http.ResponseWriter, r *http.Request) {
	app, err := s.platform.Deploy(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"))
	if err != nil {
		s.writePlatformError(w, "deploy app", err)
		return
	}
	writeJSON(w, http.StatusAccepted, app)
}

func (s *Server) handleStopApp(w http.ResponseWriter, r *http.Request) {
	app, err := s.platform.Stop(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"))
	if err != nil {
		s.writePlatformError(w, "stop app", err)
		return
	}
	writeJSON(w, http.StatusAccepted, app)
}

func (s *Server) handleRestartApp(w http.ResponseWriter, r *http.Request) {
	app, err := s.platform.Restart(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"))
	if err != nil {
		s.writePlatformError(w, "restart app", err)
		return
	}
	writeJSON(w, http.StatusAccepted, app)
}

// handleAppStatus returns the live deploy/rollout status for an app so the UI can
// render deploy progress (rollout phase, desired/updated/ready/available replica
// counts, health). It resolves the app org-scoped (cross-tenant access is hidden
// as 404 by GetApp) and then reads the live status from the kube backend.
//
// HONESTY (invariant #6): an app that has never been deployed has no Helm release,
// so there is nothing to observe — we return an explicit not-deployed status
// rather than calling the backend (which would fabricate an empty "rollout"). The
// stored app status (e.g. "deploying", "build_failed") is surfaced alongside so a
// client can distinguish a never-deployed app from a failed/in-flight one.
func (s *Server) handleAppStatus(w http.ResponseWriter, r *http.Request) {
	orgID, appID := chi.URLParam(r, "orgID"), chi.URLParam(r, "appID")
	app, err := s.platform.GetApp(r.Context(), orgID, appID)
	if err != nil {
		s.writePlatformError(w, "app status", err)
		return
	}
	if app.Release == "" {
		// No release yet: report the stored intent honestly, with empty replica
		// counts, instead of asking the backend about a release that doesn't exist.
		writeJSON(w, http.StatusOK, map[string]any{
			"deployed": false,
			"status":   app.Status,
		})
		return
	}
	st, err := s.backend.AppRolloutStatus(r.Context(), app.Namespace, app.Release)
	if err != nil {
		// A backend read failure is an upstream/dependency fault, not a 404 — surface
		// it as a bad-gateway so the client knows the status is unknown (never faked).
		s.logger.Error("app status", "org", orgID, "app", appID, "err", err)
		writeError(w, http.StatusBadGateway, "could not read rollout status from deploy backend")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deployed": true,
		"status":   app.Status,
		"rollout":  st,
	})
}

func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	orgID, appID := chi.URLParam(r, "orgID"), chi.URLParam(r, "appID")
	// ?follow=true upgrades to a live Server-Sent Events stream; otherwise the
	// existing one-shot snapshot is returned unchanged.
	if isTrue(r.URL.Query().Get("follow")) {
		s.streamAppLogs(w, r, orgID, appID)
		return
	}
	logs, err := s.platform.AppLogs(r.Context(), orgID, appID)
	if err != nil {
		s.writePlatformError(w, "app logs", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

// streamAppLogs streams an app's pod logs as Server-Sent Events. Each log line is
// emitted as one `data:` event and flushed immediately. The stream is bound to
// the request context, so a client disconnect cancels the backend stream (no
// goroutine leak). The endpoint is already tenant-scoped by the app-project authz
// middleware on the route, and AppLogStream re-checks org ownership.
func (s *Server) streamAppLogs(w http.ResponseWriter, r *http.Request, orgID, appID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering so events flush
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	opts := kube.LogStreamOptions{
		Follow:    true,
		TailLines: 200,
		AllPods:   isTrue(r.URL.Query().Get("all")),
	}
	sw := &sseLineWriter{w: w, flusher: flusher}
	if err := s.platform.AppLogStream(r.Context(), orgID, appID, opts, sw); err != nil {
		// A cancelled context (client disconnect) is the normal stop condition.
		if r.Context().Err() != nil {
			return
		}
		// The headers/200 are already written, so signal failure as an SSE error
		// event. The detail is LOGGED (not echoed) so backend error text never
		// reaches the client response body.
		s.logger.Error("app log stream", "org", orgID, "app", appID, "err", err)
		_, _ = io.WriteString(w, "event: error\ndata: log stream ended\n\n")
		flusher.Flush()
	}
}

// isTrue parses a truthy query flag (true/1/yes/on).
func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// sseLineWriter adapts the per-line io.Writer the backend log stream expects into
// Server-Sent Events: every newline-terminated line written by the backend is
// emitted as one `data:` event and flushed so the client sees it immediately.
type sseLineWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	buf     []byte
}

func (s *sseLineWriter) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for {
		i := indexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := s.buf[:i]
		s.buf = s.buf[i+1:]
		if _, err := io.WriteString(s.w, "data: "+sseEscape(string(line))+"\n\n"); err != nil {
			return 0, err
		}
		s.flusher.Flush()
	}
	return len(p), nil
}

// sseEscape replaces characters that would corrupt an SSE frame. Newlines are the
// only structural concern (each becomes a separate data line); we collapse them so
// a single backend line stays a single event payload.
func sseEscape(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\n", " ")
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func (s *Server) handleListBuilds(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	builds, err := s.platform.ListBuilds(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"), page)
	if err != nil {
		s.writePlatformError(w, "list builds", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": builds,
		"page": pageMeta(page, len(builds), -1),
	})
}

func (s *Server) handleGetBuild(w http.ResponseWriter, r *http.Request) {
	b, err := s.platform.GetBuild(r.Context(),
		chi.URLParam(r, "orgID"), chi.URLParam(r, "appID"), chi.URLParam(r, "buildID"))
	if err != nil {
		s.writePlatformError(w, "get build", err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

type createDatabaseRequest struct {
	Name      string  `json:"name"`
	Engine    string  `json:"engine"`
	ProjectID string  `json:"projectId"`
	CPU       float64 `json:"cpu"`
	MemoryMB  int     `json:"memoryMb"`
	StorageGB int     `json:"storageGb"`
	Region    string  `json:"region"`
}

func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	orgID := chi.URLParam(r, "orgID")
	dbs, err := s.platform.ListDatabases(r.Context(), orgID)
	if err != nil {
		s.writePlatformError(w, "list databases", err)
		return
	}
	ids, isAdmin, err := s.identity.AccessibleProjectIDs(r.Context(), user.ID, orgID)
	if err != nil {
		s.logger.Error("list databases: accessible projects", "err", err)
		writeError(w, http.StatusInternalServerError, "authorization error")
		return
	}
	if !isAdmin {
		filtered := dbs[:0:0]
		for _, d := range dbs {
			if ids[d.ProjectID] {
				filtered = append(filtered, d)
			}
		}
		dbs = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": dbs})
}

func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	var req createDatabaseRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	orgID := chi.URLParam(r, "orgID")
	projectID := req.ProjectID
	if projectID == "" {
		if p, err := s.identity.DefaultProject(r.Context(), orgID); err == nil {
			projectID = p.ID
		}
	}
	db, err := s.platform.CreateDatabase(r.Context(), orgID, platform.CreateDatabaseInput{
		Name:      req.Name,
		Engine:    req.Engine,
		ProjectID: projectID,
		CPU:       req.CPU,
		MemoryMB:  req.MemoryMB,
		StorageGB: req.StorageGB,
		Region:    req.Region,
	})
	if err != nil {
		s.writePlatformError(w, "create database", err)
		return
	}
	writeJSON(w, http.StatusCreated, db)
}

// handleGetDatabase returns one database plus its in-cluster connection info
// (host/port/credentials/connectionString). Databases are internal-only so the
// host is the cluster-internal service DNS. Cross-tenant access is hidden as 404.
func (s *Server) handleGetDatabase(w http.ResponseWriter, r *http.Request) {
	detail, err := s.platform.GetDatabaseDetail(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "databaseID"))
	if err != nil {
		s.writePlatformError(w, "get database", err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleDeployDatabase(w http.ResponseWriter, r *http.Request) {
	db, err := s.platform.DeployDatabase(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "databaseID"))
	if err != nil {
		s.writePlatformError(w, "deploy database", err)
		return
	}
	writeJSON(w, http.StatusOK, db)
}

func (s *Server) handleStopDatabase(w http.ResponseWriter, r *http.Request) {
	db, err := s.platform.StopDatabase(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "databaseID"))
	if err != nil {
		s.writePlatformError(w, "stop database", err)
		return
	}
	writeJSON(w, http.StatusOK, db)
}

func (s *Server) handleRestartDatabase(w http.ResponseWriter, r *http.Request) {
	db, err := s.platform.RestartDatabase(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "databaseID"))
	if err != nil {
		s.writePlatformError(w, "restart database", err)
		return
	}
	writeJSON(w, http.StatusOK, db)
}

func (s *Server) handleDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	if err := s.platform.DeleteDatabase(r.Context(), chi.URLParam(r, "orgID"), chi.URLParam(r, "databaseID")); err != nil {
		s.writePlatformError(w, "delete database", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
