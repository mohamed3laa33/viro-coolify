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

// --- personal access tokens (PAT) ---

// ApiToken is a personal access token (PAT). The plaintext Token is populated
// ONLY by CreateToken (shown once); listings never carry it.
type ApiToken struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token,omitempty"` // "vrt_..."; create-only, shown once
	// Plaintext (alias accepted by some servers); CreateToken normalizes onto Token.
	PlainToken string    `json:"plainToken,omitempty"`
	Prefix     string    `json:"prefix"`
	Scopes     []string  `json:"scopes,omitempty"`
	ExpiresAt  time.Time `json:"expiresAt,omitempty"`
	LastUsedAt time.Time `json:"lastUsedAt,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

type createTokenRequest struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes,omitempty"`
	ExpiresInDays int      `json:"expiresInDays,omitempty"`
}

// CreateTokenInput describes a new personal access token.
type CreateTokenInput struct {
	Name          string
	Scopes        []string
	ExpiresInDays int
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
	CoolifyUUID   string    `json:"coolifyUuid,omitempty"`
	Name          string    `json:"name"`
	Image         string    `json:"image,omitempty"`
	GitRepository string    `json:"gitRepository,omitempty"`
	GitBranch     string    `json:"gitBranch,omitempty"`
	BuildPack     string    `json:"buildPack,omitempty"`
	CPU           float64   `json:"cpu"`
	MemoryMB      int       `json:"memoryMb"`
	Status        string    `json:"status"`
	MinReplicas   int       `json:"minReplicas,omitempty"`
	MaxReplicas   int       `json:"maxReplicas,omitempty"`
	Namespace     string    `json:"namespace,omitempty"`
	Release       string    `json:"release,omitempty"`
	Host          string    `json:"host,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

// AppDetail is the GET /apps/{id} body: the app fields flattened to the top level
// plus the currently-active release.
type AppDetail struct {
	App
	CurrentRelease *Release `json:"currentRelease,omitempty"`
}

type createAppRequest struct {
	Name          string  `json:"name"`
	ProjectID     string  `json:"projectId,omitempty"`
	Image         string  `json:"image,omitempty"`
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
	Image         string
	GitRepository string
	GitBranch     string
	BuildPack     string
	CPU           float64
	MemoryMB      int
}

// updateAppRequest carries the editable app fields. Pointers distinguish an
// omitted field (leave unchanged) from one explicitly set.
type updateAppRequest struct {
	Image         *string  `json:"image,omitempty"`
	CPU           *float64 `json:"cpu,omitempty"`
	MemoryMB      *int     `json:"memoryMb,omitempty"`
	GitRepository *string  `json:"gitRepository,omitempty"`
	GitBranch     *string  `json:"gitBranch,omitempty"`
}

// UpdateAppInput describes a PATCH to an app. A nil field is left unchanged.
type UpdateAppInput struct {
	Image         *string
	CPU           *float64
	MemoryMB      *int
	GitRepository *string
	GitBranch     *string
}

type scaleAppRequest struct {
	MinReplicas *int `json:"minReplicas,omitempty"`
	MaxReplicas *int `json:"maxReplicas,omitempty"`
}

// ScaleAppInput sets autoscaling bounds. A nil field is left unchanged.
type ScaleAppInput struct {
	MinReplicas *int
	MaxReplicas *int
}

type rollbackRequest struct {
	Revision int `json:"revision,omitempty"`
}

// Release is one immutable deploy revision of an app.
type Release struct {
	ID         string    `json:"id"`
	AppID      string    `json:"appId"`
	OrgID      string    `json:"orgId"`
	Revision   int       `json:"revision"`
	Image      string    `json:"image"`
	GitRef     string    `json:"gitRef,omitempty"`
	ConfigHash string    `json:"configHash,omitempty"`
	CPU        float64   `json:"cpu"`
	MemoryMB   int       `json:"memoryMb"`
	Status     string    `json:"status"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Build records one git-source image build for an app.
type Build struct {
	ID         string    `json:"id"`
	AppID      string    `json:"appId"`
	OrgID      string    `json:"orgId"`
	Status     string    `json:"status"`
	CommitRef  string    `json:"commitRef,omitempty"`
	Image      string    `json:"image,omitempty"`
	Logs       string    `json:"logs,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
}

// PodMetric is a single pod's live CPU/memory usage.
type PodMetric struct {
	Name          string `json:"name"`
	CPUMillicores int64  `json:"cpuMillicores"`
	MemoryBytes   int64  `json:"memoryBytes"`
}

// Metrics is a live snapshot of an app's pod resource usage.
type Metrics struct {
	Available     bool        `json:"available"`
	Unavailable   string      `json:"unavailable,omitempty"`
	Pods          []PodMetric `json:"pods"`
	CPUMillicores int64       `json:"cpuMillicores"`
	MemoryBytes   int64       `json:"memoryBytes"`
}

// --- domains ---

// DomainInstructions are the DNS records to publish for verification + routing.
type DomainInstructions struct {
	VerificationToken string `json:"verificationToken"`
	TXTName           string `json:"txtName"`
	TXTValue          string `json:"txtValue"`
	TargetType        string `json:"targetType"`
	TargetValue       string `json:"targetValue"`
}

// Domain is a custom domain attached to an app.
type Domain struct {
	ID                string    `json:"id"`
	OrgID             string    `json:"orgId"`
	AppID             string    `json:"appId"`
	Domain            string    `json:"domain"`
	Verified          bool      `json:"verified"`
	Status            string    `json:"status"`
	VerificationToken string    `json:"verificationToken,omitempty"`
	VerifiedAt        time.Time `json:"verifiedAt,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
}

// DomainResult bundles a domain record with its DNS instructions (add/verify).
type DomainResult struct {
	Domain
	Instructions DomainInstructions `json:"instructions"`
}

// --- databases ---

// Database is a Vortex managed database.
type Database struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	ProjectID string    `json:"projectId,omitempty"`
	Name      string    `json:"name"`
	Engine    string    `json:"engine"`
	CPU       float64   `json:"cpu"`
	MemoryMB  int       `json:"memoryMb"`
	StorageGB int       `json:"storageGb"`
	Status    string    `json:"status"`
	Namespace string    `json:"namespace,omitempty"`
	Release   string    `json:"release,omitempty"`
	Host      string    `json:"host,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// DatabaseConnInfo is the in-cluster connection detail for a managed database.
type DatabaseConnInfo struct {
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Database         string `json:"database"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	ConnectionString string `json:"connectionString"`
}

