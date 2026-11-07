package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/agent"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/middleware"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/agentadmin"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// fakeManager records every call so tests can assert what the handler
// sent to the use case, and can inject use-case-level errors.
type fakeManager struct {
	agents   map[string]domain.Agent
	order    []string
	degraded map[string][]string
	inUse    map[string]bool
	catalog  []string

	createErr, updateErr, deleteErr, getErr, listErr error

	creates map[string][]agentadmin.Input
	updates map[string][]agentadmin.Input
	deletes []string
}

func newFake(agents ...domain.Agent) *fakeManager {
	f := &fakeManager{
		agents:   map[string]domain.Agent{},
		degraded: map[string][]string{},
		inUse:    map[string]bool{},
		creates:  map[string][]agentadmin.Input{},
		updates:  map[string][]agentadmin.Input{},
	}
	for _, a := range agents {
		f.agents[a.ID] = a
		f.order = append(f.order, a.ID)
	}
	return f
}

func (f *fakeManager) Create(_ context.Context, id string, in agentadmin.Input) (domain.Agent, error) {
	f.creates[id] = append(f.creates[id], in)
	if f.createErr != nil {
		return domain.Agent{}, f.createErr
	}
	a := applyInput(domain.Agent{ID: id}, in)
	f.agents[id] = a
	f.order = append(f.order, id)
	return a, nil
}

func (f *fakeManager) Update(_ context.Context, id string, in agentadmin.Input) (domain.Agent, error) {
	if f.updateErr != nil {
		return domain.Agent{}, f.updateErr
	}
	cur, ok := f.agents[id]
	if !ok {
		return domain.Agent{}, domain.ErrNotFound
	}
	f.updates[id] = append(f.updates[id], in)
	a := applyInput(cur, in)
	f.agents[id] = a
	return a, nil
}

func (f *fakeManager) Delete(_ context.Context, id string) error {
	f.deletes = append(f.deletes, id)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.agents[id]; !ok {
		return domain.ErrNotFound
	}
	if f.inUse[id] {
		return fmt.Errorf("%w: agent %q still has conversations", domain.ErrAgentInUse, id)
	}
	delete(f.agents, id)
	return nil
}

func (f *fakeManager) Get(_ context.Context, id string) (domain.Agent, []string, error) {
	if f.getErr != nil {
		return domain.Agent{}, nil, f.getErr
	}
	a, ok := f.agents[id]
	if !ok {
		return domain.Agent{}, nil, domain.ErrNotFound
	}
	return a, f.degraded[id], nil
}

func (f *fakeManager) List(context.Context) ([]domain.Agent, map[string][]string, error) {
	if f.listErr != nil {
		return nil, nil, f.listErr
	}
	out := make([]domain.Agent, 0, len(f.order))
	for _, id := range f.order {
		if _, ok := f.agents[id]; ok {
			out = append(out, f.agents[id])
		}
	}
	return out, f.degraded, nil
}

// ToolNames mimics the catalog-backed listing: whatever the fake was
// given, sorted (the real service sorts the catalog keys).
func (f *fakeManager) ToolNames() []string {
	out := append([]string(nil), f.catalog...)
	sort.Strings(out)
	return out
}

// applyInput mimics what the store persists from an agentadmin.Input.
func applyInput(a domain.Agent, in agentadmin.Input) domain.Agent {
	a.DisplayName, a.Description, a.Icon = in.DisplayName, in.Description, in.Icon
	a.SystemPrompt = in.SystemPrompt
	a.Model, a.API, a.Temperature, a.ReasoningEffort = in.Model, in.API, in.Temperature, in.ReasoningEffort
	a.Tools = append([]string(nil), in.Tools...)
	a.Enabled, a.SortOrder = in.Enabled, in.SortOrder
	return a
}

type stubVerifier struct{ roles []string }

func (v stubVerifier) Verify(context.Context, string) (domain.Principal, error) {
	return domain.Principal{UserID: "alice", Organization: "pribadi", Roles: v.roles}, nil
}

func adminRoles() []string { return []string{"fantastic-admin"} }

