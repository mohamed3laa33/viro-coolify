// Package client is a typed HTTP client for the Vortex control-plane API
// (apps/api). It speaks the exact JSON contract defined by the server's route
// table and handlers, attaches the JWT bearer token, and transparently refreshes
// an expired access token via /v1/auth/refresh on a 401.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// PATPrefix is the personal-access-token prefix. A bearer value beginning with
// it is a PAT (authenticates as the token owner) rather than a short-lived JWT.
const PATPrefix = "vrt_"

// TokenStore abstracts persistence of the access/refresh tokens so the client
// can update them after a refresh without depending on the config package.
//
// A store MAY also expose a personal access token (PAT) via the optional
// PATStore interface; when present and non-empty the client sends it verbatim as
// the bearer and never attempts a refresh.
type TokenStore interface {
	Access() string
	Refresh() string
	Save(access, refresh string) error
}

// PATStore is optionally implemented by a TokenStore that holds a personal
// access token. When PAT() returns a non-empty "vrt_..." value it is used as the
// Authorization bearer in preference to the JWT access token.
type PATStore interface {
	PAT() string
}

// bearer returns the credential the client should send: the PAT when the store
// has one, else the JWT access token.
func (c *Client) bearer() string {
	if ps, ok := c.tokens.(PATStore); ok {
		if pat := ps.PAT(); pat != "" {
			return pat
		}
	}
	return c.tokens.Access()
}

// hasPAT reports whether the store is authenticating with a PAT (which must not
// be refreshed on a 401).
func (c *Client) hasPAT() bool {
	if ps, ok := c.tokens.(PATStore); ok {
		return ps.PAT() != ""
	}
	return false
}

// Client talks to the Vortex API.
type Client struct {
	baseURL string
	http    *http.Client
	tokens  TokenStore
}

// Option customizes a Client.
type Option func(*Client)

// WithHTTPClient overrides the underlying *http.Client (used in tests).
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// New builds a client for baseURL. tokens may be nil for unauthenticated use
// (e.g. login/signup or the public catalog endpoints).
func New(baseURL string, tokens TokenStore, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
		tokens:  tokens,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// APIError is a structured error returned by the API (non-2xx response).
type APIError struct {
	Status  int
	Message string
	Code    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("api error: HTTP %d", e.Status)
	}
	return e.Message
}

// IsUnauthorized reports whether err is an APIError with a 401 status.
func IsUnauthorized(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusUnauthorized
}

// request performs an HTTP request against path (e.g. "/v1/me"), encoding body
// as JSON when non-nil and decoding the response into out when non-nil. It
// attaches the bearer token and, on a 401 for an authenticated call, attempts a
// single token refresh before retrying.
func (c *Client) request(ctx context.Context, method, path string, body, out any) error {
	err := c.do(ctx, method, path, body, out, true)
	return err
}

// requestNoAuth performs a request without attaching a token or refreshing
// (used for the public auth + catalog endpoints).
func (c *Client) requestNoAuth(ctx context.Context, method, path string, body, out any) error {
	return c.do(ctx, method, path, body, out, false)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any, auth bool) error {
	resp, err := c.send(ctx, method, path, body, auth)
	if err != nil {
		return err
	}

	// On 401 for an authenticated request, try a one-shot refresh then retry.
	// A PAT-authenticated client never refreshes (PATs are long-lived).
	if auth && resp.StatusCode == http.StatusUnauthorized && c.tokens != nil && !c.hasPAT() && c.tokens.Refresh() != "" {
		_ = resp.Body.Close()
		if refreshErr := c.refresh(ctx); refreshErr != nil {
			return refreshErr
		}
		resp, err = c.send(ctx, method, path, body, auth)
		if err != nil {
			return err
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// stream issues a GET to path expecting a Server-Sent Events response and calls
// onLine for every `data:` payload until the stream ends or ctx is cancelled. It
// bypasses the JSON decode path in do(). A non-2xx response is decoded as an
// APIError. PAT and JWT bearer auth are attached the same way as request().
func (c *Client) stream(ctx context.Context, path string, onLine func(string)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.tokens != nil {
		if b := c.bearer(); b != "" {
			req.Header.Set("Authorization", "Bearer "+b)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeError(resp)
	}
	sc := bufio.NewScanner(resp.Body)
	// Allow long log lines (default bufio scan token cap is 64KB).
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data:") {
			onLine(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("read log stream: %w", err)
	}
	return nil
}

func (c *Client) send(ctx context.Context, method, path string, body any, auth bool) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if auth && c.tokens != nil {
		if b := c.bearer(); b != "" {
			req.Header.Set("Authorization", "Bearer "+b)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	return resp, nil
}

// refresh exchanges the stored refresh token for a fresh token pair and
// persists it via the TokenStore.
func (c *Client) refresh(ctx context.Context) error {
	var out authResponse
	err := c.requestNoAuth(ctx, http.MethodPost, "/v1/auth/refresh",
		refreshRequest{RefreshToken: c.tokens.Refresh()}, &out)
	if err != nil {
		return fmt.Errorf("session expired, please run `vortex auth login`: %w", err)
	}
	return c.tokens.Save(out.AccessToken, out.RefreshToken)
}

func decodeError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	ae := &APIError{Status: resp.StatusCode}
	var er struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(body, &er) == nil && er.Error != "" {
		ae.Message = er.Error
		ae.Code = er.Code
	} else {
		ae.Message = strings.TrimSpace(string(body))
		if ae.Message == "" {
			ae.Message = http.StatusText(resp.StatusCode)
		}
	}
	return ae
}
