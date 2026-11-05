package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/middleware"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// fakeVerifier returns the configured principal or error.
type fakeVerifier struct {
	p   domain.Principal
	err error
}

func (f fakeVerifier) Verify(context.Context, string) (domain.Principal, error) {
	return f.p, f.err
}

func newApp(v middleware.TokenVerifier) *echo.Echo {
	e := echo.New()
	e.GET("/api/v1/whoami", func(c *echo.Context) error {
		p, ok := middleware.PrincipalFrom(c)
		if !ok {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "no principal"})
		}
		return c.JSON(http.StatusOK, map[string]string{"user_id": p.UserID, "org": p.Organization})
	}, middleware.BearerAuth(v, discardLogger()))
	return e
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBearerAuthPassesPrincipal(t *testing.T) {
	e := newApp(fakeVerifier{p: domain.Principal{UserID: "aditya", Organization: "pribadi"}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "aditya", body["user_id"])
	assert.Equal(t, "pribadi", body["org"])
}

func TestBearerAuthRejectsMissingHeader(t *testing.T) {
	e := newApp(fakeVerifier{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"))
}

func TestBearerAuthRejectsNonBearerScheme(t *testing.T) {
	e := newApp(fakeVerifier{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	req.Header.Set("Authorization", "Basic abc")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestBearerAuthRejectsBadTokenUniformly(t *testing.T) {
	e := newApp(fakeVerifier{err: errors.New("token type \"refresh-token\" is not an access token")})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer whatever")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.NotContains(t, rec.Body.String(), "refresh-token", "failure reason must not leak to the caller")
}
