// Package coolify is a typed Go client for the Coolify (v4) REST API.
//
// Coolify exposes its API under /api/v1 and authenticates with a Laravel
// Sanctum bearer token carrying read / write / deploy abilities. This client
// is the orchestration backend for Viro: the control-plane translates Viro's
// product concepts (apps, deploys, databases) into Coolify API calls.
package coolify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	apiPrefix = "/api/v1"
	// maxResponseBytes caps how much of an upstream response we buffer (8 MiB).
	maxResponseBytes = 8 << 20
)

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(truncated)"
}

// Client is a Coolify API client. It is safe for concurrent use.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the underlying *http.Client (useful in tests).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// NewClient returns a Coolify client for the given instance base URL and token.
// baseURL is the Coolify root (e.g. "https://coolify.example.com"); the /api/v1
// prefix is added automatically.
func NewClient(baseURL, token string, opts ...Option) *Client {
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Configured reports whether the client has a token and can talk to a real
// Coolify backend. When false, the control-plane runs in local/demo mode and
// skips outbound Coolify calls.
func (c *Client) Configured() bool { return c.token != "" }

// APIError represents a non-2xx response from the Coolify API.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("coolify api: status %d: %s", e.StatusCode, e.Body)
}

// do performs an API request against {baseURL}/api/v1{path}, encoding body as
// JSON when non-nil and decoding the response into out when non-nil.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+apiPrefix+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Bound the response we buffer so a misbehaving upstream cannot OOM the process.
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{StatusCode: resp.StatusCode, Body: truncate(strings.TrimSpace(string(data)), 512)}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
