// Package middleware provides echo HTTP middlewares.
package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/response"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// principalKey is the echo context key holding the verified Principal.
const principalKey = "fantastic.principal"

// TokenVerifier validates raw bearer tokens into principals.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (domain.Principal, error)
}

// BearerAuth rejects requests without a valid Casdoor bearer token before
// any business logic runs, and stores the verified Principal in the echo
// context (retrieve with PrincipalFrom).
func BearerAuth(v TokenVerifier, log *slog.Logger) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			header := c.Request().Header.Get("Authorization")
			token, ok := strings.CutPrefix(header, "Bearer ")
			if !ok || strings.TrimSpace(token) == "" {
				return unauthorized(c)
			}

			p, err := v.Verify(c.Request().Context(), token)
			if err != nil {
				// The reason is logged but never revealed to the caller.
				log.Warn("token rejected", slog.String("reason", err.Error()))
				return unauthorized(c)
			}
			c.Set(principalKey, p)
			return next(c)
		}
	}
}

// PrincipalFrom returns the verified Principal stored by BearerAuth.
func PrincipalFrom(c *echo.Context) (domain.Principal, bool) {
	p, ok := c.Get(principalKey).(domain.Principal)
	if !ok || p.IsZero() {
		return domain.Principal{}, false
	}
	return p, true
}

// unauthorized renders the uniform 401 envelope with a Bearer challenge.
func unauthorized(c *echo.Context) error {
	c.Response().Header().Set("WWW-Authenticate", "Bearer")
	return response.Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
}
