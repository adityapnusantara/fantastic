// Package health serves the liveness endpoint.
package health

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// New returns a handler that reports service liveness.
func New() echo.HandlerFunc {
	return func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "up"})
	}
}
