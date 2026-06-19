package client

import (
	"context"
	"net/http"
	"net/url"
)

// --- auth ---

// Signup registers a new user and returns the token pair.
func (c *Client) Signup(ctx context.Context, email, name, password string) (*AuthResult, error) {
	var out authResponse
	err := c.requestNoAuth(ctx, http.MethodPost, "/v1/auth/signup",
		signupRequest{Email: email, Name: name, Password: password}, &out)
	if err != nil {
		return nil, err
	}
	return toAuthResult(out), nil
}

// Login authenticates a user and returns the token pair.
func (c *Client) Login(ctx context.Context, email, password string) (*AuthResult, error) {
	var out authResponse
	err := c.requestNoAuth(ctx, http.MethodPost, "/v1/auth/login",
		loginRequest{Email: email, Password: password}, &out)
	if err != nil {
		return nil, err
	}
	return toAuthResult(out), nil
}

func toAuthResult(out authResponse) *AuthResult {
	return &AuthResult{User: out.User, AccessToken: out.AccessToken, RefreshToken: out.RefreshToken}
}

// --- personal access tokens (PAT) ---

// CreateToken issues a personal access token. The plaintext token is returned
// ONLY here (shown once) on the result's Token field.
func (c *Client) CreateToken(ctx context.Context, in CreateTokenInput) (*ApiToken, error) {
	var tok ApiToken
	err := c.request(ctx, http.MethodPost, "/v1/tokens", createTokenRequest(in), &tok)
	if err != nil {
		return nil, err
	}
	// Normalize: some servers return the secret as "plainToken".
	if tok.Token == "" && tok.PlainToken != "" {
		tok.Token = tok.PlainToken
	}
	return &tok, nil
}

