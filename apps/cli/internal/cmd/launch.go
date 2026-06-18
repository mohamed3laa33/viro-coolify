package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/client"
	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/manifest"
	"github.com/spf13/cobra"
)

// newLaunchCmd implements `vortex launch`: the one-command on-ramp. From a
// directory it detects the app (a Dockerfile => image/build path; otherwise it
// prompts for an image or git source), scaffolds a minimal vortex.yaml manifest,
// creates the app via the API, and (unless --no-deploy) deploys it.
//
// It invents no business values: CPU/memory are left unset so the control plane
// applies the admin-configured plan defaults (invariant #1), and the deploy is a
// real API call — there is no fake-success path (invariant #6).
func (a *App) newLaunchCmd() *cobra.Command {
	var (
		dir       string
		name      string
		image     string
		gitRepo   string
		gitBranch string
		buildpack string
		cpu       float64
		memMB     int
		noDeploy  bool
		yes       bool
	)
	cmd := &cobra.Command{
		Use:   "launch [path]",
		Short: "Detect, scaffold, create and deploy an app from a directory in one command",
		Long: "launch inspects a directory, writes a vortex.yaml manifest, creates the app\n" +
			"in your current org/project and deploys it. Re-running in a directory that\n" +
			"already has a manifest deploys the existing app instead of recreating it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			if len(args) == 1 {
				dir = args[0]
			}
			if dir == "" {
				dir = "."
			}
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				return fmt.Errorf("path %q is not a directory", dir)
			}

			out := cmd.OutOrStdout()

			// Reuse an existing manifest if one is present: launch is idempotent.
			man, err := manifest.LoadIn(dir)
			switch {
			case err == nil:
				fmt.Fprintf(out, "Found existing %s — deploying it.\n", manifest.PathIn(dir))
			case errors.Is(err, manifest.ErrNotFound):
				man, err = a.scaffold(cmd, dir, launchOpts{
					name: name, image: image, gitRepo: gitRepo,
					gitBranch: gitBranch, buildpack: buildpack, cpu: cpu, memMB: memMB, yes: yes,
				})
				if err != nil {
					return err
				}
			default:
				return err
			}
			if err := man.Validate(); err != nil {
				return err
			}

			orgID, err := a.resolveLaunchOrg(cmd, man)
			if err != nil {
				return err
			}
			projID, err := a.resolveLaunchProject(cmd, orgID, man)
			if err != nil {
				return err
			}

			app, err := a.ensureApp(cmd, orgID, projID, man)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "App %s (%s) ready in org %s.\n", app.Name, app.ID, orgID)

			if noDeploy {
				fmt.Fprintln(out, "Skipping deploy (--no-deploy). Run `vortex apps deploy "+app.Name+"` when ready.")
				return a.emit(out, app, func() { printAppRow(cmd, app) })
			}

			deployed, err := a.client.DeployApp(ctx(cmd), orgID, app.ID)
			if err != nil {
				return fmt.Errorf("deploy %s: %w", app.Name, err)
			}
			return a.emit(out, deployed, func() {
				fmt.Fprintf(out, "Deploying %s: status %s\n", deployed.Name, deployed.Status)
				if deployed.Host != "" {
					fmt.Fprintf(out, "URL: https://%s\n", deployed.Host)
				}
				fmt.Fprintf(out, "Tail logs with: vortex apps logs %s --follow\n", deployed.Name)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&dir, "path", "", "directory to launch from (default: positional arg or current directory)")
	f.StringVar(&name, "name", "", "app name (default: sanitized directory name)")
	f.StringVar(&image, "image", "", "container image to deploy directly (skips prompts)")
	f.StringVar(&gitRepo, "git-repo", "", "git repository URL to build from source")
	f.StringVar(&gitBranch, "git-branch", "", "git branch (default: detected or main)")
	f.StringVar(&buildpack, "buildpack", "", "buildpack for git-source apps")
	f.Float64Var(&cpu, "cpu", 0, "requested vCPU (0 = admin-configured plan default)")
	f.IntVar(&memMB, "memory", 0, "requested memory in MB (0 = admin-configured plan default)")
	f.BoolVar(&noDeploy, "no-deploy", false, "create the app and write the manifest but don't deploy")
	f.BoolVarP(&yes, "yes", "y", false, "accept detected defaults without prompting (non-interactive)")
	return cmd
}

// launchOpts carries the resolved launch flags into scaffolding.
type launchOpts struct {
	name, image, gitRepo, gitBranch, buildpack string
	cpu                                        float64
	memMB                                      int
	yes                                        bool
}

// scaffold inspects dir, decides the build source (flags > detection > prompts),
// writes a vortex.yaml manifest, and returns it.
func (a *App) scaffold(cmd *cobra.Command, dir string, o launchOpts) (*manifest.Manifest, error) {
	out := cmd.OutOrStdout()
	det := manifest.Detect(dir)

	name := o.name
	if name == "" {
		name = det.SuggestedName
		if !o.yes {
			if in := prompt(fmt.Sprintf("App name [%s]: ", name)); in != "" {
				name = manifest.SanitizeName(in)
			}
		}
	} else {
		name = manifest.SanitizeName(name)
	}

	build, err := a.resolveBuild(cmd, det, o)
	if err != nil {
		return nil, err
	}

	man := &manifest.Manifest{
		App:      name,
		Org:      a.orgFlag,
		Project:  a.projectFlag,
		Build:    build,
		CPU:      o.cpu,
		MemoryMB: o.memMB,
	}
	path := manifest.PathIn(dir)
	if err := man.Save(path); err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "Wrote manifest %s\n", path)
	return man, nil
}

