package client

import "time"

// --- auth ---

type signupRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

// User mirrors the API's userView.
type User struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"isAdmin"`
}

type authResponse struct {
	User         User   `json:"user"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

// AuthResult is returned by Signup/Login.
type AuthResult struct {
	User         User
	AccessToken  string
	RefreshToken string
}

// --- orgs / projects ---

// Org is a Vortex organization.
type Org struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"createdAt"`
}

// Project groups apps within an org.
type Project struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	IsDefault bool      `json:"isDefault"`
	CreatedAt time.Time `json:"createdAt"`
}

// --- apps ---

// App is a Vortex application.
type App struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"orgId"`
	ProjectID     string    `json:"projectId"`
	Name          string    `json:"name"`
	GitRepository string    `json:"gitRepository,omitempty"`
	GitBranch     string    `json:"gitBranch,omitempty"`
	BuildPack     string    `json:"buildPack,omitempty"`
	CPU           float64   `json:"cpu"`
	MemoryMB      int       `json:"memoryMb"`
	Status        string    `json:"status"`
	Namespace     string    `json:"namespace,omitempty"`
	Release       string    `json:"release,omitempty"`
	Host          string    `json:"host,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type createAppRequest struct {
	Name          string  `json:"name"`
	ProjectID     string  `json:"projectId,omitempty"`
	GitRepository string  `json:"gitRepository,omitempty"`
	GitBranch     string  `json:"gitBranch,omitempty"`
	BuildPack     string  `json:"buildPack,omitempty"`
	CPU           float64 `json:"cpu,omitempty"`
	MemoryMB      int     `json:"memoryMb,omitempty"`
}

// CreateAppInput describes a new app for CreateApp.
type CreateAppInput struct {
	Name          string
	ProjectID     string
	GitRepository string
	GitBranch     string
	BuildPack     string
	CPU           float64
	MemoryMB      int
}

// --- services ---

// ServiceTemplate is a one-click catalog entry.
type ServiceTemplate struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Kind        string `json:"kind"`
	Image       string `json:"image,omitempty"`
	DefaultPort int    `json:"defaultPort,omitempty"`
	Active      bool   `json:"active"`
	SortOrder   int    `json:"sortOrder"`
}

// Service is a provisioned one-click catalog instance.
type Service struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	ProjectID string    `json:"projectId"`
	Template  string    `json:"template"`
	Name      string    `json:"name"`
	CPU       float64   `json:"cpu"`
	MemoryMB  int       `json:"memoryMb"`
	Status    string    `json:"status"`
	Namespace string    `json:"namespace,omitempty"`
	Release   string    `json:"release,omitempty"`
	Host      string    `json:"host,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type createServiceRequest struct {
	TemplateKey string  `json:"templateKey"`
	Name        string  `json:"name,omitempty"`
	CPU         float64 `json:"cpu,omitempty"`
	MemoryMB    int     `json:"memoryMb,omitempty"`
}

// CreateServiceInput describes a new one-click service.
type CreateServiceInput struct {
	TemplateKey string
	Name        string
	CPU         float64
	MemoryMB    int
}

// --- secrets / env ---

// EnvVar is an app environment variable / secret.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type setEnvRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// --- billing ---

// Plan is a billing plan in the catalog.
type Plan struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Description         string  `json:"description"`
	PriceCents          int     `json:"priceCents"`
	Currency            string  `json:"currency"`
	IncludedHours       int     `json:"includedHours"`
	OveragePerHourCents int     `json:"overagePerHourCents"`
	MaxCPU              float64 `json:"maxCpu"`
	MaxMemoryMB         int     `json:"maxMemoryMb"`
	MaxApps             int     `json:"maxApps"`
	IsDefault           bool    `json:"isDefault"`
	SortOrder           int     `json:"sortOrder"`
	Active              bool    `json:"active"`
}

// --- version ---

// Version is the API server's version info.
type Version struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Env     string `json:"env"`
}

// dataEnvelope wraps the API's {"data": [...]} list responses.
type dataEnvelope[T any] struct {
	Data []T `json:"data"`
}
