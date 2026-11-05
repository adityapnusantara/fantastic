package bootstrap

import (
	"log/slog"

	"github.com/adityapratamanusantara/fantastic/migrations"
)

// Migrate brings the database schema up to date.
func Migrate(databaseURL string, log *slog.Logger) error {
	return migrations.Up(databaseURL, log)
}

// Rollback steps the schema down N migrations (development reset).
func Rollback(databaseURL string, steps int, log *slog.Logger) error {
	return migrations.Down(databaseURL, steps, log)
}
