package cmd

import (
	"testing"

	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/client"
)

func TestMatchOrg(t *testing.T) {
	orgs := []client.Org{
		{ID: "org_1", Name: "Acme", Slug: "acme"},
		{ID: "org_2", Name: "Beta Co", Slug: "beta"},
	}
	cases := []struct {
		name    string
		ref     string
		want    string
		wantErr bool
	}{
		{"by id", "org_2", "org_2", false},
		{"by slug", "acme", "org_1", false},
		{"by name case-insensitive", "beta co", "org_2", false},
		{"no match", "nope", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := matchOrg(orgs, tc.ref)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.ref)
				}
				return
			}
			if err != nil {
				t.Fatalf("matchOrg(%q): %v", tc.ref, err)
			}
			if got != tc.want {
				t.Fatalf("matchOrg(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

func TestMatchOrgIDWinsOverNameCollision(t *testing.T) {
	// A ref that equals one org's id but another org's name must resolve to the id.
	orgs := []client.Org{
		{ID: "shared", Name: "X"},
		{ID: "org_2", Name: "shared"},
	}
	got, err := matchOrg(orgs, "shared")
	if err != nil {
		t.Fatalf("matchOrg: %v", err)
	}
	if got != "shared" {
		t.Fatalf("expected exact id match to win, got %q", got)
	}
}

func TestMatchAppAmbiguous(t *testing.T) {
	apps := []client.App{
		{ID: "a1", Name: "web"},
		{ID: "a2", Name: "web"},
	}
	if _, err := matchApp(apps, "web"); err == nil {
		t.Fatal("expected ambiguity error for duplicate names")
	}
	// An exact id still resolves even when names collide.
	got, err := matchApp(apps, "a2")
	if err != nil {
		t.Fatalf("matchApp by id: %v", err)
	}
	if got != "a2" {
		t.Fatalf("matchApp by id = %q, want a2", got)
	}
}

func TestMatchProject(t *testing.T) {
	projects := []client.Project{
		{ID: "p1", Name: "Default", Slug: "default", IsDefault: true},
		{ID: "p2", Name: "Staging", Slug: "staging"},
	}
	got, err := matchProject(projects, "staging")
	if err != nil {
		t.Fatalf("matchProject: %v", err)
	}
	if got != "p2" {
		t.Fatalf("matchProject(staging) = %q, want p2", got)
	}
	if _, err := matchProject(projects, "prod"); err == nil {
		t.Fatal("expected error for unknown project")
	}
}
