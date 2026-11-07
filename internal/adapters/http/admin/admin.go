// Package admin serves the static agent-admin page (no build step).
// The page is a thin client over the agent management API: authorization
// stays enforced by the API (RequireAdmin), the page only shows the
// "admin role required" banner when a call is rejected.
package admin

import (
	"embed"
	"net/http"

	"github.com/labstack/echo/v5"
)

//go:embed index.html
var files embed.FS

// Register mounts the admin page.
func Register(e *echo.Echo) {
	e.GET("/admin", func(c *echo.Context) error {
		b, err := files.ReadFile("index.html")
		if err != nil {
			return echo.ErrNotFound
		}
		return c.HTML(http.StatusOK, string(b))
	})
}
