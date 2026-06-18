package cmd

import (
	"fmt"

	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/client"
	"github.com/spf13/cobra"
)

// newAppsDomainsCmd is the `vortex apps domains ...` group: add/verify/list/remove
// custom domains on an app, surfacing the DNS instructions on add/verify.
func (a *App) newAppsDomainsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "domains",
		Aliases: []string{"domain"},
		Short:   "Manage an app's custom domains",
	}
	cmd.AddCommand(
		a.newDomainsListCmd(),
		a.newDomainsAddCmd(),
		a.newDomainsVerifyCmd(),
		a.newDomainsRemoveCmd(),
	)
	return cmd
}

func (a *App) newDomainsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <app>",
		Short: "List an app's custom domains",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(cmd)
			if err != nil {
				return err
			}
			appID, err := a.resolveAppID(cmd, orgID, args[0])
			if err != nil {
				return err
			}
			domains, err := a.client.ListDomains(ctx(cmd), orgID, appID)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), domains, func() {
				rows := make([][]string, 0, len(domains))
				for _, d := range domains {
					rows = append(rows, []string{d.ID, d.Domain, dash(d.Status)})
				}
				table(cmd.OutOrStdout(), []string{"ID", "DOMAIN", "STATUS"}, rows)
			})
		},
	}
}

func (a *App) newDomainsAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <app> <domain>",
		Short: "Attach a custom domain and print the DNS records to publish",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(cmd)
			if err != nil {
				return err
			}
			appID, err := a.resolveAppID(cmd, orgID, args[0])
			if err != nil {
				return err
			}
			res, err := a.client.AddDomain(ctx(cmd), orgID, appID, args[1])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func() { printDomainResult(cmd, res) })
		},
	}
}

func (a *App) newDomainsVerifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "verify <app> <domain-id>",
		Short: "Verify domain ownership via the DNS TXT challenge",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(cmd)
			if err != nil {
				return err
			}
			appID, err := a.resolveAppID(cmd, orgID, args[0])
			if err != nil {
				return err
			}
			res, err := a.client.VerifyDomain(ctx(cmd), orgID, appID, args[1])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), res, func() {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: status %s\n", res.Domain.Domain, dash(res.Domain.Status))
				if res.Domain.Status != "verified" {
					printDomainResult(cmd, res)
				}
			})
		},
	}
}

func (a *App) newDomainsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <app> <domain-id>",
		Aliases: []string{"rm", "delete"},
		Short:   "Detach a custom domain from an app",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(cmd)
			if err != nil {
				return err
			}
			appID, err := a.resolveAppID(cmd, orgID, args[0])
			if err != nil {
				return err
			}
			if err := a.client.RemoveDomain(ctx(cmd), orgID, appID, args[1]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed domain %s from app %s\n", args[1], args[0])
			return nil
		},
	}
}

// printDomainResult prints the DNS records the user must publish: the TXT
// ownership challenge and the A/CNAME routing target.
func printDomainResult(cmd *cobra.Command, res *client.DomainResult) {
	ins := res.Instructions
	rows := [][]string{
		{"Domain ID", res.Domain.ID},
		{"Domain", res.Domain.Domain},
		{"Status", dash(res.Domain.Status)},
		{"TXT name", dash(ins.TXTName)},
		{"TXT value", dash(ins.TXTValue)},
		{ins.TargetType + " target", dash(ins.TargetValue)},
	}
	table(cmd.OutOrStdout(), []string{"FIELD", "VALUE"}, rows)
	fmt.Fprintln(cmd.OutOrStdout(),
		"\nPublish the TXT record to prove ownership, then point your domain at the target above.")
}
