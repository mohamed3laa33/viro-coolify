package cmd

import (
	"fmt"
	"strconv"
	"strings"

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
		a.newAppsUpdateCmd(),
		a.newAppsScaleCmd(),
		a.newAppsDeployCmd(),
		a.newAppsLogsCmd(),
		a.newAppsRestartCmd(),
		a.newAppsStopCmd(),
		a.newAppsReleasesCmd(),
		a.newAppsRollbackCmd(),
		a.newAppsBuildsCmd(),
		a.newAppsMetricsCmd(),
		a.newAppsDomainsCmd(),
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
		image, gitRepo, gitBranch, buildPack string
		cpu                                  float64
		memMB                                int
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new app in the current org/project (--image for a container, --git-repo to build from source)",
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
				Image:         image,
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
				printAppRow(cmd, app)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&image, "image", "", "container image to deploy directly (no build)")
	f.StringVar(&gitRepo, "git-repo", "", "git repository URL to build from source")
	f.StringVar(&gitBranch, "git-branch", "", "git branch (default main)")
	f.StringVar(&buildPack, "buildpack", "", "build pack")
	f.Float64Var(&cpu, "cpu", 0, "requested vCPU (server default when 0)")
	f.IntVar(&memMB, "memory", 0, "requested memory in MB (server default when 0)")
	// Backwards-compatible alias for the previous --git/--branch flags.
	f.StringVar(&gitRepo, "git", "", "alias for --git-repo")
	f.StringVar(&gitBranch, "branch", "", "alias for --git-branch")
	_ = f.MarkHidden("git")
	_ = f.MarkHidden("branch")
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
			d, err := a.client.GetApp(ctx(cmd), orgID, args[0])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), d, func() { printAppDetail(cmd, d) })
		},
	}
}

func (a *App) newAppsUpdateCmd() *cobra.Command {
	var (
		image, gitRepo, gitBranch string
		cpu                       float64
		memMB                     int
	)
	cmd := &cobra.Command{
		Use:   "update <app-id>",
		Short: "Update an app's image / CPU / memory / git source (only the flags you pass change)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			var in client.UpdateAppInput
			f := cmd.Flags()
			if f.Changed("image") {
				in.Image = &image
			}
			if f.Changed("git-repo") {
				in.GitRepository = &gitRepo
			}
			if f.Changed("git-branch") {
				in.GitBranch = &gitBranch
			}
			if f.Changed("cpu") {
				in.CPU = &cpu
			}
			if f.Changed("memory") {
				in.MemoryMB = &memMB
			}
			if in.Image == nil && in.GitRepository == nil && in.GitBranch == nil && in.CPU == nil && in.MemoryMB == nil {
				return fmt.Errorf("nothing to update: pass --image, --cpu, --memory, --git-repo and/or --git-branch")
			}
			app, err := a.client.UpdateApp(ctx(cmd), orgID, args[0], in)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), app, func() { printAppRow(cmd, app) })
		},
	}
	f := cmd.Flags()
	f.StringVar(&image, "image", "", "new container image")
	f.StringVar(&gitRepo, "git-repo", "", "new git repository URL")
	f.StringVar(&gitBranch, "git-branch", "", "new git branch")
	f.Float64Var(&cpu, "cpu", 0, "new requested vCPU")
	f.IntVar(&memMB, "memory", 0, "new requested memory in MB")
	return cmd
}

func (a *App) newAppsScaleCmd() *cobra.Command {
	var minR, maxR int
	cmd := &cobra.Command{
		Use:   "scale <app-id>",
		Short: "Set an app's autoscaling bounds (--min/--max)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			var in client.ScaleAppInput
			f := cmd.Flags()
			if f.Changed("min") {
				in.MinReplicas = &minR
			}
			if f.Changed("max") {
				in.MaxReplicas = &maxR
			}
			if in.MinReplicas == nil && in.MaxReplicas == nil {
				return fmt.Errorf("nothing to scale: pass --min and/or --max")
			}
			app, err := a.client.ScaleApp(ctx(cmd), orgID, args[0], in)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), app, func() {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: min=%d max=%d\n", app.Name, app.MinReplicas, app.MaxReplicas)
			})
		},
	}
	f := cmd.Flags()
	f.IntVar(&minR, "min", 0, "minimum replicas (0 = scale to zero for stateless apps)")
	f.IntVar(&maxR, "max", 0, "maximum replicas")
	return cmd
}

func (a *App) newAppsReleasesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "releases <app-id>",
		Short: "List an app's release history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			rels, err := a.client.ListReleases(ctx(cmd), orgID, args[0])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), rels, func() {
				rows := make([][]string, 0, len(rels))
				for _, r := range rels {
					rows = append(rows, []string{
						strconv.Itoa(r.Revision), r.Status, dash(r.Image),
						fmt.Sprintf("%.2f", r.CPU), strconv.Itoa(r.MemoryMB),
						dash(r.Note), r.CreatedAt.Format("2006-01-02 15:04"),
					})
				}
				table(cmd.OutOrStdout(),
					[]string{"REV", "STATUS", "IMAGE", "CPU", "MEM(MB)", "NOTE", "CREATED"}, rows)
			})
		},
	}
}

func (a *App) newAppsRollbackCmd() *cobra.Command {
	var revision int
	cmd := &cobra.Command{
		Use:   "rollback <app-id>",
		Short: "Roll an app back to a prior revision (defaults to the previous release)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			app, err := a.client.Rollback(ctx(cmd), orgID, args[0], revision)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), app, func() {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: rolled back, status %s\n", app.Name, app.Status)
			})
		},
	}
	cmd.Flags().IntVar(&revision, "revision", 0, "target revision (0 = previous release)")
	return cmd
}

