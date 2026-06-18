package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func (a *App) newPlansCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plans",
		Short: "List the billing plan catalog",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Public endpoint — no auth required.
			plans, err := a.client.Plans(ctx(cmd))
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), plans, func() {
				rows := make([][]string, 0, len(plans))
				for _, p := range plans {
					rows = append(rows, []string{
						p.ID,
						p.Name,
						money(p.PriceCents, p.Currency),
						strconv.Itoa(p.IncludedHours),
						fmt.Sprintf("%.2f", p.MaxCPU),
						strconv.Itoa(p.MaxMemoryMB),
						strconv.Itoa(p.MaxApps),
					})
				}
				table(cmd.OutOrStdout(),
					[]string{"ID", "NAME", "PRICE/MO", "HOURS", "MAXCPU", "MAXMEM(MB)", "MAXAPPS"}, rows)
			})
		},
	}
}

func (a *App) newPricingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pricing",
		Short: "List the hourly resource price list",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Public endpoint — no auth required.
			comps, err := a.client.Pricing(ctx(cmd))
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), comps, func() {
				rows := make([][]string, 0, len(comps))
				for _, p := range comps {
					rows = append(rows, []string{
						p.Key, p.Name, dash(p.Unit),
						fmt.Sprintf("%.4f", p.PricePerHour), strings.ToUpper(p.Currency),
					})
				}
				table(cmd.OutOrStdout(),
					[]string{"KEY", "NAME", "UNIT", "PRICE/HR", "CURRENCY"}, rows)
			})
		},
	}
}

// money formats cents in the given currency, e.g. 1500/"usd" -> "$15.00".
func money(cents int, currency string) string {
	sym := ""
	switch strings.ToLower(currency) {
	case "usd", "":
		sym = "$"
	case "eur":
		sym = "€"
	case "gbp":
		sym = "£"
	}
	v := fmt.Sprintf("%s%.2f", sym, float64(cents)/100)
	if sym == "" {
		v = fmt.Sprintf("%.2f %s", float64(cents)/100, strings.ToUpper(currency))
	}
	return v
}