// ListTokens returns the caller's personal access tokens (never the secrets).
func (c *Client) ListTokens(ctx context.Context) ([]ApiToken, error) {
	var out dataEnvelope[ApiToken]
	if err := c.request(ctx, http.MethodGet, "/v1/tokens", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// RevokeToken deletes a personal access token by id.
func (c *Client) RevokeToken(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, "/v1/tokens/"+url.PathEscape(id), nil, nil)
}

// Me returns the authenticated user.
func (c *Client) Me(ctx context.Context) (*User, error) {
	var u User
	if err := c.request(ctx, http.MethodGet, "/v1/me", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// Version returns the API server version (public endpoint).
func (c *Client) Version(ctx context.Context) (*Version, error) {
	var v Version
	if err := c.requestNoAuth(ctx, http.MethodGet, "/v1/version", nil, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// --- orgs ---

// ListOrgs returns the organizations the caller belongs to.
func (c *Client) ListOrgs(ctx context.Context) ([]Org, error) {
	var out dataEnvelope[Org]
	if err := c.request(ctx, http.MethodGet, "/v1/orgs", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// CreateOrg creates a new organization.
func (c *Client) CreateOrg(ctx context.Context, name string) (*Org, error) {
	var org Org
	if err := c.request(ctx, http.MethodPost, "/v1/orgs", map[string]string{"name": name}, &org); err != nil {
		return nil, err
	}
	return &org, nil
}

// --- projects ---

// ListProjects returns the projects within an org.
func (c *Client) ListProjects(ctx context.Context, orgID string) ([]Project, error) {
	var out dataEnvelope[Project]
	if err := c.request(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(orgID)+"/projects", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// CreateProject creates a project within an org.
func (c *Client) CreateProject(ctx context.Context, orgID, name string) (*Project, error) {
	var p Project
	err := c.request(ctx, http.MethodPost, "/v1/orgs/"+url.PathEscape(orgID)+"/projects",
		map[string]string{"name": name}, &p)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// --- apps ---

// ListApps returns the apps in an org.
func (c *Client) ListApps(ctx context.Context, orgID string) ([]App, error) {
	var out dataEnvelope[App]
	if err := c.request(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(orgID)+"/apps", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// ListProjectApps returns the apps in a project within an org.
func (c *Client) ListProjectApps(ctx context.Context, orgID, projectID string) ([]App, error) {
	var out dataEnvelope[App]
	path := "/v1/orgs/" + url.PathEscape(orgID) + "/projects/" + url.PathEscape(projectID) + "/apps"
	if err := c.request(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// CreateApp creates an app in an org. When in.ProjectID is set, it is sent in
// the body and the org-level endpoint routes it to that project.
func (c *Client) CreateApp(ctx context.Context, orgID string, in CreateAppInput) (*App, error) {
	body := createAppRequest(in)
	var app App
	if err := c.request(ctx, http.MethodPost, "/v1/orgs/"+url.PathEscape(orgID)+"/apps", body, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// GetApp returns one app with its currently-active release.
func (c *Client) GetApp(ctx context.Context, orgID, appID string) (*AppDetail, error) {
	var d AppDetail
	if err := c.request(ctx, http.MethodGet, appPath(orgID, appID), nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// UpdateApp patches an app's image / resources / git source. A nil input field
// is omitted (left unchanged).
func (c *Client) UpdateApp(ctx context.Context, orgID, appID string, in UpdateAppInput) (*App, error) {
	body := updateAppRequest(in)
	var app App
	if err := c.request(ctx, http.MethodPatch, appPath(orgID, appID), body, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// ScaleApp sets an app's autoscaling bounds. A nil bound is left unchanged.
func (c *Client) ScaleApp(ctx context.Context, orgID, appID string, in ScaleAppInput) (*App, error) {
	var app App
	err := c.request(ctx, http.MethodPost, appPath(orgID, appID)+"/scale", scaleAppRequest(in), &app)
	if err != nil {
		return nil, err
	}
	return &app, nil
}

// ListReleases returns an app's release history (newest first per the API).
func (c *Client) ListReleases(ctx context.Context, orgID, appID string) ([]Release, error) {
	var out dataEnvelope[Release]
	if err := c.request(ctx, http.MethodGet, appPath(orgID, appID)+"/releases", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Rollback rolls an app back to a prior revision. A revision of 0 rolls back to
// the previous release.
func (c *Client) Rollback(ctx context.Context, orgID, appID string, revision int) (*App, error) {
	var app App
	var body any
	if revision != 0 {
		body = rollbackRequest{Revision: revision}
	}
	if err := c.request(ctx, http.MethodPost, appPath(orgID, appID)+"/rollback", body, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// ListBuilds returns an app's git-source image builds.
func (c *Client) ListBuilds(ctx context.Context, orgID, appID string) ([]Build, error) {
	var out dataEnvelope[Build]
	if err := c.request(ctx, http.MethodGet, appPath(orgID, appID)+"/builds", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// GetBuild returns one build (including captured logs on failure).
func (c *Client) GetBuild(ctx context.Context, orgID, appID, buildID string) (*Build, error) {
	var b Build
	if err := c.request(ctx, http.MethodGet, appPath(orgID, appID)+"/builds/"+url.PathEscape(buildID), nil, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// AppMetrics returns an app's live pod CPU/memory usage from metrics-server.
func (c *Client) AppMetrics(ctx context.Context, orgID, appID string) (*Metrics, error) {
	var m Metrics
	if err := c.request(ctx, http.MethodGet, appPath(orgID, appID)+"/metrics", nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// DeployApp triggers a (re)deploy of an app.
func (c *Client) DeployApp(ctx context.Context, orgID, appID string) (*App, error) {
	return c.appAction(ctx, orgID, appID, "deploy")
}

// RestartApp triggers a rollout restart of an app.
func (c *Client) RestartApp(ctx context.Context, orgID, appID string) (*App, error) {
	return c.appAction(ctx, orgID, appID, "restart")
}

// StopApp scales an app to zero.
func (c *Client) StopApp(ctx context.Context, orgID, appID string) (*App, error) {
	return c.appAction(ctx, orgID, appID, "stop")
}

func (c *Client) appAction(ctx context.Context, orgID, appID, action string) (*App, error) {
	var app App
	if err := c.request(ctx, http.MethodPost, appPath(orgID, appID)+"/"+action, nil, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// DestroyApp deletes an app.
func (c *Client) DestroyApp(ctx context.Context, orgID, appID string) error {
	return c.request(ctx, http.MethodDelete, appPath(orgID, appID), nil, nil)
}

// AppLogs returns recent logs for an app (one-shot snapshot).
func (c *Client) AppLogs(ctx context.Context, orgID, appID string) (string, error) {
	var out struct {
		Logs string `json:"logs"`
	}
	if err := c.request(ctx, http.MethodGet, appPath(orgID, appID)+"/logs", nil, &out); err != nil {
		return "", err
	}
	return out.Logs, nil
}

// FollowAppLogs consumes the live SSE log stream (GET /logs?follow=true), calling
// onLine for every log line until the stream ends or ctx is cancelled. The
// optional tail caps the initial backlog (the API tails 200 lines server-side);
// allPods=true streams every pod's logs.
func (c *Client) FollowAppLogs(ctx context.Context, orgID, appID string, allPods bool, onLine func(string)) error {
	q := url.Values{"follow": {"true"}}
	if allPods {
		q.Set("all", "true")
	}
	return c.stream(ctx, appPath(orgID, appID)+"/logs?"+q.Encode(), onLine)
}

func appPath(orgID, appID string) string {
	return "/v1/orgs/" + url.PathEscape(orgID) + "/apps/" + url.PathEscape(appID)
}

// --- secrets / env ---

// ListEnv returns an app's environment variables / secrets.
func (c *Client) ListEnv(ctx context.Context, orgID, appID string) ([]EnvVar, error) {
	var out dataEnvelope[EnvVar]
	if err := c.request(ctx, http.MethodGet, appPath(orgID, appID)+"/env", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// SetEnv sets a single plain environment variable on an app.
func (c *Client) SetEnv(ctx context.Context, orgID, appID, key, value string) (*EnvVar, error) {
	return c.SetEnvSecret(ctx, orgID, appID, key, value, false)
}

// SetEnvSecret sets a single environment variable on an app, marking it secret
// (encrypted at rest, masked on read) when secret is true.
func (c *Client) SetEnvSecret(ctx context.Context, orgID, appID, key, value string, secret bool) (*EnvVar, error) {
	var ev EnvVar
	err := c.request(ctx, http.MethodPut, appPath(orgID, appID)+"/env",
		setEnvRequest{Key: key, Value: value, Secret: secret}, &ev)
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

// UnsetEnv removes an environment variable from an app.
func (c *Client) UnsetEnv(ctx context.Context, orgID, appID, key string) error {
	return c.request(ctx, http.MethodDelete, appPath(orgID, appID)+"/env/"+url.PathEscape(key), nil, nil)
}

// --- domains ---

// ListDomains returns an app's custom domains.
func (c *Client) ListDomains(ctx context.Context, orgID, appID string) ([]Domain, error) {
	var out dataEnvelope[Domain]
	if err := c.request(ctx, http.MethodGet, appPath(orgID, appID)+"/domains", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// AddDomain attaches a custom domain to an app and returns the DNS instructions
// (TXT challenge + A/CNAME target) the user must publish.
func (c *Client) AddDomain(ctx context.Context, orgID, appID, fqdn string) (*DomainResult, error) {
	var res DomainResult
	err := c.request(ctx, http.MethodPost, appPath(orgID, appID)+"/domains",
		map[string]string{"domain": fqdn}, &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// VerifyDomain triggers DNS TXT verification for a domain and returns its updated
// status plus instructions.
func (c *Client) VerifyDomain(ctx context.Context, orgID, appID, domainID string) (*DomainResult, error) {
	var res DomainResult
	path := appPath(orgID, appID) + "/domains/" + url.PathEscape(domainID) + "/verify"
	if err := c.request(ctx, http.MethodPost, path, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// RemoveDomain detaches a custom domain from an app.
func (c *Client) RemoveDomain(ctx context.Context, orgID, appID, domainID string) error {
	return c.request(ctx, http.MethodDelete, appPath(orgID, appID)+"/domains/"+url.PathEscape(domainID), nil, nil)
}

// --- databases ---

func dbPath(orgID, dbID string) string {
	return "/v1/orgs/" + url.PathEscape(orgID) + "/databases/" + url.PathEscape(dbID)
}

// ListDatabases returns the managed databases in an org.
func (c *Client) ListDatabases(ctx context.Context, orgID string) ([]Database, error) {
	var out dataEnvelope[Database]
	if err := c.request(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(orgID)+"/databases", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// CreateDatabase provisions a managed database in an org (defaults to the org's
// default project when ProjectID is empty).
func (c *Client) CreateDatabase(ctx context.Context, orgID string, in CreateDatabaseInput) (*Database, error) {
	body := createDatabaseRequest(in)
	var db Database
	if err := c.request(ctx, http.MethodPost, "/v1/orgs/"+url.PathEscape(orgID)+"/databases", body, &db); err != nil {
		return nil, err
	}
	return &db, nil
}

// GetDatabase returns one database plus its in-cluster connection info
// (host/port/db/user/password + connectionString).
func (c *Client) GetDatabase(ctx context.Context, orgID, dbID string) (*DatabaseDetail, error) {
	var d DatabaseDetail
	if err := c.request(ctx, http.MethodGet, dbPath(orgID, dbID), nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// DeployDatabase provisions/redeploys a database.
func (c *Client) DeployDatabase(ctx context.Context, orgID, dbID string) (*Database, error) {
	return c.dbAction(ctx, orgID, dbID, "deploy")
}

// StopDatabase stops a database.
func (c *Client) StopDatabase(ctx context.Context, orgID, dbID string) (*Database, error) {
	return c.dbAction(ctx, orgID, dbID, "stop")
}

// RestartDatabase restarts a database.
func (c *Client) RestartDatabase(ctx context.Context, orgID, dbID string) (*Database, error) {
	return c.dbAction(ctx, orgID, dbID, "restart")
}

func (c *Client) dbAction(ctx context.Context, orgID, dbID, action string) (*Database, error) {
	var db Database
	if err := c.request(ctx, http.MethodPost, dbPath(orgID, dbID)+"/"+action, nil, &db); err != nil {
		return nil, err
	}
	return &db, nil
}

// DeleteDatabase deletes a database.
func (c *Client) DeleteDatabase(ctx context.Context, orgID, dbID string) error {
	return c.request(ctx, http.MethodDelete, dbPath(orgID, dbID), nil, nil)
}

// --- services ---

// ServiceCatalog returns the public one-click services catalog.
func (c *Client) ServiceCatalog(ctx context.Context) ([]ServiceTemplate, error) {
	var out dataEnvelope[ServiceTemplate]
	if err := c.requestNoAuth(ctx, http.MethodGet, "/v1/services/catalog", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// ListServices returns the provisioned services in an org.
func (c *Client) ListServices(ctx context.Context, orgID string) ([]Service, error) {
	var out dataEnvelope[Service]
	if err := c.request(ctx, http.MethodGet, "/v1/orgs/"+url.PathEscape(orgID)+"/services", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// CreateService provisions a one-click service in a project within an org.
func (c *Client) CreateService(ctx context.Context, orgID, projectID string, in CreateServiceInput) (*Service, error) {
	body := createServiceRequest(in)
	path := "/v1/orgs/" + url.PathEscape(orgID) + "/projects/" + url.PathEscape(projectID) + "/services"
	var svc Service
	if err := c.request(ctx, http.MethodPost, path, body, &svc); err != nil {
		return nil, err
	}
	return &svc, nil
}

func svcPath(orgID, svcID string) string {
	return "/v1/orgs/" + url.PathEscape(orgID) + "/services/" + url.PathEscape(svcID)
}

// DeployService provisions/redeploys a one-click service.
func (c *Client) DeployService(ctx context.Context, orgID, svcID string) (*Service, error) {
	return c.svcAction(ctx, orgID, svcID, "deploy")
}

// StopService stops a service.
func (c *Client) StopService(ctx context.Context, orgID, svcID string) (*Service, error) {
	return c.svcAction(ctx, orgID, svcID, "stop")
}

// RestartService restarts a service.
func (c *Client) RestartService(ctx context.Context, orgID, svcID string) (*Service, error) {
	return c.svcAction(ctx, orgID, svcID, "restart")
}

func (c *Client) svcAction(ctx context.Context, orgID, svcID, action string) (*Service, error) {
	var svc Service
	if err := c.request(ctx, http.MethodPost, svcPath(orgID, svcID)+"/"+action, nil, &svc); err != nil {
		return nil, err
	}
	return &svc, nil
}

// DestroyService deletes a service.
func (c *Client) DestroyService(ctx context.Context, orgID, svcID string) error {
	return c.request(ctx, http.MethodDelete, svcPath(orgID, svcID), nil, nil)
}

// --- billing ---

// Plans returns the public plan catalog.
func (c *Client) Plans(ctx context.Context) ([]Plan, error) {
	var out dataEnvelope[Plan]
	if err := c.requestNoAuth(ctx, http.MethodGet, "/v1/billing/plans", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Pricing returns the public hourly price list (active components).
func (c *Client) Pricing(ctx context.Context) ([]PricingComponent, error) {
	var out dataEnvelope[PricingComponent]
	if err := c.requestNoAuth(ctx, http.MethodGet, "/v1/billing/pricing", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}
