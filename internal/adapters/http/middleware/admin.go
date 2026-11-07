package middleware

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/response"
)

// RequireAdmin rejects requests whose verified Principal carries none of
// the allowed roles with a uniform 403. Mount it after BearerAuth; an
// empty or absent principal and an empty allowed list all fail closed.
func RequireAdmin(allowed []string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			p, ok := PrincipalFrom(c)
			if !ok || !p.HasAnyRole(allowed...) {
				return response.Fail(c, http.StatusForbidden, "forbidden", "admin role required")
			}
			return next(c)
		}
	}
}
