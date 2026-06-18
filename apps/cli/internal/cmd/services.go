package cmd

import (
	"fmt"
	"strconv"

	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/client"
	"github.com/spf13/cobra"
)

func (a *App) newServicesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "services",
		Aliases: []string{"service"},
		Short:   "Manage one-click services",
	}
	cmd.AddCommand(
		a.newServicesCatalogCmd(),
		a.newServicesListCmd(),
		a.newServicesCreateCmd(),
		a.newServiceActionCmd("deploy", "Deploy (or redeploy) a service", func(cmd *cobra.Command, o, id string) (*client.Service, error) {
			return a.client.DeployService(ctx(cmd), o, id)
		}),
		a.newServiceActionCmd("stop", "Stop a service", func(cmd *cobra.Command, o, id string) (*client.Service, error) {
			return a.client.StopService(ctx(cmd), o, id)
		}),
		a.newServiceActionCmd("restart", "Restart a service", func(cmd *cobra.Command, o, id string) (*client.Service, error) {
			return a.client.RestartService(ctx(cmd), o, id)
		}),
		a.newServicesDestroyCmd(),
	)
	return cmd
}

func (a *App) newServiceActionCmd(use, short string, fn func(cmd *cobra.Command, orgID, svcID string) (*client.Service, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <service-id>",
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
			svc, err := fn(cmd, orgID, args[0])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), svc, func() {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: status %s\n", svc.Name, svc.Status)
			})
		},
	}
}

func (a *App) newServicesDestroyCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "destroy <service-id>",
		Aliases: []string{"delete", "rm"},
		Short:   "Destroy a service",
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
				ans := prompt(fmt.Sprintf("Destroy service %s? This cannot be undone. [y/N]: ", args[0]))
				if ans != "y" && ans != "Y" && ans != "yes" {
					fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
					return nil
				}
			}
			if err := a.client.DestroyService(ctx(cmd), orgID, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Destroyed service %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	return cmd
}

func (a *App) newServicesCatalogCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "catalog",
		Short: "List the one-click services catalog",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Public endpoint — no auth required.
			items, err := a.client.ServiceCatalog(ctx(cmd))
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), items, func() {
				rows := make([][]string, 0, len(items))
				for _, t := range items {
					rows = append(rows, []string{t.Key, t.Name, dash(t.Category), dash(t.Kind), dash(t.Description)})
				}
				table(cmd.OutOrStdout(), []string{"KEY", "NAME", "CATEGORY", "KIND", "DESCRIPTION"}, rows)
			})
		},
	}
}

func (a *App) newServicesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List provisioned services in the current organization",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			svcs, err := a.client.ListServices(ctx(cmd), orgID)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), svcs, func() {
				rows := make([][]string, 0, len(svcs))
				for _, s := range svcs {
					rows = append(rows, []string{s.ID, s.Name, s.Template, s.Status, dash(s.Host)})
				}
				table(cmd.OutOrStdout(), []string{"ID", "NAME", "TEMPLATE", "STATUS", "HOST"}, rows)
			})
		},
	}
}

func (a *App) newServicesCreateCmd() *cobra.Command {
	var (
		name  string
		cpu   float64
		memMB int
	)
	cmd := &cobra.Command{
		Use:   "create <template-key>",
		Short: "Provision a one-click service in the current org/project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.orgID()
			if err != nil {
				return err
			}
			projID, err := a.projectID()
			if err != nil {
				return err
			}
			svc, err := a.client.CreateService(ctx(cmd), orgID, projID, client.CreateServiceInput{
				TemplateKey: args[0],
				Name:        name,
				CPU:         cpu,
				MemoryMB:    memMB,
			})
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), svc, func() {
				table(cmd.OutOrStdout(),
					[]string{"ID", "NAME", "TEMPLATE", "STATUS", "CPU", "MEM(MB)", "HOST"},
					[][]string{{svc.ID, svc.Name, svc.Template, svc.Status,
						fmt.Sprintf("%.2f", svc.CPU), strconv.Itoa(svc.MemoryMB), dash(svc.Host)}})
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "service instance name")
	f.Float64Var(&cpu, "cpu", 0, "requested vCPU (server default when 0)")
	f.IntVar(&memMB, "memory", 0, "requested memory in MB (server default when 0)")
	return cmd
}
