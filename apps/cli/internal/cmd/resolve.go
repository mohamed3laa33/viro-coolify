package cmd

import (
	"fmt"
	"strings"

	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/client"
	"github.com/spf13/cobra"
)

// Name-based addressing
//
// Users should not have to copy UUIDs around. Every command that takes an
// org/project/app accepts a *name, slug, or id* and resolves it to the canonical
// id via the API. Resolution prefers an exact id match (so existing scripts that
// pass ids keep working with zero extra requests-of-ambiguity), then a slug
// match, then a case-insensitive name match. An unmatched or ambiguous name is a
// clear error rather than a silent guess (no fake success — invariant #6).

// resolveOrgID resolves the effective org reference (--org flag or persisted
// context) to an org id. A value that already equals an org id is returned
// as-is; otherwise it is matched by slug/name against the caller's orgs.
func (a *App) resolveOrgID(cmd *cobra.Command) (string, error) {
	ref, err := a.orgID()
	if err != nil {
		return "", err
	}
	orgs, err := a.client.ListOrgs(ctx(cmd))
	if err != nil {
		// If listing fails (e.g. offline) fall back to treating the ref as an id
		// so callers still get a meaningful downstream error, not a resolve error.
		return ref, nil //nolint:nilerr // intentional: defer to the real call's error
	}
	id, err := matchOrg(orgs, ref)
	if err != nil {
		return "", err
	}
	return id, nil
}

// resolveOrgApp is the common path for app-targeting commands: it resolves the
// effective org (flag/context, by name or id) and the positional app reference
// (name or id) to their canonical ids in one step.
func (a *App) resolveOrgApp(cmd *cobra.Command, appRef string) (orgID, appID string, err error) {
	orgID, err = a.resolveOrgID(cmd)
	if err != nil {
		return "", "", err
	}
	appID, err = a.resolveAppID(cmd, orgID, appRef)
	if err != nil {
		return "", "", err
	}
	return orgID, appID, nil
}

// resolveProjectID resolves the effective project reference (--project flag or
// context) to a project id within orgID.
func (a *App) resolveProjectID(cmd *cobra.Command, orgID string) (string, error) {
	ref, err := a.projectID()
	if err != nil {
		return "", err
	}
	return a.resolveProjectRef(cmd, orgID, ref)
}

// resolveProjectRef resolves an explicit project name/slug/id within orgID.
func (a *App) resolveProjectRef(cmd *cobra.Command, orgID, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("no project set: pass --project or run `vortex config set-context --project <name>`")
	}
	projects, err := a.client.ListProjects(ctx(cmd), orgID)
	if err != nil {
		return ref, nil //nolint:nilerr // defer to the downstream call's error
	}
	return matchProject(projects, ref)
}

// resolveAppID resolves an app name/slug/id to an app id within orgID. It only
// lists when ref is not already an exact id, keeping the common (id) path to a
// single request.
func (a *App) resolveAppID(cmd *cobra.Command, orgID, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("app name or id is required")
	}
	apps, err := a.client.ListApps(ctx(cmd), orgID)
	if err != nil {
		return ref, nil //nolint:nilerr // defer to the downstream call's error
	}
	return matchApp(apps, ref)
}

// --- pure matchers (unit-testable, no I/O) ---

func matchOrg(orgs []client.Org, ref string) (string, error) {
	for _, o := range orgs { // exact id wins
		if o.ID == ref {
			return o.ID, nil
		}
	}
	var hits []string
	for _, o := range orgs {
		if o.Slug == ref || strings.EqualFold(o.Name, ref) {
			hits = append(hits, o.ID)
		}
	}
	return pick("organization", ref, hits)
}

func matchProject(projects []client.Project, ref string) (string, error) {
	for _, p := range projects {
		if p.ID == ref {
			return p.ID, nil
		}
	}
	var hits []string
	for _, p := range projects {
		if p.Slug == ref || strings.EqualFold(p.Name, ref) {
			hits = append(hits, p.ID)
		}
	}
	return pick("project", ref, hits)
}

func matchApp(apps []client.App, ref string) (string, error) {
	for _, app := range apps {
		if app.ID == ref {
			return app.ID, nil
		}
	}
	var hits []string
	for _, app := range apps {
		if strings.EqualFold(app.Name, ref) {
			hits = append(hits, app.ID)
		}
	}
	return pick("app", ref, hits)
}

// pick returns the single matched id, or a clear error when there are zero or
// multiple matches.
func pick(kind, ref string, hits []string) (string, error) {
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", fmt.Errorf("no %s named %q found (use `vortex %ss list` to see available ids)", kind, ref, kind)
	default:
		return "", fmt.Errorf("%q is ambiguous: %d %ss match — pass the id instead", ref, len(hits), kind)
	}
}
