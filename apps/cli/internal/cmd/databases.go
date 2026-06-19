package cmd

import (
	"fmt"
	"strconv"

	"github.com/mohamed3laa33/viro-coolify/apps/cli/internal/client"
	"github.com/spf13/cobra"
)

func (a *App) newDatabasesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "databases",
		Aliases: []string{"database", "db"},
		Short:   "Manage managed databases",
	}
	cmd.AddCommand(
		a.newDatabasesListCmd(),
		a.newDatabasesCreateCmd(),
		a.newDatabasesGetCmd(),
		a.newDatabaseActionCmd("deploy", "Deploy (provision) a database", func(cmd *cobra.Command, o, id string) (*client.Database, error) {
			return a.client.DeployDatabase(ctx(cmd), o, id)
		}),
		a.newDatabaseActionCmd("start", "Start a database", func(cmd *cobra.Command, o, id string) (*client.Database, error) {
			return a.client.DeployDatabase(ctx(cmd), o, id)
		}),
		a.newDatabaseActionCmd("stop", "Stop a database", func(cmd *cobra.Command, o, id string) (*client.Database, error) {
			return a.client.StopDatabase(ctx(cmd), o, id)
		}),
		a.newDatabaseActionCmd("restart", "Restart a database", func(cmd *cobra.Command, o, id string) (*client.Database, error) {
			return a.client.RestartDatabase(ctx(cmd), o, id)
		}),
		a.newDatabasesDeleteCmd(),
	)
	return cmd
}

func (a *App) newDatabasesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed databases in the current organization",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(cmd)
			if err != nil {
				return err
			}
			dbs, err := a.client.ListDatabases(ctx(cmd), orgID)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), dbs, func() {
				rows := make([][]string, 0, len(dbs))
				for _, d := range dbs {
					rows = append(rows, []string{d.ID, d.Name, d.Engine, d.Status,
						strconv.Itoa(d.StorageGB)})
				}
				table(cmd.OutOrStdout(), []string{"ID", "NAME", "ENGINE", "STATUS", "STORAGE(GB)"}, rows)
			})
		},
	}
}

func (a *App) newDatabasesCreateCmd() *cobra.Command {
	var (
		engine    string
		cpu       float64
		memMB     int
		storageGB int
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a managed database in the current org/project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(cmd)
			if err != nil {
				return err
			}
			in := client.CreateDatabaseInput{
				Name:      args[0],
				Engine:    engine,
				CPU:       cpu,
				MemoryMB:  memMB,
				StorageGB: storageGB,
			}
			if a.projectFlag != "" || a.cfg.CurrentProj != "" {
				if in.ProjectID, err = a.resolveProjectID(cmd, orgID); err != nil {
					return err
				}
			}
			db, err := a.client.CreateDatabase(ctx(cmd), orgID, in)
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), db, func() {
				table(cmd.OutOrStdout(), []string{"ID", "NAME", "ENGINE", "STATUS"},
					[][]string{{db.ID, db.Name, db.Engine, db.Status}})
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&engine, "engine", "", "database engine (postgresql, mysql, redis, mongodb)")
	f.Float64Var(&cpu, "cpu", 0, "requested vCPU (server default when 0)")
	f.IntVar(&memMB, "memory", 0, "requested memory in MB (server default when 0)")
	f.IntVar(&storageGB, "storage", 0, "requested storage in GB (server default when 0)")
	return cmd
}

func (a *App) newDatabasesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <database-id>",
		Aliases: []string{"info", "show"},
		Short:   "Show a database and its connection info (host/port/db/user/password)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(cmd)
			if err != nil {
				return err
			}
			d, err := a.client.GetDatabase(ctx(cmd), orgID, args[0])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), d, func() {
				c := d.Connection
				rows := [][]string{
					{"ID", d.ID},
					{"Name", d.Name},
					{"Engine", d.Engine},
					{"Status", d.Status},
					{"Host", dash(c.Host)},
					{"Port", strconv.Itoa(c.Port)},
					{"Database", dash(c.Database)},
					{"Username", dash(c.Username)},
					{"Password", dash(c.Password)},
					{"Connection string", dash(c.ConnectionString)},
				}
				table(cmd.OutOrStdout(), []string{"FIELD", "VALUE"}, rows)
			})
		},
	}
}

func (a *App) newDatabaseActionCmd(use, short string, fn func(cmd *cobra.Command, orgID, dbID string) (*client.Database, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <database-id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(cmd)
			if err != nil {
				return err
			}
			db, err := fn(cmd, orgID, args[0])
			if err != nil {
				return err
			}
			return a.emit(cmd.OutOrStdout(), db, func() {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: status %s\n", db.Name, db.Status)
			})
		},
	}
}

func (a *App) newDatabasesDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <database-id>",
		Aliases: []string{"destroy", "rm"},
		Short:   "Delete a managed database",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requireAuth(); err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(cmd)
			if err != nil {
				return err
			}
			if !yes {
				ans := prompt(fmt.Sprintf("Delete database %s? This cannot be undone. [y/N]: ", args[0]))
				if ans != "y" && ans != "Y" && ans != "yes" {
					fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
					return nil
				}
			}
			if err := a.client.DeleteDatabase(ctx(cmd), orgID, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted database %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	return cmd
}