// DatabaseDetail bundles a database record with its connection info.
type DatabaseDetail struct {
	Database
	Connection DatabaseConnInfo `json:"connection"`
}

type createDatabaseRequest struct {
	Name      string  `json:"name"`
	Engine    string  `json:"engine,omitempty"`
	ProjectID string  `json:"projectId,omitempty"`
	CPU       float64 `json:"cpu,omitempty"`
	MemoryMB  int     `json:"memoryMb,omitempty"`
	StorageGB int     `json:"storageGb,omitempty"`
}

// CreateDatabaseInput describes a new managed database.
type CreateDatabaseInput struct {
	Name      string
	Engine    string
	ProjectID string
	CPU       float64
	MemoryMB  int
	StorageGB int
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

// EnvVar is an app environment variable / secret. Secret values are masked by
// the API.
type EnvVar struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

type setEnvRequest struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret,omitempty"`
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

// PricingComponent is an admin-managed billable resource priced per hour.
type PricingComponent struct {
	Key          string  `json:"key"`
	Name         string  `json:"name"`
	Unit         string  `json:"unit"`
	PricePerHour float64 `json:"pricePerHour"`
	Currency     string  `json:"currency"`
	Active       bool    `json:"active"`
	SortOrder    int     `json:"sortOrder"`
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
