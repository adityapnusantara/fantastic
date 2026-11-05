// Package demo serves the static demo page (no build step).
package demo

import (
	"embed"
	"net/http"

	"github.com/labstack/echo/v5"
)

//go:embed demo.html
var files embed.FS

// Register mounts the demo page.
func Register(e *echo.Echo) {
	e.GET("/demo", func(c *echo.Context) error {
		b, err := files.ReadFile("demo.html")
		if err != nil {
			return echo.ErrNotFound
		}
		return c.HTML(http.StatusOK, string(b))
	})
}
