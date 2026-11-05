package cmd

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/adityapratamanusantara/fantastic/internal/bootstrap"
)

// apiCommand serves the HTTP API.
func apiCommand() *cli.Command {
	return &cli.Command{
		Name:   "api",
		Usage:  "run the HTTP API server",
		Action: runAPI,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    flagDatabaseURL,
				Usage:   "database connection url (overrides config)",
				Sources: cli.EnvVars(envDatabaseURL),
			},
		},
	}
}

func runAPI(ctx context.Context, cmd *cli.Command) error {
	log := newLogger()

	cfg, err := loadConfig(cmd, log)
	if err != nil {
		return err
	}
	if v := databaseURL(cmd, cfg); v != "" {
		cfg.Database.URL = v
	}

	srv, err := bootstrap.New(cfg, log)
	if err != nil {
		return err
	}
	return srv.Start()
}
