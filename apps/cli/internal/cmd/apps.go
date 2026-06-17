package cmd

import (
	"fmt"
	"strconv"

	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/client"
	"github.com/spf13/cobra"
)

func (a *App) newAppsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "apps",
		Aliases: []string{"app"},
		Short:   "Manage applications",
	}
	cmd.AddCommand(
		a.newAppsListCmd(),
		a.newAppsCreateCmd(),
		a.newAppsStatusCmd(),
		a.newAppsDeployCmd(),
		a.newAppsLogsCmd(),
		a.newAppsRestartCmd(),
		a.newAppsStopCmd(),
		a.newAppsDestroyCmd(),
	)
	return cmd
}

func (a *App) newAppsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List apps in the current organization (or project with --project)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			var apps []client.App
			// Filter by project when --project is set or persisted in context.
			if a.projectFlag != "" || a.cfg.CurrentProj != "" {
				projID, _ := a.projectID()
				apps, err = a.client.ListProjectApps(ctx(cmd), orgID, projID)
			} else {
				apps, err = a.client.ListApps(ctx(cmd), orgID)
			}
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), apps, func() {
				rows := make([][]string, 0, len(apps))
				for _, app := range apps {
					rows = append(rows, []string{
						app.ID, app.Name, app.Status,
						fmt.Sprintf("%.2f", app.CPU),
						strconv.Itoa(app.MemoryMB),
						dash(app.Host),
					})
				}
				table(cmd.OutOrStdout(),
					[]string{"ID", "NAME", "STATUS", "CPU", "MEM(MB)", "HOST"}, rows)
			})
		},
	}
}

func (a *App) newAppsCreateCmd() *cobra.Command {
	var (
		gitRepo, gitBranch, buildPack string
		cpu                           float64
		memMB                         int
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new app in the current org/project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			in := client.CreateAppInput{
				Name:          args[0],
				GitRepository: gitRepo,
				GitBranch:     gitBranch,
				BuildPack:     buildPack,
				CPU:           cpu,
				MemoryMB:      memMB,
			}
			// Send projectId when a project is selected; otherwise the server
			// defaults to the org's default project.
			if a.projectFlag != "" || a.cfg.CurrentProj != "" {
				in.ProjectID, _ = a.projectID()
			}
			app, err := a.client.CreateApp(ctx(cmd), orgID, in)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), app, func() {
				printAppDetail(cmd, app)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&gitRepo, "git", "", "git repository URL")
	f.StringVar(&gitBranch, "branch", "", "git branch (default main)")
	f.StringVar(&buildPack, "buildpack", "", "build pack")
	f.Float64Var(&cpu, "cpu", 0, "requested vCPU (server default when 0)")
	f.IntVar(&memMB, "memory", 0, "requested memory in MB (server default when 0)")
	return cmd
}

func (a *App) newAppsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <app-id>",
		Short: "Show detailed status for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			app, err := a.client.GetApp(ctx(cmd), orgID, args[0])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), app, func() { printAppDetail(cmd, app) })
		},
	}
}

// appActionCmd builds a deploy/restart/stop command sharing the same shape.
func (a *App) appActionCmd(use, short string, fn func(cmd *cobra.Command, orgID, appID string) (*client.App, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <app-id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			app, err := fn(cmd, orgID, args[0])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), app, func() {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: status %s\n", app.Name, app.Status)
			})
		},
	}
}

func (a *App) newAppsDeployCmd() *cobra.Command {
	return a.appActionCmd("deploy", "Deploy (or redeploy) an app", func(cmd *cobra.Command, o, id string) (*client.App, error) {
		return a.client.DeployApp(ctx(cmd), o, id)
	})
}

func (a *App) newAppsRestartCmd() *cobra.Command {
	return a.appActionCmd("restart", "Restart an app", func(cmd *cobra.Command, o, id string) (*client.App, error) {
		return a.client.RestartApp(ctx(cmd), o, id)
	})
}

func (a *App) newAppsStopCmd() *cobra.Command {
	return a.appActionCmd("stop", "Stop an app (scale to zero)", func(cmd *cobra.Command, o, id string) (*client.App, error) {
		return a.client.StopApp(ctx(cmd), o, id)
	})
}

func (a *App) newAppsLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs <app-id>",
		Short: "Show recent logs for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			logs, err := a.client.AppLogs(ctx(cmd), orgID, args[0])
			if err != nil {
				return err
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), map[string]string{"logs": logs})
			}
			if logs == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "(no logs — app not deployed yet)")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), logs)
			return nil
		},
	}
}

func (a *App) newAppsDestroyCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "destroy <app-id>",
		Aliases: []string{"delete", "rm"},
		Short:   "Destroy an app",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			if !yes {
				ans := prompt(fmt.Sprintf("Destroy app %s? This cannot be undone. [y/N]: ", args[0]))
				if ans != "y" && ans != "Y" && ans != "yes" {
					fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
					return nil
				}
			}
			if err := a.client.DestroyApp(ctx(cmd), orgID, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Destroyed app %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	return cmd
}

func printAppDetail(cmd *cobra.Command, app *client.App) {
	rows := [][]string{
		{"ID", app.ID},
		{"Name", app.Name},
		{"Status", app.Status},
		{"Project", dash(app.ProjectID)},
		{"CPU", fmt.Sprintf("%.2f", app.CPU)},
		{"Memory(MB)", strconv.Itoa(app.MemoryMB)},
		{"Git", dash(app.GitRepository)},
		{"Branch", dash(app.GitBranch)},
		{"Namespace", dash(app.Namespace)},
		{"Host", dash(app.Host)},
	}
	table(cmd.OutOrStdout(), []string{"FIELD", "VALUE"}, rows)
}