// resolveBuild chooses the workload source: explicit flags win; otherwise a
// detected Dockerfile or git remote is offered; otherwise the user is prompted.
// It never fabricates an image — a launch with no resolvable source is an error.
func (a *App) resolveBuild(cmd *cobra.Command, det manifest.Detection, o launchOpts) (manifest.Build, error) {
	out := cmd.OutOrStdout()

	// 1) Explicit flags take precedence.
	if o.image != "" {
		return manifest.Build{Image: o.image}, nil
	}
	if o.gitRepo != "" {
		return manifest.Build{GitRepository: o.gitRepo, GitBranch: o.gitBranch, Buildpack: o.buildpack}, nil
	}

	// 2) A Dockerfile (or detected git remote) drives a source build by the
	//    control plane. Prefer git source when a remote is present so the server
	//    can build the Dockerfile; this matches `apps create --git-repo`.
	if det.HasDockerfile {
		if det.Language != "" {
			fmt.Fprintf(out, "Detected a %s app with a Dockerfile.\n", det.Language)
		} else {
			fmt.Fprintln(out, "Detected a Dockerfile.")
		}
		if det.GitRemote != "" {
			if o.yes || confirm(fmt.Sprintf("Build from git source %s (branch %s)?", det.GitRemote, dashBranch(det.GitBranch))) {
				return manifest.Build{GitRepository: det.GitRemote, GitBranch: det.GitBranch, Buildpack: o.buildpack}, nil
			}
		}
	}

	// 3) Interactive: ask for an image or a git repo. In --yes mode with no
	//    detected source we cannot proceed without inventing values.
	if o.yes {
		if det.GitRemote != "" {
			return manifest.Build{GitRepository: det.GitRemote, GitBranch: det.GitBranch, Buildpack: o.buildpack}, nil
		}
		return manifest.Build{}, errors.New("no Dockerfile, --image or --git-repo found; cannot launch non-interactively (pass --image or --git-repo)")
	}

	fmt.Fprintln(out, "No build source detected. Provide one:")
	if det.GitRemote != "" {
		fmt.Fprintf(out, "  (detected git remote: %s)\n", det.GitRemote)
	}
	img := prompt("Container image to deploy (e.g. nginx:1.27), or leave blank to build from git: ")
	if img != "" {
		return manifest.Build{Image: img}, nil
	}
	repo := prompt(fmt.Sprintf("Git repository URL [%s]: ", det.GitRemote))
	if repo == "" {
		repo = det.GitRemote
	}
	if repo == "" {
		return manifest.Build{}, errors.New("an image or git repository is required to launch")
	}
	branch := prompt(fmt.Sprintf("Git branch [%s]: ", dashBranch(det.GitBranch)))
	if branch == "" {
		branch = det.GitBranch
	}
	return manifest.Build{GitRepository: repo, GitBranch: branch, Buildpack: o.buildpack}, nil
}

// resolveLaunchOrg resolves the org for a launch: the --org flag wins, then the
// manifest's org, then the persisted context.
func (a *App) resolveLaunchOrg(cmd *cobra.Command, man *manifest.Manifest) (string, error) {
	if a.orgFlag == "" && man.Org != "" {
		a.orgFlag = man.Org // seed resolution from the manifest
	}
	return a.resolveOrgID(cmd)
}

// resolveLaunchProject resolves the project for a launch (optional). An empty
// result lets the server use the org's default project.
func (a *App) resolveLaunchProject(cmd *cobra.Command, orgID string, man *manifest.Manifest) (string, error) {
	ref := a.projectFlag
	if ref == "" {
		ref = man.Project
	}
	if ref == "" {
		if a.cfg.CurrentProj == "" {
			return "", nil // server defaults to the org's default project
		}
		ref = a.cfg.CurrentProj
	}
	return a.resolveProjectRef(cmd, orgID, ref)
}

// ensureApp finds an existing app matching the manifest's name in the target
// org, else creates it. This makes `vortex launch` idempotent: re-running
// updates+deploys rather than failing on a name conflict.
func (a *App) ensureApp(cmd *cobra.Command, orgID, projID string, man *manifest.Manifest) (*client.App, error) {
	apps, err := a.client.ListApps(ctx(cmd), orgID)
	if err == nil {
		for i := range apps {
			if strings.EqualFold(apps[i].Name, man.App) {
				return &apps[i], nil
			}
		}
	}
	in := client.CreateAppInput{
		Name:          man.App,
		ProjectID:     projID,
		Image:         man.Build.Image,
		GitRepository: man.Build.GitRepository,
		GitBranch:     man.Build.GitBranch,
		BuildPack:     man.Build.Buildpack,
		CPU:           man.CPU,
		MemoryMB:      man.MemoryMB,
	}
	return a.client.CreateApp(ctx(cmd), orgID, in)
}

// confirm asks a yes/no question on stderr (default no).
func confirm(question string) bool {
	ans := strings.ToLower(prompt(question + " [y/N]: "))
	return ans == "y" || ans == "yes"
}

// dashBranch renders an empty branch as "main" for prompts/instructions.
func dashBranch(b string) string {
	if b == "" {
		return "main"
	}
	return b
}
