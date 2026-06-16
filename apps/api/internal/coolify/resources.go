package coolify

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Application is a Coolify application resource (a subset of fields relevant to Viro).
type Application struct {
	UUID          string `json:"uuid"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	FQDN          string `json:"fqdn,omitempty"`
	Status        string `json:"status,omitempty"`
	GitRepository string `json:"git_repository,omitempty"`
	GitBranch     string `json:"git_branch,omitempty"`
	BuildPack     string `json:"build_pack,omitempty"`
	ProjectUUID   string `json:"project_uuid,omitempty"`
}

// Database is a Coolify standalone database resource.
type Database struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Status      string `json:"status,omitempty"`
	Description string `json:"description,omitempty"`
}

// EnvVar is an environment variable / secret attached to a resource.
type EnvVar struct {
	UUID      string `json:"uuid,omitempty"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	IsPreview bool   `json:"is_preview,omitempty"`
}

// CreatePublicApplicationRequest creates an application from a public git repository.
type CreatePublicApplicationRequest struct {
	ProjectUUID     string `json:"project_uuid"`
	ServerUUID      string `json:"server_uuid"`
	EnvironmentName string `json:"environment_name,omitempty"`
	GitRepository   string `json:"git_repository"`
	GitBranch       string `json:"git_branch"`
	BuildPack       string `json:"build_pack,omitempty"`
	Ports           string `json:"ports_exposes,omitempty"`
	Name            string `json:"name,omitempty"`
	InstantDeploy   bool   `json:"instant_deploy,omitempty"`
}

// createResponse is the common {uuid} envelope Coolify returns on create.
type createResponse struct {
	UUID string `json:"uuid"`
}

// ListApplications returns all applications visible to the token's team.
func (c *Client) ListApplications(ctx context.Context) ([]Application, error) {
	var apps []Application
	if err := c.do(ctx, http.MethodGet, "/applications", nil, &apps); err != nil {
		return nil, err
	}
	return apps, nil
}

// GetApplication returns a single application by UUID.
func (c *Client) GetApplication(ctx context.Context, uuid string) (*Application, error) {
	var app Application
	if err := c.do(ctx, http.MethodGet, "/applications/"+url.PathEscape(uuid), nil, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

// CreatePublicApplication creates an application from a public git repository and
// returns the new application UUID.
func (c *Client) CreatePublicApplication(ctx context.Context, req CreatePublicApplicationRequest) (string, error) {
	var out createResponse
	if err := c.do(ctx, http.MethodPost, "/applications/public", req, &out); err != nil {
		return "", err
	}
	return out.UUID, nil
}

// StartApplication triggers a (re)deploy of the application.
func (c *Client) StartApplication(ctx context.Context, uuid string) error {
	return c.do(ctx, http.MethodPost, "/applications/"+url.PathEscape(uuid)+"/start", nil, nil)
}

// StopApplication stops the application's running containers.
func (c *Client) StopApplication(ctx context.Context, uuid string) error {
	return c.do(ctx, http.MethodPost, "/applications/"+url.PathEscape(uuid)+"/stop", nil, nil)
}

// RestartApplication restarts the application.
func (c *Client) RestartApplication(ctx context.Context, uuid string) error {
	return c.do(ctx, http.MethodPost, "/applications/"+url.PathEscape(uuid)+"/restart", nil, nil)
}

// DeleteApplication removes the application.
func (c *Client) DeleteApplication(ctx context.Context, uuid string) error {
	return c.do(ctx, http.MethodDelete, "/applications/"+url.PathEscape(uuid), nil, nil)
}

// ListApplicationEnvs returns the environment variables for an application.
func (c *Client) ListApplicationEnvs(ctx context.Context, uuid string) ([]EnvVar, error) {
	var envs []EnvVar
	if err := c.do(ctx, http.MethodGet, "/applications/"+url.PathEscape(uuid)+"/envs", nil, &envs); err != nil {
		return nil, err
	}
	return envs, nil
}

// CreateApplicationEnv sets a single environment variable on an application.
func (c *Client) CreateApplicationEnv(ctx context.Context, uuid string, env EnvVar) error {
	return c.do(ctx, http.MethodPost, "/applications/"+url.PathEscape(uuid)+"/envs", env, nil)
}

// Deploy triggers a deployment by application UUID (or tag) via the deploy endpoint.
func (c *Client) Deploy(ctx context.Context, appUUID string) error {
	return c.do(ctx, http.MethodPost, "/deploy?uuid="+url.QueryEscape(appUUID), nil, nil)
}

// ListDatabases returns all standalone databases visible to the token's team.
func (c *Client) ListDatabases(ctx context.Context) ([]Database, error) {
	var dbs []Database
	if err := c.do(ctx, http.MethodGet, "/databases", nil, &dbs); err != nil {
		return nil, err
	}
	return dbs, nil
}

// Version returns the Coolify instance version string.
func (c *Client) Version(ctx context.Context) (string, error) {
	var v string
	if err := c.do(ctx, http.MethodGet, "/version", nil, &v); err != nil {
		return "", fmt.Errorf("coolify version: %w", err)
	}
	return v, nil
}
