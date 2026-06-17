package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (a *App) newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication (signup, login, logout, whoami)",
	}
	cmd.AddCommand(a.newSignupCmd(), a.newLoginCmd(), a.newLogoutCmd(), a.newWhoamiCmd())
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
	var email, password string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Vortex",
		RunE: func(cmd *cobra.Command, _ []string) error {
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
	fd := int(syscall.Stdin)
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