func (a *App) newAppsBuildsCmd() *cobra.Command {
	var logsID string
	cmd := &cobra.Command{
		Use:   "builds <app-id>",
		Short: "List an app's git-source image builds (--logs <build-id> for one build's logs)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			if logsID != "" {
				b, err := a.client.GetBuild(ctx(cmd), orgID, args[0], logsID)
				if err != nil {
					return err
				}
				if a.jsonOut {
					return printJSON(cmd.OutOrStdout(), b)
				}
				if b.Logs == "" {
					fmt.Fprintf(cmd.OutOrStdout(), "(no logs for build %s — status %s)\n", b.ID, b.Status)
					return nil
				}
				fmt.Fprintln(cmd.OutOrStdout(), b.Logs)
				return nil
			}
			builds, err := a.client.ListBuilds(ctx(cmd), orgID, args[0])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), builds, func() {
				rows := make([][]string, 0, len(builds))
				for _, b := range builds {
					rows = append(rows, []string{
						b.ID, b.Status, dash(b.CommitRef), dash(b.Image),
						b.CreatedAt.Format("2006-01-02 15:04"),
					})
				}
				table(cmd.OutOrStdout(),
					[]string{"ID", "STATUS", "REF", "IMAGE", "CREATED"}, rows)
			})
		},
	}
	cmd.Flags().StringVar(&logsID, "logs", "", "show captured logs for a single build id")
	return cmd
}

func (a *App) newAppsMetricsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "metrics <app-id>",
		Short: "Show an app's live pod CPU/memory usage",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			m, err := a.client.AppMetrics(ctx(cmd), orgID, args[0])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), m, func() {
				if !m.Available {
					fmt.Fprintf(cmd.OutOrStdout(), "metrics unavailable: %s\n", dash(m.Unavailable))
					return
				}
				rows := make([][]string, 0, len(m.Pods)+1)
				for _, p := range m.Pods {
					rows = append(rows, []string{p.Name,
						fmt.Sprintf("%dm", p.CPUMillicores), fmtBytes(p.MemoryBytes)})
				}
				rows = append(rows, []string{"TOTAL",
					fmt.Sprintf("%dm", m.CPUMillicores), fmtBytes(m.MemoryBytes)})
				table(cmd.OutOrStdout(), []string{"POD", "CPU", "MEMORY"}, rows)
			})
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
	var (
		follow  bool
		allPods bool
		since   string
		tail    int
	)
	cmd := &cobra.Command{
		Use:   "logs <app-id>",
		Short: "Show recent logs for an app (--follow streams live; --tail/--since filter the snapshot)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			// --follow consumes the live SSE stream until interrupted (Ctrl-C).
			if follow {
				out := cmd.OutOrStdout()
				return a.client.FollowAppLogs(ctx(cmd), orgID, args[0], allPods, func(line string) {
					fmt.Fprintln(out, line)
				})
			}
			logs, err := a.client.AppLogs(ctx(cmd), orgID, args[0])
			if err != nil {
				return err
			}
			logs = filterLogs(logs, since, tail)
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
	f := cmd.Flags()
	f.BoolVarP(&follow, "follow", "f", false, "stream logs live (Server-Sent Events)")
	f.BoolVar(&allPods, "all", false, "with --follow, stream every pod's logs")
	f.StringVar(&since, "since", "", "only show snapshot lines containing this substring (e.g. a date/level)")
	f.IntVar(&tail, "tail", 0, "with the snapshot, show only the last N lines (0 = all)")
	return cmd
}

// filterLogs applies the client-side --since (substring) and --tail (last N
// lines) filters to a one-shot log snapshot. The live --follow stream is already
// tailed server-side, so these only affect the snapshot.
func filterLogs(logs, since string, tail int) string {
	if logs == "" || (since == "" && tail <= 0) {
		return logs
	}
	lines := strings.Split(strings.TrimRight(logs, "\n"), "\n")
	if since != "" {
		kept := lines[:0:0]
		for _, l := range lines {
			if strings.Contains(l, since) {
				kept = append(kept, l)
			}
		}
		lines = kept
	}
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return strings.Join(lines, "\n")
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

// printAppRow renders a one-line-ish app summary (used by create/update).
func printAppRow(cmd *cobra.Command, app *client.App) {
	table(cmd.OutOrStdout(),
		[]string{"ID", "NAME", "STATUS", "CPU", "MEM(MB)", "HOST"},
		[][]string{{app.ID, app.Name, app.Status,
			fmt.Sprintf("%.2f", app.CPU), strconv.Itoa(app.MemoryMB), dash(app.Host)}})
}

func printAppDetail(cmd *cobra.Command, d *client.AppDetail) {
	app := d.App
	rows := [][]string{
		{"ID", app.ID},
		{"Name", app.Name},
		{"Status", app.Status},
		{"Project", dash(app.ProjectID)},
		{"Image", dash(app.Image)},
		{"CPU", fmt.Sprintf("%.2f", app.CPU)},
		{"Memory(MB)", strconv.Itoa(app.MemoryMB)},
		{"Git", dash(app.GitRepository)},
		{"Branch", dash(app.GitBranch)},
		{"Namespace", dash(app.Namespace)},
		{"Host", dash(app.Host)},
	}
	if d.CurrentRelease != nil {
		rows = append(rows, []string{"Release", fmt.Sprintf("r%d (%s)", d.CurrentRelease.Revision, d.CurrentRelease.Status)})
	}
	table(cmd.OutOrStdout(), []string{"FIELD", "VALUE"}, rows)
}

// fmtBytes renders a byte count in a human-readable binary unit.
func fmtBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
