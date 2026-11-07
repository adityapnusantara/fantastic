package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/middleware"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

func newAdminApp(allowed []string, p domain.Principal, withAuth bool) *echo.Echo {
	e := echo.New()
	handler := func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"user_id": p.UserID})
	}
	var middlewares []echo.MiddlewareFunc
	if withAuth {
		middlewares = append(middlewares, middleware.BearerAuth(fakeVerifier{p: p}, discardLogger()))
	}
	middlewares = append(middlewares, middleware.RequireAdmin(allowed))
	e.GET("/api/v1/agents", handler, middlewares...)
	return e
}

func adminBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func TestRequireAdminPassesMatchingRole(t *testing.T) {
	e := newAdminApp([]string{"fantastic-admin"}, domain.Principal{
		UserID: "aditya",
		Roles:  []string{"fantastic-admin"},
	}, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := adminBody(t, rec)
	assert.Equal(t, "aditya", body["user_id"])
}

func TestRequireAdminRejectsForeignRole(t *testing.T) {
	e := newAdminApp([]string{"fantastic-admin"}, domain.Principal{
		UserID: "aditya",
		Roles:  []string{"fantastic-viewer"},
	}, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	body := adminBody(t, rec)
	assert.Equal(t, "forbidden", body["code"])
	assert.Equal(t, "admin role required", body["message"])
}

func TestRequireAdminRejectsPrincipalWithoutRoles(t *testing.T) {
	e := newAdminApp([]string{"fantastic-admin"}, domain.Principal{UserID: "aditya"}, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequireAdminFailsClosedOnEmptyAllowedList(t *testing.T) {
	e := newAdminApp(nil, domain.Principal{
		UserID: "aditya",
		Roles:  []string{"fantastic-admin"},
	}, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequireAdminMatchesRolesCaseInsensitively(t *testing.T) {
	e := newAdminApp([]string{"Fantastic-Admin"}, domain.Principal{
		UserID: "aditya",
		Roles:  []string{"fantastic-admin"},
	}, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireAdminFailsClosedWithoutPrincipal(t *testing.T) {
	e := newAdminApp([]string{"fantastic-admin"}, domain.Principal{}, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}