func newApp(svc agent.Manager, roles []string) *echo.Echo {
	e := echo.New()
	g := e.Group("/api/v1", middleware.BearerAuth(stubVerifier{roles: roles}, discardLogger()))
	agent.Register(g, svc, adminRoles())
	return e
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func do(e *echo.Echo, method, path string) *httptest.ResponseRecorder {
	return doJSON(e, method, path, "")
}

func doJSON(e *echo.Echo, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func sampleAgent(id string) domain.Agent {
	model, api, effort := "gpt-5", "responses", "high"
	temp := float32(0.7)
	return domain.Agent{
		ID:              id,
		DisplayName:     "General",
		Description:     "the default agent",
		Icon:            "sparkles",
		SystemPrompt:    "you are helpful",
		Model:           &model,
		API:             &api,
		Temperature:     &temp,
		ReasoningEffort: &effort,
		Tools:           []string{"search_faq"},
		Enabled:         true,
		SortOrder:       10,
	}
}

func TestListOpenToPlainUsers(t *testing.T) {
	e := newApp(newFake(sampleAgent("general")), nil)

	rec := do(e, http.MethodGet, "/api/v1/agents")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "00", decode(t, rec)["code"])
}

func TestListIsMetadataOnly(t *testing.T) {
	fake := newFake(sampleAgent("general"))
	fake.degraded["general"] = []string{"mcp.search"}
	e := newApp(fake, adminRoles())

	rec := do(e, http.MethodGet, "/api/v1/agents")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Code string           `json:"code"`
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	row := body.Data[0]
	assert.Equal(t, "general", row["id"])
	assert.Equal(t, "General", row["display_name"])
	assert.Equal(t, true, row["enabled"])
	assert.Equal(t, float64(10), row["sort_order"])
	assert.Equal(t, []any{"mcp.search"}, row["degraded_tools"])
	for _, forbidden := range []string{"system_prompt", "tools", "model", "api", "temperature", "reasoning_effort", "overrides"} {
		assert.NotContains(t, row, forbidden, "the listing leaks no definition detail even to admins")
	}
}

func TestListRequiresAuthentication(t *testing.T) {
	e := echo.New()
	g := e.Group("/api/v1", middleware.BearerAuth(stubVerifier{}, discardLogger()))
	agent.Register(g, newFake(), adminRoles())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDetailForAdmin(t *testing.T) {
	e := newApp(newFake(sampleAgent("general")), adminRoles())

	rec := do(e, http.MethodGet, "/api/v1/agents/general")
	require.Equal(t, http.StatusOK, rec.Code)

	data, ok := decode(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "you are helpful", data["system_prompt"])
	assert.Equal(t, "gpt-5", data["model"])
	assert.Equal(t, "responses", data["api"])
	assert.Equal(t, 0.7, data["temperature"])
	assert.Equal(t, "high", data["reasoning_effort"])
	assert.Equal(t, []any{"search_faq"}, data["tools"])
}

func TestDetailOverridesNullWhenUnset(t *testing.T) {
	a := sampleAgent("plain")
	a.Model, a.API, a.Temperature, a.ReasoningEffort, a.Tools = nil, nil, nil, nil, nil
	e := newApp(newFake(a), adminRoles())

	rec := do(e, http.MethodGet, "/api/v1/agents/plain")
	require.Equal(t, http.StatusOK, rec.Code)

	data, ok := decode(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Nil(t, data["model"])
	assert.Nil(t, data["api"])
	assert.Nil(t, data["temperature"])
	assert.Nil(t, data["reasoning_effort"])
	assert.Equal(t, []any{}, data["tools"])
}

func TestDetailPlainUserIs403(t *testing.T) {
	e := newApp(newFake(sampleAgent("general")), nil)

	rec := do(e, http.MethodGet, "/api/v1/agents/general")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "forbidden", decode(t, rec)["code"])
}

func TestCreateForAdmin(t *testing.T) {
	fake := newFake()
	e := newApp(fake, adminRoles())

	body := `{"display_name":"FAQ","description":"answers faq","icon":"book","system_prompt":"be brief","tools":["search_faq"],"sort_order":5}`
	rec := doJSON(e, http.MethodPost, "/api/v1/agents/faq", body)
	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "faq", decode(t, rec)["data"].(map[string]any)["id"])

	require.Len(t, fake.creates["faq"], 1)
	in := fake.creates["faq"][0]
	assert.Equal(t, "FAQ", in.DisplayName)
	assert.Equal(t, "be brief", in.SystemPrompt)
	assert.Equal(t, []string{"search_faq"}, in.Tools)
	assert.Equal(t, 5, in.SortOrder)
	assert.True(t, in.Enabled, "enabled defaults to true")
	assert.Nil(t, in.Model, "absent overrides stay nil")
}

func TestCreateDefaultsOnEmptyBody(t *testing.T) {
	fake := newFake()
	e := newApp(fake, adminRoles())

	rec := doJSON(e, http.MethodPost, "/api/v1/agents/empty", `{}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	in := fake.creates["empty"][0]
	assert.True(t, in.Enabled)
	assert.Equal(t, 0, in.SortOrder)
	assert.Equal(t, "", in.DisplayName)
	assert.Empty(t, in.Tools)
}

func TestCreateTakenSlugIs409(t *testing.T) {
	fake := newFake()
	fake.createErr = fmt.Errorf("insert agent faq: %w", domain.ErrAgentExists)
	e := newApp(fake, adminRoles())

	rec := doJSON(e, http.MethodPost, "/api/v1/agents/faq", `{"system_prompt":"p"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "agent id already exists")
}

func TestCreateUnknownToolIs400NamingTheTool(t *testing.T) {
	fake := newFake()
	fake.createErr = &agentadmin.ValidationError{
		Field:   "tools",
		Message: fmt.Sprintf("unknown tool %q (assign tools from the runtime catalog)", "nope_tool"),
	}
	e := newApp(fake, adminRoles())

	rec := doJSON(e, http.MethodPost, "/api/v1/agents/faq", `{"system_prompt":"p","tools":["nope_tool"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	body := decode(t, rec)
	assert.Equal(t, "invalid_request", body["code"])
	assert.Contains(t, body["message"], "nope_tool", "the rejection names the offending tool")
}

func TestCreatePlainUserIs403(t *testing.T) {
	fake := newFake()
	e := newApp(fake, nil)

	rec := doJSON(e, http.MethodPost, "/api/v1/agents/faq", `{"system_prompt":"p"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, fake.creates, "no change applied")
}

func TestCreateInvalidBodyIs400(t *testing.T) {
	e := newApp(newFake(), adminRoles())

	rec := doJSON(e, http.MethodPost, "/api/v1/agents/faq", "{not json")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "invalid_request", decode(t, rec)["code"])
}

func TestPatchOverlaysOnlyProvidedFields(t *testing.T) {
	fake := newFake(sampleAgent("general"))
	e := newApp(fake, adminRoles())

	rec := doJSON(e, http.MethodPatch, "/api/v1/agents/general", `{"system_prompt":"new prompt"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Len(t, fake.updates["general"], 1)
	in := fake.updates["general"][0]
	assert.Equal(t, "new prompt", in.SystemPrompt)
	assert.Equal(t, "General", in.DisplayName, "fields absent from the body keep their value")
	assert.Equal(t, "the default agent", in.Description)
	assert.Equal(t, []string{"search_faq"}, in.Tools)
	require.NotNil(t, in.Model)
	assert.Equal(t, "gpt-5", *in.Model)
	assert.True(t, in.Enabled)
	assert.Equal(t, 10, in.SortOrder)
}

func TestPatchEnabledFalseReachesTheUseCase(t *testing.T) {
	// the disable contract: an explicit {"enabled": false} must reach the
	// use case as false — not be silently dropped by the overlay (the
	// 8.2 smoke run caught exactly that).
	fake := newFake(sampleAgent("general"))
	e := newApp(fake, adminRoles())

	rec := doJSON(e, http.MethodPatch, "/api/v1/agents/general", `{"enabled":false}`)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Len(t, fake.updates["general"], 1)
	in := fake.updates["general"][0]
	assert.False(t, in.Enabled, "PATCH enabled:false must disable, not be dropped")
	assert.Equal(t, "the default agent", in.Description, "fields absent from the body keep their value")
}

func TestPatchUnknownAgentIs404(t *testing.T) {
	e := newApp(newFake(), adminRoles())

	rec := doJSON(e, http.MethodPatch, "/api/v1/agents/ghost", `{"system_prompt":"p"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "not_found", decode(t, rec)["code"])
}

func TestPatchPlainUserIs403(t *testing.T) {
	fake := newFake(sampleAgent("general"))
	e := newApp(fake, nil)

	rec := doJSON(e, http.MethodPatch, "/api/v1/agents/general", `{"system_prompt":"nope"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, fake.updates, "no change applied")
}

func TestDeleteWithConversationsIs409(t *testing.T) {
	fake := newFake(sampleAgent("general"))
	fake.inUse["general"] = true
	e := newApp(fake, adminRoles())

	rec := do(e, http.MethodDelete, "/api/v1/agents/general")
	require.Equal(t, http.StatusConflict, rec.Code)

	body := decode(t, rec)
	assert.Equal(t, "conflict", body["code"])
	assert.Contains(t, body["message"], "enabled: false", "the rejection suggests disabling instead")
	assert.Contains(t, fake.agents, "general", "the agent remains stored")
}

func TestDeleteForAdmin(t *testing.T) {
	fake := newFake(sampleAgent("general"))
	e := newApp(fake, adminRoles())

	rec := do(e, http.MethodDelete, "/api/v1/agents/general")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "00", decode(t, rec)["code"])
	assert.Contains(t, fake.deletes, "general")
	assert.NotContains(t, fake.agents, "general")
}

func TestDeleteUnknownAgentIs404(t *testing.T) {
	e := newApp(newFake(), adminRoles())

	rec := do(e, http.MethodDelete, "/api/v1/agents/ghost")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "not_found", decode(t, rec)["code"])
}

func TestToolCatalogForAdminIsSorted(t *testing.T) {
	fake := newFake()
	fake.catalog = []string{"web.search", "search_faq", "mcp.alpha.ping"}
	e := newApp(fake, adminRoles())

	rec := do(e, http.MethodGet, "/api/v1/agent-tools")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Code string   `json:"code"`
		Data []string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "00", body.Code)
	assert.Equal(t, []string{"mcp.alpha.ping", "search_faq", "web.search"}, body.Data,
		"the catalog listing is sorted so the admin page's checkboxes are stable")
}

func TestToolCatalogPlainUserIs403(t *testing.T) {
	e := newApp(newFake(), nil)

	rec := do(e, http.MethodGet, "/api/v1/agent-tools")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "forbidden", decode(t, rec)["code"])
}

func TestToolCatalogRequiresAuthentication(t *testing.T) {
	e := echo.New()
	g := e.Group("/api/v1", middleware.BearerAuth(stubVerifier{}, discardLogger()))
	agent.Register(g, newFake(), adminRoles())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-tools", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
