package admin_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/admin"
)

// TestAdminPageServes asserts the page contract the admin flow relies on:
// GET /admin answers 200 text/html with a non-empty body (the embedded
// index.html), without any authentication — the API enforces authz, the
// page is a public shell.
func TestAdminPageServes(t *testing.T) {
	e := echo.New()
	admin.Register(e)

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	body := rec.Body.String()
	require.NotEmpty(t, strings.TrimSpace(body), "the embedded page must not be empty")
	assert.Contains(t, body, "<!DOCTYPE html>")
	// The page is self-contained: login, table and form live in this one file.
	assert.Contains(t, body, "/api/v1/auth/token")
	assert.Contains(t, body, "/api/v1/agents")
	assert.Contains(t, body, "/api/v1/agent-tools")
}

// TestAdminPageDoesNotShadowAPI asserts /admin serves the page only;
// API paths stay untouched by this package.
func TestAdminPageDoesNotShadowAPI(t *testing.T) {
	e := echo.New()
	admin.Register(e)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
