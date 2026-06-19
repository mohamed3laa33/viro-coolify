package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/client"
	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (a *App) newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication (signup, login, logout, whoami, tokens)",
	}
	cmd.AddCommand(
		a.newSignupCmd(),
		a.newLoginCmd(),
		a.newLogoutCmd(),
		a.newWhoamiCmd(),
		a.newAuthTokenCmd(),
	)
	return cmd
}

func (a *App) newSignupCmd() *cobra.Command {
	var email, name, password string
	cmd := &cobra.Command{
		Use:   "signup",
		Short: "Create a new Vortex account",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if email == "" {
				email = prompt("Email: ")
			}
			if name == "" {
				name = prompt("Name: ")
			}
			if password == "" {
				var err error
				if password, err = promptPassword("Password: "); err != nil {
					return err
				}
			}
			res, err := a.client.Signup(ctx(cmd), email, name, password)
			if err != nil {
				return err
			}
			if err := a.cfg.SetTokens(res.AccessToken, res.RefreshToken); err != nil {
				return err
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), res.User)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Signed up and logged in as %s\n", res.User.Email)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "account email")
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&password, "password", "", "account password (prompted if omitted)")
	return cmd
}

func (a *App) newLoginCmd() *cobra.Command {
	var email, password, token string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Vortex (with email/password, or --token for a personal access token)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Non-interactive / CI: store a personal access token and verify it.
			if token != "" {
				if !strings.HasPrefix(token, config.PATPrefix) {
					return fmt.Errorf("a personal access token must start with %q", config.PATPrefix)
				}
				if err := a.cfg.SetPAT(token); err != nil {
					return err
				}
				// Rebuild the client so it picks up the freshly-stored PAT.
				a.client = client.New(a.cfg.APIURL, tokenStore{a.cfg})
				u, err := a.client.Me(ctx(cmd))
				if err != nil {
					return fmt.Errorf("token rejected: %w", err)
				}
				if a.jsonOut {
					return printJSON(cmd.OutOrStdout(), u)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Logged in as %s (via token)\n", u.Email)
				return nil
			}
			if email == "" {
				email = prompt("Email: ")
			}
			if password == "" {
				var err error
				if password, err = promptPassword("Password: "); err != nil {
					return err
				}
			}
			res, err := a.client.Login(ctx(cmd), email, password)
			if err != nil {
				return err
			}
			if err := a.cfg.SetTokens(res.AccessToken, res.RefreshToken); err != nil {
				return err
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), res.User)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Logged in as %s\n", res.User.Email)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "account email")
	cmd.Flags().StringVar(&password, "password", "", "account password (prompted if omitted)")
	cmd.Flags().StringVar(&token, "token", "", "log in non-interactively with a personal access token (vrt_...)")
	return cmd
}

func (a *App) newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out (clears stored tokens)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.cfg.Clear(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Logged out.")
			return nil
		},
	}
}

func (a *App) newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the currently authenticated user",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			u, err := a.client.Me(ctx(cmd))
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), u, func() {
				admin := "no"
				if u.IsAdmin {
					admin = "yes"
				}
				table(cmd.OutOrStdout(),
					[]string{"ID", "EMAIL", "NAME", "ADMIN"},
					[][]string{{u.ID, u.Email, dash(u.Name), admin}})
			})
		},
	}
}

// newAuthTokenCmd is the `vortex auth token ...` group: create/list/revoke
// personal access tokens (PATs) for non-interactive / CI use.
func (a *App) newAuthTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "token",
		Aliases: []string{"tokens", "pat"},
		Short:   "Manage personal access tokens",
	}
	cmd.AddCommand(a.newTokenCreateCmd(), a.newTokenListCmd(), a.newTokenRevokeCmd())
	return cmd
}

func (a *App) newTokenCreateCmd() *cobra.Command {
	var (
		scopes        []string
		expiresInDays int
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a personal access token (the secret is shown once)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			tok, err := a.client.CreateToken(ctx(cmd), client.CreateTokenInput{
				Name:          args[0],
				Scopes:        scopes,
				ExpiresInDays: expiresInDays,
			})
			if err != nil {
				return err
			}
			if a.jsonOut {
				return printJSON(cmd.OutOrStdout(), tok)
			}
			out := cmd.OutOrStdout()
			table(out, []string{"FIELD", "VALUE"}, [][]string{
				{"ID", tok.ID},
				{"Name", tok.Name},
				{"Prefix", dash(tok.Prefix)},
				{"Scopes", dash(strings.Join(tok.Scopes, ","))},
				{"Expires", tokenExpiry(tok)},
			})
			fmt.Fprintf(out, "\nToken (shown once — store it now):\n%s\n", tok.Token)
			fmt.Fprintf(out, "\nUse it with: vortex auth login --token %s\n", tok.Token)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&scopes, "scope", nil, "scope to grant (repeatable)")
	f.IntVar(&expiresInDays, "expires-in-days", 0, "expiry in days (0 = never expires)")
	return cmd
}

func (a *App) newTokenListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List your personal access tokens (never shows the secret)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			tokens, err := a.client.ListTokens(ctx(cmd))
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), tokens, func() {
				rows := make([][]string, 0, len(tokens))
				for _, t := range tokens {
					last := "-"
					if !t.LastUsedAt.IsZero() {
						last = t.LastUsedAt.Format("2006-01-02 15:04")
					}
					rows = append(rows, []string{
						t.ID, t.Name, dash(t.Prefix),
						dash(strings.Join(t.Scopes, ",")), tokenExpiry(&t), last,
					})
				}
				table(cmd.OutOrStdout(),
					[]string{"ID", "NAME", "PREFIX", "SCOPES", "EXPIRES", "LAST USED"}, rows)
			})
		},
	}
}

func (a *App) newTokenRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "revoke <token-id>",
		Aliases: []string{"delete", "rm"},
		Short:   "Revoke a personal access token",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			if err := a.client.RevokeToken(ctx(cmd), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Revoked token %s\n", args[0])
			return nil
		},
	}
}

// tokenExpiry renders a token's expiry, "never" when unset.
func tokenExpiry(t *client.ApiToken) string {
	if t.ExpiresAt.IsZero() {
		return "never"
	}
	return t.ExpiresAt.Format("2006-01-02")
}

// prompt reads a single trimmed line from stdin after printing a label.
func prompt(label string) string {
	fmt.Fprint(os.Stderr, label)
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		return strings.TrimSpace(sc.Text())
	}
	return ""
}

// promptPassword reads a password from the terminal without echoing it. When
// stdin is not a terminal (e.g. piped input), it falls back to a plain line.
func promptPassword(label string) (string, error) {
	fmt.Fprint(os.Stderr, label)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	return prompt(""), nil
}
