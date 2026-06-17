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
	body := createAppRequest{
		Name:          in.Name,
		ProjectID:     in.ProjectID,
		GitRepository: in.GitRepository,
		GitBranch:     in.GitBranch,
		BuildPack:     in.BuildPack,
		CPU:           in.CPU,
		MemoryMB:      in.MemoryMB,
	}
	var app App
	if err := c.request(ctx, http.MethodPost, "/v1/orgs/"+url.PathEscape(orgID)+"/apps", body, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// GetApp returns one app.
func (c *Client) GetApp(ctx context.Context, orgID, appID string) (*App, error) {
	var app App
	if err := c.request(ctx, http.MethodGet, appPath(orgID, appID), nil, &app); err != nil {
		return nil, err
	}
	return &app, nil
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

// AppLogs returns recent logs for an app.
func (c *Client) AppLogs(ctx context.Context, orgID, appID string) (string, error) {
	var out struct {
		Logs string `json:"logs"`
	}
	if err := c.request(ctx, http.MethodGet, appPath(orgID, appID)+"/logs", nil, &out); err != nil {
		return "", err
	}
	return out.Logs, nil
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

// SetEnv sets a single environment variable on an app.
func (c *Client) SetEnv(ctx context.Context, orgID, appID, key, value string) (*EnvVar, error) {
	var ev EnvVar
	err := c.request(ctx, http.MethodPut, appPath(orgID, appID)+"/env",
		setEnvRequest{Key: key, Value: value}, &ev)
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

// UnsetEnv removes an environment variable from an app.
func (c *Client) UnsetEnv(ctx context.Context, orgID, appID, key string) error {
	return c.request(ctx, http.MethodDelete, appPath(orgID, appID)+"/env/"+url.PathEscape(key), nil, nil)
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
	body := createServiceRequest{
		TemplateKey: in.TemplateKey,
		Name:        in.Name,
		CPU:         in.CPU,
		MemoryMB:    in.MemoryMB,
	}
	path := "/v1/orgs/" + url.PathEscape(orgID) + "/projects/" + url.PathEscape(projectID) + "/services"
	var svc Service
	if err := c.request(ctx, http.MethodPost, path, body, &svc); err != nil {
		return nil, err
	}
	return &svc, nil
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
