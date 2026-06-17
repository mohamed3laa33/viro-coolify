package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func (a *App) newSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "secrets",
		Aliases: []string{"secret", "env"},
		Short:   "Manage app environment variables / secrets",
	}
	cmd.AddCommand(a.newSecretsListCmd(), a.newSecretsSetCmd(), a.newSecretsUnsetCmd())
	return cmd
}

func (a *App) newSecretsListCmd() *cobra.Command {
	var appID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List an app's environment variables",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			env, err := a.client.ListEnv(ctx(cmd), orgID, appID)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), env, func() {
				rows := make([][]string, 0, len(env))
				for _, e := range env {
					rows = append(rows, []string{e.Key, e.Value})
				}
				table(cmd.OutOrStdout(), []string{"KEY", "VALUE"}, rows)
			})
		},
	}
	cmd.Flags().StringVarP(&appID, "app", "a", "", "app id (required)")
	_ = cmd.MarkFlagRequired("app")
	return cmd
}

func (a *App) newSecretsSetCmd() *cobra.Command {
	var appID string
	cmd := &cobra.Command{
		Use:   "set KEY=VALUE [KEY=VALUE ...]",
		Short: "Set one or more environment variables on an app",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			for _, kv := range args {
				key, value, ok := strings.Cut(kv, "=")
				if !ok || key == "" {
					return fmt.Errorf("invalid KEY=VALUE pair: %q", kv)
				}
				if _, err := a.client.SetEnv(ctx(cmd), orgID, appID, key, value); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set %d secret(s) on app %s. Redeploy to apply.\n", len(args), appID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&appID, "app", "a", "", "app id (required)")
	_ = cmd.MarkFlagRequired("app")
	return cmd
}

func (a *App) newSecretsUnsetCmd() *cobra.Command {
	var appID string
	cmd := &cobra.Command{
		Use:   "unset KEY [KEY ...]",
		Short: "Remove one or more environment variables from an app",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			for _, key := range args {
				if err := a.client.UnsetEnv(ctx(cmd), orgID, appID, key); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %d secret(s) from app %s. Redeploy to apply.\n", len(args), appID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&appID, "app", "a", "", "app id (required)")
	_ = cmd.MarkFlagRequired("app")
	return cmd
}
