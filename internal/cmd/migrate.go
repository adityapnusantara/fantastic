package cmd

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/adityapratamanusantara/fantastic/internal/bootstrap"
)

// migrateCommand applies pending database migrations.
func migrateCommand() *cli.Command {
	return &cli.Command{
		Name:   "migrate",
		Usage:  "apply pending database migrations",
		Action: runMigrate,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    flagDatabaseURL,
				Usage:   "database connection url (overrides config)",
				Sources: cli.EnvVars(envDatabaseURL),
			},
			&cli.IntFlag{
				Name:  "down",
				Usage: "instead of migrating up, roll back N migration steps",
			},
		},
	}
}

func runMigrate(ctx context.Context, cmd *cli.Command) error {
	log := newLogger()

	cfg, err := loadConfig(cmd, log)
	if err != nil {
		return err
	}
	url := databaseURL(cmd, cfg)

	if steps := cmd.Int("down"); steps > 0 {
		if err := bootstrap.Rollback(url, steps, log); err != nil {
			return err
		}
		return nil
	}

	if err := bootstrap.Migrate(url, log); err != nil {
		return err
	}
	return nil
}
