package migrations

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Up brings the database schema up to date. The connection URL may use
// postgres:// (rewritten to the migrate pgx5 driver automatically).
func Up(databaseURL string, log *slog.Logger) error {
	m, err := newMigrate(databaseURL)
	if err != nil {
		return err
	}
	defer closeMigrate(m, log)

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("run migrations: %w", err)
	}
	log.Info("migrations applied")
	return nil
}

// Down steps the schema down N migrations (for development resets).
func Down(databaseURL string, steps int, log *slog.Logger) error {
	m, err := newMigrate(databaseURL)
	if err != nil {
		return err
	}
	defer closeMigrate(m, log)

	if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("rollback migrations: %w", err)
	}
	log.Info("migrations rolled back", slog.Int("steps", steps))
	return nil
}

func newMigrate(databaseURL string) (*migrate.Migrate, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("database url is empty; set DATABASE_URL or config database.url")
	}
	sub, err := Sub()
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	src, err := iofs.New(sub, ".")
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, pgxDriverURL(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("init migrate: %w", err)
	}
	return m, nil
}

func closeMigrate(m *migrate.Migrate, log *slog.Logger) {
	if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
		log.Warn("migrate close", slog.Any("source", srcErr), slog.Any("db", dbErr))
	}
}

// pgxDriverURL rewrites postgres:// URLs to the pgx5:// scheme expected by
// the embedded migrate driver.
func pgxDriverURL(url string) string {
	return strings.Replace(url, "postgres://", "pgx5://", 1)
}
