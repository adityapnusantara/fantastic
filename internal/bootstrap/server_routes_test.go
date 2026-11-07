package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agenthttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/agent"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/agentadmin"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// Router-level proof (task 5.3) that the agent management routes and the
// agent-scoped chat route coexist on one echo instance: GET/POST/PATCH/
// DELETE /api/v1/agents/{id} (param name "id") and POST
// /api/v1/agents/{agent}/chat (param name "agent") are distinct routes
// served without conflict.

// stubAgentManager implements agenthttp.Manager; its results are never
// asserted, only that dispatch reaches the right handler.
type stubAgentManager struct{}

func (stubAgentManager) Create(context.Context, string, agentadmin.Input) (domain.Agent, error) {
	return domain.Agent{}, nil
}

func (stubAgentManager) Update(context.Context, string, agentadmin.Input) (domain.Agent, error) {
	return domain.Agent{}, nil
}

func (stubAgentManager) Delete(context.Context, string) error { return nil }

func (stubAgentManager) Get(context.Context, string) (domain.Agent, []string, error) {
	return domain.Agent{}, nil, nil
}

func (stubAgentManager) List(context.Context) ([]domain.Agent, map[string][]string, error) {
	return nil, nil, nil
}

func (stubAgentManager) ToolNames() []string { return nil }

func TestAgentAndChatRoutesServeDistinctPaths(t *testing.T) {
	e := echo.New()
	api := e.Group("/api/v1")

	// registered exactly as bootstrap mounts them
	agenthttp.Register(api, stubAgentManager{}, []string{"fantastic-admin"})
	chatHits := 0
	var chatAgentParam string
	api.POST("/agents/:agent/chat", func(c *echo.Context) error {
		chatHits++
		chatAgentParam = c.Param("agent")
		return c.NoContent(http.StatusOK)
	})

	routes := make(map[string]echo.RouteInfo, len(e.Router().Routes()))
	for _, r := range e.Router().Routes() {
		routes[r.Method+" "+r.Path] = r
	}

	want := map[string][]string{
		http.MethodGet + " /api/v1/agents":              nil,
		http.MethodGet + " /api/v1/agents/:id":          {"id"},
		http.MethodPost + " /api/v1/agents/:id":         {"id"},
		http.MethodPatch + " /api/v1/agents/:id":        {"id"},
		http.MethodDelete + " /api/v1/agents/:id":       {"id"},
		http.MethodPost + " /api/v1/agents/:agent/chat": {"agent"},
		// admin-only tool catalog listing feeding the admin page (task 7.2)
		http.MethodGet + " /api/v1/agent-tools": nil,
	}
	for key, params := range want {
		r, ok := routes[key]
		require.True(t, ok, "route %s must be registered", key)
		assert.Equal(t, params, r.Parameters, "route %s must bind the path params %v", key, params)
	}
	assert.Len(t, routes, len(want), "no extra /api/v1/agents routes may exist")

	// dispatch: the chat route serves POST /api/v1/agents/{agent}/chat and
	// binds the agent param (no auth — the handler here is a stub).
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/agents/general/chat", nil))
	require.Equal(t, http.StatusOK, rec.Code, "chat route must match; got status %d", rec.Code)
	assert.Equal(t, 1, chatHits)
	assert.Equal(t, "general", chatAgentParam)

	// dispatch: POST /api/v1/agents/{id} is a different route on the same
	// path prefix. Without a principal the admin gate answers 403 — proof
	// the route matched and carries RequireAdmin (a conflict would surface
	// as 404/405 or hit the chat handler instead).
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/agents/general", nil))
	assert.Equal(t, http.StatusForbidden, rec.Code, "create route must match and be admin-gated; got status %d", rec.Code)
	assert.Equal(t, 1, chatHits, "the create route must not dispatch into the chat handler")

	// dispatch: GET /agents/{id} likewise matched and admin-gated.
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/agents/general", nil))
	assert.Equal(t, http.StatusForbidden, rec.Code, "detail route must match and be admin-gated; got status %d", rec.Code)
	assert.Equal(t, 1, chatHits, "the detail route must not dispatch into the chat handler")
}
