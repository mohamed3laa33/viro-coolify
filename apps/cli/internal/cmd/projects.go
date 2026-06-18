package cmd

import "github.com/spf13/cobra"

func (a *App) newProjectsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "projects",
		Aliases: []string{"project"},
		Short:   "Manage projects within an organization",
	}
	cmd.AddCommand(a.newProjectsListCmd(), a.newProjectsCreateCmd())
	return cmd
}

func (a *App) newProjectsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List projects in the current organization",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(cmd)
			if err != nil {
				return err
			}
			projects, err := a.client.ListProjects(ctx(cmd), orgID)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), projects, func() {
				rows := make([][]string, 0, len(projects))
				for _, p := range projects {
					current := ""
					if p.ID == a.cfg.CurrentProj {
						current = "*"
					}
					def := ""
					if p.IsDefault {
						def = "yes"
					}
					rows = append(rows, []string{current, p.ID, p.Name, dash(p.Slug), def})
				}
				table(cmd.OutOrStdout(), []string{"", "ID", "NAME", "SLUG", "DEFAULT"}, rows)
			})
		},
	}
}

func (a *App) newProjectsCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a project in the current organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(cmd)
			if err != nil {
				return err
			}
			p, err := a.client.CreateProject(ctx(cmd), orgID, args[0])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), p, func() {
				table(cmd.OutOrStdout(), []string{"ID", "NAME", "SLUG"},
					[][]string{{p.ID, p.Name, dash(p.Slug)}})
			})
		},
	}
}
