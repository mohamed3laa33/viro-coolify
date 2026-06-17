package cmd

import "github.com/spf13/cobra"

func (a *App) newOrgsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "orgs",
		Aliases: []string{"org", "organizations"},
		Short:   "Manage organizations",
	}
	cmd.AddCommand(a.newOrgsListCmd(), a.newOrgsCreateCmd())
	return cmd
}

func (a *App) newOrgsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the organizations you belong to",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgs, err := a.client.ListOrgs(ctx(cmd))
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), orgs, func() {
				rows := make([][]string, 0, len(orgs))
				for _, o := range orgs {
					current := ""
					if o.ID == a.cfg.CurrentOrg {
						current = "*"
					}
					rows = append(rows, []string{current, o.ID, o.Name, dash(o.Slug)})
				}
				table(cmd.OutOrStdout(), []string{"", "ID", "NAME", "SLUG"}, rows)
			})
		},
	}
}

func (a *App) newOrgsCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			org, err := a.client.CreateOrg(ctx(cmd), args[0])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), org, func() {
				table(cmd.OutOrStdout(), []string{"ID", "NAME", "SLUG"},
					[][]string{{org.ID, org.Name, dash(org.Slug)}})
			})
		},
	}
}
