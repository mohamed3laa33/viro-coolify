package coolify

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListApplications(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/applications" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("auth header = %q, want Bearer test-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"uuid":"abc","name":"web","status":"running:healthy"}]`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	apps, err := c.ListApplications(context.Background())
	if err != nil {
		t.Fatalf("ListApplications: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	if apps[0].UUID != "abc" || apps[0].Name != "web" {
		t.Fatalf("unexpected app: %+v", apps[0])
	}
}

func TestStartApplication(t *testing.T) {
	var hitPath, hitMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath, hitMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	if err := c.StartApplication(context.Background(), "app-uuid-1"); err != nil {
		t.Fatalf("StartApplication: %v", err)
	}
	if hitPath != "/api/v1/applications/app-uuid-1/start" {
		t.Fatalf("path = %s", hitPath)
	}
	if hitMethod != http.MethodPost {
		t.Fatalf("method = %s", hitMethod)
	}
}

func TestCreatePublicApplication(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/applications/public" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uuid":"new-app-uuid"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	uuid, err := c.CreatePublicApplication(context.Background(), CreatePublicApplicationRequest{
		ProjectUUID:   "p1",
		ServerUUID:    "s1",
		GitRepository: "https://github.com/acme/web",
		GitBranch:     "main",
	})
	if err != nil {
		t.Fatalf("CreatePublicApplication: %v", err)
	}
	if uuid != "new-app-uuid" {
		t.Fatalf("uuid = %q", uuid)
	}
}

func TestAPIErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthenticated."}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "bad")
	_, err := c.ListApplications(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", apiErr.StatusCode)
	}
}
