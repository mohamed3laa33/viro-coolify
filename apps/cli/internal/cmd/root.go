// Package cmd assembles the vortex CLI command tree. Each command group lives in
// its own file (auth, orgs, projects, apps, services, secrets, plans, config,
// version). The root command owns shared state: the loaded config, the API
// client, and the global --org/--project/--json/--api-url flags.
package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/client"
	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/config"
	"github.com/spf13/cobra"
)

// App holds the shared dependencies threaded through the command tree.
type App struct {
	cfg    *config.Config
	client *client.Client

	// global flag values
	orgFlag     string
	projectFlag string
	apiURLFlag  string
	jsonOut     bool
}

// tokenStore adapts *config.Config to client.TokenStore so the client can read
// and persist tokens (including refresh-on-401).
type tokenStore struct{ cfg *config.Config }

func (t tokenStore) Access() string  { return t.cfg.AccessToken }
func (t tokenStore) Refresh() string { return t.cfg.RefreshToken }

// PAT implements client.PATStore: a stored personal access token authenticates
// as its owner and is sent verbatim as the bearer (never refreshed).
func (t tokenStore) PAT() string { return t.cfg.Token }
func (t tokenStore) Save(access, refresh string) error {
	return t.cfg.SetTokens(access, refresh)
}

// Execute builds the root command and runs it. It returns a process exit code.
func Execute() int {
	app := &App{}
	root := app.newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	return 0
}

func (a *App) newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "vortex",
		Short:         "Vortex — deploy and manage apps on the Vortex hosting platform",
		Long:          "vortex is the command-line interface for the Vortex hosting platform.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Load config and build the client before any sub-command runs.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadDefault()
			if err != nil {
				return err
			}
			if a.apiURLFlag != "" {
				cfg.APIURL = a.apiURLFlag
			}
			a.cfg = cfg
			a.client = client.New(cfg.APIURL, tokenStore{cfg})
			return nil
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&a.apiURLFlag, "api-url", "", "Vortex API base URL (overrides config)")
	pf.StringVar(&a.orgFlag, "org", "", "organization id (overrides current context)")
	pf.StringVar(&a.projectFlag, "project", "", "project id (overrides current context)")
	pf.BoolVar(&a.jsonOut, "json", false, "output JSON for scripting")

	root.AddCommand(
		a.newAuthCmd(),
		a.newOrgsCmd(),
		a.newProjectsCmd(),
		a.newAppsCmd(),
		a.newServicesCmd(),
		a.newDatabasesCmd(),
		a.newSecretsCmd(),
		a.newPlansCmd(),
		a.newPricingCmd(),
		a.newConfigCmd(),
		a.newVersionCmd(),
	)
	return root
}

// ctx returns the command's context (or Background as a fallback).
func ctx(cmd *cobra.Command) context.Context {
	if c := cmd.Context(); c != nil {
		return c
	}
	return context.Background()
}

// requireAuth returns an error when no access token is configured.
func (a *App) requireAuth() error {
	if !a.cfg.LoggedIn() {
		return fmt.Errorf("not logged in: run `vortex auth login` first")
	}
	return nil
}

// orgID resolves the effective org: --org flag, else the persisted context.
func (a *App) orgID() (string, error) {
	if a.orgFlag != "" {
		return a.orgFlag, nil
	}
	if a.cfg.CurrentOrg != "" {
		return a.cfg.CurrentOrg, nil
	}
	return "", fmt.Errorf("no organization set: pass --org or run `vortex config set-context --org <id>`")
}

// projectID resolves the effective project: --project flag, else context.
func (a *App) projectID() (string, error) {
	if a.projectFlag != "" {
		return a.projectFlag, nil
	}
	if a.cfg.CurrentProj != "" {
		return a.cfg.CurrentProj, nil
	}
	return "", fmt.Errorf("no project set: pass --project or run `vortex config set-context --project <id>`")
}
