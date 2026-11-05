// Package cmd defines the fantastic command-line surface:
// `fantastic api` and `fantastic migrate`.
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/adityapratamanusantara/fantastic/config"
)

// ConfigPath is the CLI flag/env override for the config file location.
const (
	flagConfig      = "config"
	defaultConfig   = "config.yaml"
	envConfigPath   = "FANTASTIC_CONFIG"
	flagDatabaseURL = "database-url"
	envDatabaseURL  = "DATABASE_URL"
)

// Run executes the CLI with the given arguments and returns the process
// exit error (nil on success).
func Run(args []string) error {
	// Local DX: load .env from the working directory when present, so the
	// documented flow (docker compose up → migrate → api) works in a plain
	// shell. Already-set variables win.
	_ = loadDotEnv(".env")

	cmd := &cli.Command{
		Name:  "fantastic",
		Usage: "personal AI agent backend (eino + MCP + postgres)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    flagConfig,
				Usage:   "path to the YAML config file",
				Sources: cli.EnvVars(envConfigPath),
				Value:   defaultConfig,
			},
		},
		Commands: []*cli.Command{
			apiCommand(),
			migrateCommand(),
		},
	}
	return cmd.Run(context.Background(), args)
}

// loadConfig reads the config file selected by the --config flag and
// logs any environment-interpolation warnings.
func loadConfig(cmd *cli.Command, log *slog.Logger) (*config.Config, error) {
	path := cmd.String(flagConfig)
	cfg, warnings, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	for _, w := range warnings {
		log.Warn("config references unset environment variable", slog.String("var", w))
	}
	return cfg, nil
}

// databaseURL returns the effective database URL: --database-url flag or
// DATABASE_URL env first, then the config file value.
func databaseURL(cmd *cli.Command, cfg *config.Config) string {
	if v := cmd.String(flagDatabaseURL); v != "" {
		return v
	}
	if v := os.Getenv(envDatabaseURL); v != "" {
		return v
	}
	return cfg.Database.URL
}

// newLogger builds the process logger. Any error is fatal at startup.
func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// loadDotEnv parses a simple KEY=VALUE file and sets variables that are
// not already present in the environment.
func loadDotEnv(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil // no .env is normal
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
	return nil
}

// fatal prints the error and exits non-zero.
func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fantastic:", err)
	os.Exit(1)
}
