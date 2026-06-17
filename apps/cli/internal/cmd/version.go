package cmd

import (
	"fmt"

	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/client"
	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/version"
	"github.com/spf13/cobra"
)

func (a *App) newVersionCmd() *cobra.Command {
	var checkServer bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show the vortex CLI version (and optionally the API server version)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := map[string]any{
				"cli": map[string]string{
					"version": version.Version,
					"commit":  version.Commit,
				},
			}
			var srv *client.Version
			if checkServer {
				v, err := a.client.Version(ctx(cmd))
				if err == nil {
					srv = v
					out["server"] = v
				} else {
					out["serverError"] = err.Error()
				}
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), out)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "vortex %s (commit %s)\n", version.Version, version.Commit)
			if checkServer && srv != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "server %s (%s)\n", srv.Version, srv.Env)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkServer, "server", false, "also query the API server version")
	return cmd
}
