// Package migrations embeds the SQL migration files for single-binary
// delivery (`fantastic migrate`).
package migrations

import (
	"embed"
	"io/fs"
)

// SQL holds every *.up.sql / *.down.sql file in this directory.
//
//go:embed *.sql
var SQL embed.FS

// Sub returns the filesystem rooted at the SQL files, ready for a
// golang-migrate iofs source driver.
func Sub() (fs.FS, error) {
	return fs.Sub(SQL, ".")
}
