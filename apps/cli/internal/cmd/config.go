package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (a *App) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage CLI configuration and context",
	}
	cmd.AddCommand(a.newConfigSetContextCmd(), a.newConfigShowCmd())
	return cmd
}

func (a *App) newConfigSetContextCmd() *cobra.Command {
	var org, project, apiURL string
	cmd := &cobra.Command{
		Use:   "set-context",
		Short: "Set the default org/project (and optionally the API URL)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			changed := false
			if cmd.Flags().Changed("org") {
				a.cfg.CurrentOrg = org
				changed = true
			}
			if cmd.Flags().Changed("project") {
				a.cfg.CurrentProj = project
				changed = true
			}
			if cmd.Flags().Changed("api-url") {
				a.cfg.APIURL = apiURL
				changed = true
			}
			if !changed {
				return fmt.Errorf("nothing to set: pass --org, --project and/or --api-url")
			}
			if err := a.cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Context updated.")
			return nil
		},
	}
	// Local flags so they don't collide with the persistent --org/--project.
	f := cmd.Flags()
	f.StringVar(&org, "org", "", "default organization name or id")
	f.StringVar(&project, "project", "", "default project name or id")
	f.StringVar(&apiURL, "api-url", "", "Vortex API base URL")
	return cmd
}

func (a *App) newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the current configuration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			view := map[string]any{
				"path":            a.cfg.Path(),
				"api_url":         a.cfg.APIURL,
				"current_org":     a.cfg.CurrentOrg,
				"current_project": a.cfg.CurrentProj,
				"logged_in":       a.cfg.LoggedIn(),
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), view)
			}
			table(cmd.OutOrStdout(), []string{"FIELD", "VALUE"}, [][]string{
				{"Config path", a.cfg.Path()},
				{"API URL", a.cfg.APIURL},
				{"Current org", dash(a.cfg.CurrentOrg)},
				{"Current project", dash(a.cfg.CurrentProj)},
				{"Logged in", fmt.Sprintf("%t", a.cfg.LoggedIn())},
			})
			return nil
		},
	}
}
