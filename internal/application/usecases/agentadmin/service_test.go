package agentadmin_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloudwego/eino/components/tool"

	chatusecase "github.com/adityapratamanusantara/fantastic/internal/application/usecases/chat"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/conversation/mocks"

	"github.com/adityapratamanusantara/fantastic/internal/application/agentregistry"
	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/agentadmin"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// testCatalog is the runtime tool catalog the usecase validates tool
// assignments against. Values are never invoked, only looked up.
var testCatalog = map[string]tool.BaseTool{
	"search_faq": nil,
	"web.search": nil,
}

// testTime is the fake database clock the fake agent store stamps rows
// with, mimicking "timestamps are assigned by the database".
var testTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// fakeAgentStore is an in-memory driven.AgentStore. It records calls for
// assertions and mimics the store contract: unknown id -> ErrNotFound,
// Create stamps timestamps, Update keeps the original created_at.
type fakeAgentStore struct {
	rows map[string]domain.Agent

	createCalls    []string
	updateCalls    []domain.Agent
	setEnableds    []setEnabledCall
	deleteCalls    []string
	countByAgentID []string // ids the conversation store was asked about

	events *[]string // shared write/rebuild operation log for order assertions
}

type setEnabledCall struct {
	id      string
	enabled bool
}

func newFakeAgentStore(events *[]string) *fakeAgentStore {
	return &fakeAgentStore{rows: make(map[string]domain.Agent), events: events}
}

func (f *fakeAgentStore) record(event string) {
	if f.events != nil {
		*f.events = append(*f.events, event)
	}
}

func (f *fakeAgentStore) List(ctx context.Context) ([]domain.Agent, error) {
	// Deliberately return rows in reverse (sort_order, id) order so the
	// test proves the usecase sorts, not the fake.
	out := make([]domain.Agent, 0, len(f.rows))
	for _, a := range f.rows {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder > out[j].SortOrder
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (f *fakeAgentStore) Get(ctx context.Context, id string) (*domain.Agent, error) {
	a, ok := f.rows[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := a
	return &cp, nil
}

func (f *fakeAgentStore) Create(ctx context.Context, agent *domain.Agent) error {
	f.record("create:" + agent.ID)
	if _, exists := f.rows[agent.ID]; exists {
		return fmt.Errorf("duplicate agent id %q", agent.ID)
	}
	stored := *agent
	stored.CreatedAt = testTime
	stored.UpdatedAt = testTime
	f.rows[agent.ID] = stored
	f.createCalls = append(f.createCalls, agent.ID)
	return nil
}

func (f *fakeAgentStore) CreateIfAbsent(ctx context.Context, agent *domain.Agent) (bool, error) {
	if _, exists := f.rows[agent.ID]; exists {
		return false, nil
	}
	f.rows[agent.ID] = *agent
	return true, nil
}

func (f *fakeAgentStore) Update(ctx context.Context, agent *domain.Agent) error {
	f.record("update:" + agent.ID)
	prev, ok := f.rows[agent.ID]
	if !ok {
		return domain.ErrNotFound
	}
	stored := *agent
	stored.CreatedAt = prev.CreatedAt // created_at is never rewritten
	stored.UpdatedAt = testTime
	f.rows[agent.ID] = stored
	f.updateCalls = append(f.updateCalls, *agent)
	return nil
}

func (f *fakeAgentStore) SetEnabled(ctx context.Context, id string, enabled bool) error {
	f.record("setenabled:" + id)
	a, ok := f.rows[id]
	if !ok {
		return domain.ErrNotFound
	}
	a.Enabled = enabled
	a.UpdatedAt = testTime
	f.rows[id] = a
	f.setEnableds = append(f.setEnableds, setEnabledCall{id: id, enabled: enabled})
	return nil
}

func (f *fakeAgentStore) Delete(ctx context.Context, id string) error {
	f.record("delete:" + id)
	if _, ok := f.rows[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.rows, id)
	f.deleteCalls = append(f.deleteCalls, id)
	return nil
}

var _ driven.AgentStore = (*fakeAgentStore)(nil)

// recordingFactory stands in for bootstrap's registry Factory: it logs
// every agent it is asked to build (into the shared event log, so tests
// can assert store-write-before-rebuild ordering) and can be told to
// degrade or fail specific agent ids.
type recordingFactory struct {
	built   []domain.Agent
	degrade map[string][]string
	fail    map[string]error
	events  *[]string
}

func (f *recordingFactory) build(a domain.Agent) (agentregistry.BuildResult, error) {
	f.built = append(f.built, a)
	if f.events != nil {
		*f.events = append(*f.events, "rebuild:"+a.ID)
	}
	if err, ok := f.fail[a.ID]; ok {
		return agentregistry.BuildResult{}, err
	}
	return agentregistry.BuildResult{
		Service:       &chatusecase.Service{},
		DegradedTools: f.degrade[a.ID],
	}, nil
}

// harness wires the service with its fakes.
type harness struct {
	service   *agentadmin.Service
	store     *fakeAgentStore
	registry  *agentregistry.Registry
	convs     *mocks.MockConversationStore
	factory   *recordingFactory
	askedFor  *[]string // agent ids passed to CountByAgent
	count     int64     // what CountByAgent answers
	countErr  error     // what CountByAgent fails with
	eventsLog []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	h := &harness{askedFor: &[]string{}}
	h.store = newFakeAgentStore(&h.eventsLog)
	h.factory = &recordingFactory{
		degrade: make(map[string][]string),
		fail:    make(map[string]error),
		events:  &h.eventsLog,
	}
	h.registry = agentregistry.New(h.factory.build)
	h.convs = &mocks.MockConversationStore{}
	h.convs.CountByAgentFn = func(ctx context.Context, agentID string) (int64, error) {
		*h.askedFor = append(*h.askedFor, agentID)
		return h.count, h.countErr
	}

	svc, err := agentadmin.NewService(h.store, h.registry, testCatalog, h.convs)
	require.NoError(t, err)
	h.service = svc
	return h
}

func validInput() agentadmin.Input {
	return agentadmin.Input{
		DisplayName:  "Helper",
		Description:  "an agent used by tests",
		Icon:         "sparkles",
		SystemPrompt: "You are helpful.",
		Tools:        []string{"search_faq", "web.search"},
		Enabled:      true,
		SortOrder:    1,
	}
}

func TestCreateDisabledStaysOutOfRegistry(t *testing.T) {
	h := newHarness(t)
	in := validInput()
	in.Enabled = false

	created, err := h.service.Create(context.Background(), "offline", in)
	require.NoError(t, err)
	assert.False(t, created.Enabled)

	_, ok := h.registry.Get("offline")
	assert.False(t, ok, "a disabled agent must never enter the registry — it must not serve chat")
}

func TestUpdateDisableRemovesFromRegistry(t *testing.T) {
	h := newHarness(t)
	_, err := h.service.Create(context.Background(), "faq", validInput())
	require.NoError(t, err)
	_, ok := h.registry.Get("faq")
	require.True(t, ok, "precondition: enabled agent serves chat")

	in := validInput()
	in.Enabled = false
	before := len(h.eventsLog)
	_, err = h.service.Update(context.Background(), "faq", in)
	require.NoError(t, err)

	_, ok = h.registry.Get("faq")
	assert.False(t, ok, "PATCH enabled:false must stop chat exactly like SetEnabled")
	assert.Equal(t, []string{"update:faq"}, h.eventsLog[before:],
		"the disable path must only write the store — no rebuild after the disable")
}

func TestNewServiceRejectsNilDeps(t *testing.T) {
	convs := &mocks.MockConversationStore{}
	cases := []struct {
		name  string
		build func() (*agentadmin.Service, error)
	}{
		{"nil store", func() (*agentadmin.Service, error) {
			return agentadmin.NewService(nil, agentregistry.New(nil), testCatalog, convs)
		}},
		{"nil registry", func() (*agentadmin.Service, error) {
			return agentadmin.NewService(newFakeAgentStore(nil), nil, testCatalog, convs)
		}},
		{"nil catalog", func() (*agentadmin.Service, error) {
			return agentadmin.NewService(newFakeAgentStore(nil), agentregistry.New(nil), nil, convs)
		}},
		{"nil conversation store", func() (*agentadmin.Service, error) {
			return agentadmin.NewService(newFakeAgentStore(nil), agentregistry.New(nil), testCatalog, nil)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.build()
			require.Error(t, err)
			assert.NotErrorIs(t, err, domain.ErrNotFound)
		})
	}
}

func TestCreateValidationMatrix(t *testing.T) {
	cases := []struct {
		name        string
		id          string
		mutate      func(*agentadmin.Input)
		wantField   string
		wantMessage string // substring that must appear (names the offender)
	}{
		{"empty id", "", func(in *agentadmin.Input) {}, "id", `""`},
		{"uppercase id", "General", func(in *agentadmin.Input) {}, "id", "General"},
		{"id with space", "has space", func(in *agentadmin.Input) {}, "id", "has space"},
		{"id with underscore", "has_underscore", func(in *agentadmin.Input) {}, "id", "has_underscore"},
		{"id with dot", "has.dot", func(in *agentadmin.Input) {}, "id", "has.dot"},
		{"empty prompt", "ok-id", func(in *agentadmin.Input) { in.SystemPrompt = "" }, "system_prompt", "required"},
		{"whitespace prompt", "ok-id", func(in *agentadmin.Input) { in.SystemPrompt = "   " }, "system_prompt", "required"},
		{"unsupported api", "ok-id", func(in *agentadmin.Input) {
			api := "completion"
			in.API = &api
		}, "api", "completion"},
		{"temperature below range", "ok-id", func(in *agentadmin.Input) {
			temp := float32(-0.5)
			in.Temperature = &temp
		}, "temperature", "-0.5"},
		{"temperature above range", "ok-id", func(in *agentadmin.Input) {
			temp := float32(2.5)
			in.Temperature = &temp
		}, "temperature", "2.5"},
		{"unknown tool", "ok-id", func(in *agentadmin.Input) {
			in.Tools = []string{"search_faq", "totally_missing"}
		}, "tools", "totally_missing"},
		{"empty tool name", "ok-id", func(in *agentadmin.Input) {
			in.Tools = []string{""}
		}, "tools", "non-empty"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			in := validInput()
			tc.mutate(&in)

			_, err := h.service.Create(context.Background(), tc.id, in)
			require.Error(t, err)

			var verr *agentadmin.ValidationError
			require.ErrorAs(t, err, &verr, "rejection must be a ValidationError, got: %v", err)
			assert.Equal(t, tc.wantField, verr.Field)
			assert.Contains(t, verr.Error(), tc.wantMessage, "rejection must name the offender")

			// no partial change: nothing stored, nothing built
			assert.Empty(t, h.store.createCalls, "rejected write must not reach the store")
			assert.Empty(t, h.factory.built, "rejected write must not rebuild the registry")
			assert.Empty(t, h.registry.IDs())
		})
	}

	t.Run("valid input passes", func(t *testing.T) {
		h := newHarness(t)

		agent, err := h.service.Create(context.Background(), "ok-id", validInput())
		require.NoError(t, err)
		assert.Equal(t, "ok-id", agent.ID)
		assert.Equal(t, "You are helpful.", agent.SystemPrompt)
		assert.True(t, agent.Enabled)
		assert.Equal(t, []string{"search_faq", "web.search"}, agent.Tools)

		require.Len(t, h.store.createCalls, 1)
		assert.Equal(t, "ok-id", h.store.createCalls[0])

		// registry serves the new agent
		assert.Contains(t, h.registry.IDs(), "ok-id")
	})
}

func TestCreateRebuildUsesStoredRow(t *testing.T) {
	h := newHarness(t)

	in := validInput()
	in.SortOrder = 7
	agent, err := h.service.Create(context.Background(), "stored-row", in)
	require.NoError(t, err)

	// the returned row carries the database-assigned timestamps
	assert.Equal(t, testTime, agent.CreatedAt, "must return the stored row, not the raw input")

	// the registry rebuild saw the stored row too (timestamps stamped,
	// proving it was not the raw input struct)
	require.Len(t, h.factory.built, 1)
	built := h.factory.built[0]
	assert.Equal(t, "stored-row", built.ID)
	assert.Equal(t, testTime, built.CreatedAt)
	assert.Equal(t, 7, built.SortOrder)

	// store.Create happened before the registry rebuild
	require.Len(t, h.eventsLog, 2)
	assert.Equal(t, "create:stored-row", h.eventsLog[0])
	assert.Equal(t, "rebuild:stored-row", h.eventsLog[1], "rebuild must follow the store write")
}

func TestUpdate(t *testing.T) {
	t.Run("unknown id passes through as ErrNotFound before validation", func(t *testing.T) {
		h := newHarness(t)

		in := validInput()
		in.SystemPrompt = "" // invalid on purpose: 404 must win over 400
		_, err := h.service.Update(context.Background(), "missing", in)
		assert.ErrorIs(t, err, domain.ErrNotFound)

		assert.Empty(t, h.store.updateCalls)
		assert.Empty(t, h.factory.built)
	})

	t.Run("validation rejects before any write", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.service.Create(context.Background(), "target", validInput())
		require.NoError(t, err)
		builtBefore := len(h.factory.built)

		in := validInput()
		in.Tools = []string{"no_such_tool"}
		_, err = h.service.Update(context.Background(), "target", in)
		require.Error(t, err)
		var verr *agentadmin.ValidationError
		require.ErrorAs(t, err, &verr)
		assert.Equal(t, "tools", verr.Field)
		assert.Contains(t, verr.Error(), "no_such_tool")

		// row unchanged, no rebuild
		stored, _, err := h.service.Get(context.Background(), "target")
		require.NoError(t, err)
		assert.Equal(t, []string{"search_faq", "web.search"}, stored.Tools, "row must be untouched")
		assert.Len(t, h.factory.built, builtBefore)
	})

	t.Run("valid update rewrites the row and rebuilds, id immutable", func(t *testing.T) {
		h := newHarness(t)
		created, err := h.service.Create(context.Background(), "target", validInput())
		require.NoError(t, err)

		in := validInput()
		in.SystemPrompt = "New prompt."
		in.Tools = []string{"search_faq"}
		in.Enabled = false
		in.SortOrder = 42
		updated, err := h.service.Update(context.Background(), "target", in)
		require.NoError(t, err)

		// id immutability: the update keeps the id it was addressed by,
		// both in the write the store received and in the stored row
		require.Len(t, h.store.updateCalls, 1)
		assert.Equal(t, "target", h.store.updateCalls[0].ID)
		assert.Equal(t, "target", updated.ID)
		assert.Equal(t, "New prompt.", updated.SystemPrompt)
		assert.False(t, updated.Enabled)
		assert.Equal(t, 42, updated.SortOrder)

		// created_at survives the rewrite (only the definition changed)
		assert.Equal(t, created.CreatedAt, updated.CreatedAt)

		stored, degraded, err := h.service.Get(context.Background(), "target")
		require.NoError(t, err)
		assert.Equal(t, "target", stored.ID)
		assert.Equal(t, "New prompt.", stored.SystemPrompt)
		assert.Empty(t, degraded)

		// the update disabled the agent: the registry drops it instead of
		// rebuilding (spec: a disabled agent never serves chat)
		require.Len(t, h.factory.built, 1)
		assert.NotContains(t, h.registry.IDs(), "target")
	})
}

func TestDeleteBlockedWhileReferenced(t *testing.T) {
	h := newHarness(t)
	_, err := h.service.Create(context.Background(), "busy", validInput())
	require.NoError(t, err)

	h.count = 2
	err = h.service.Delete(context.Background(), "busy")
	require.ErrorIs(t, err, domain.ErrAgentInUse, "delete must be blocked with 409 semantics")

	// nothing happened: no store delete, no registry remove
	assert.Empty(t, h.store.deleteCalls)
	assert.Contains(t, h.registry.IDs(), "busy", "agent must stay live")
	stored, _, err := h.service.Get(context.Background(), "busy")
	require.NoError(t, err, "agent must remain stored")
	assert.Equal(t, "busy", stored.ID)
}

func TestDeleteCleanRemovesFromRegistry(t *testing.T) {
	h := newHarness(t)
	_, err := h.service.Create(context.Background(), "doomed", validInput())
	require.NoError(t, err)

	h.count = 0
	require.NoError(t, h.service.Delete(context.Background(), "doomed"))

	require.Equal(t, []string{"doomed"}, *h.askedFor, "delete must count conversations for the right agent")
	assert.Equal(t, []string{"doomed"}, h.store.deleteCalls)
	_, _, err = h.service.Get(context.Background(), "doomed")
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.NotContains(t, h.registry.IDs(), "doomed", "deleted agent must leave the registry")
}

func TestDeleteUnknownID(t *testing.T) {
	h := newHarness(t)

	err := h.service.Delete(context.Background(), "ghost")
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.Empty(t, h.store.deleteCalls)
}

func TestDeleteConversationCountErrorPropagates(t *testing.T) {
	h := newHarness(t)
	_, err := h.service.Create(context.Background(), "agent", validInput())
	require.NoError(t, err)

	h.countErr = errors.New("db down")
	err = h.service.Delete(context.Background(), "agent")
	require.Error(t, err)
	assert.NotErrorIs(t, err, domain.ErrAgentInUse)
	assert.Empty(t, h.store.deleteCalls, "must not delete when the count fails")
}

func TestSetEnabled(t *testing.T) {
	t.Run("disable removes from registry, keeps the row", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.service.Create(context.Background(), "napper", validInput())
		require.NoError(t, err)
		require.Contains(t, h.registry.IDs(), "napper")

		require.NoError(t, h.service.SetEnabled(context.Background(), "napper", false))

		stored, _, err := h.service.Get(context.Background(), "napper")
		require.NoError(t, err, "disabled agent stays stored and readable")
		assert.False(t, stored.Enabled)
		assert.NotContains(t, h.registry.IDs(), "napper", "disabled agent must leave the registry")
	})

	t.Run("enable rebuilds from the fresh row", func(t *testing.T) {
		h := newHarness(t)
		in := validInput()
		in.Enabled = false
		_, err := h.service.Create(context.Background(), "riser", in)
		require.NoError(t, err)
		// Create honors the disabled flag: the agent stays OUT of the
		// registry until it is enabled (a disabled agent never serves
		// chat).
		require.NotContains(t, h.registry.IDs(), "riser")

		// enabling rebuilds it from the fresh row
		require.NoError(t, h.service.SetEnabled(context.Background(), "riser", true))

		stored, _, err := h.service.Get(context.Background(), "riser")
		require.NoError(t, err)
		assert.True(t, stored.Enabled)
		assert.Contains(t, h.registry.IDs(), "riser")
		require.NotEmpty(t, h.factory.built)
		assert.True(t, h.factory.built[len(h.factory.built)-1].Enabled,
			"enable must rebuild from the fresh row")
	})

	t.Run("unknown id passes through", func(t *testing.T) {
		h := newHarness(t)
		assert.ErrorIs(t, h.service.SetEnabled(context.Background(), "ghost", true), domain.ErrNotFound)
	})
}

func TestGetReturnsDegradedTools(t *testing.T) {
	h := newHarness(t)
	h.factory.degrade["degraded"] = []string{"mcp.down.search"}

	_, err := h.service.Create(context.Background(), "degraded", validInput())
	require.NoError(t, err)
	_, err = h.service.Create(context.Background(), "healthy", validInput())
	require.NoError(t, err)

	_, degraded, err := h.service.Get(context.Background(), "degraded")
	require.NoError(t, err)
	assert.Equal(t, []string{"mcp.down.search"}, degraded)

	_, degraded, err = h.service.Get(context.Background(), "healthy")
	require.NoError(t, err)
	assert.Empty(t, degraded)

	_, _, err = h.service.Get(context.Background(), "ghost")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestListSortedWithDegradedMap(t *testing.T) {
	h := newHarness(t)
	h.factory.degrade["zeta"] = []string{"mcp.x.missing"}

	// create in an order that differs from the expected result order
	for _, tc := range []struct {
		id        string
		sortOrder int
	}{
		{"zeta", 5},
		{"alpha", 5}, // tie with zeta: id breaks it
		{"first", 1},
	} {
		in := validInput()
		in.SortOrder = tc.sortOrder
		_, err := h.service.Create(context.Background(), tc.id, in)
		require.NoError(t, err)
	}

	agents, degraded, err := h.service.List(context.Background())
	require.NoError(t, err)

	ids := make([]string, len(agents))
	for i, a := range agents {
		ids[i] = a.ID
	}
	assert.Equal(t, []string{"first", "alpha", "zeta"}, ids,
		"rows must be sorted by (sort_order, id)")

	assert.Equal(t, map[string][]string{"zeta": {"mcp.x.missing"}}, degraded,
		"degraded map must be keyed by agent id and omit healthy agents")
}

func TestRebuildFailureKeepsPreviousRegistryEntry(t *testing.T) {
	h := newHarness(t)
	_, err := h.service.Create(context.Background(), "runner", validInput())
	require.NoError(t, err)

	// next rebuild of this agent fails at the factory
	h.factory.fail["runner"] = errors.New("bad model override")
	_, err = h.service.Update(context.Background(), "runner", func() agentadmin.Input {
		in := validInput()
		in.SystemPrompt = "Changed."
		return in
	}())
	require.Error(t, err)

	// the row is persisted regardless; the registry keeps serving the old build
	stored, _, err := h.service.Get(context.Background(), "runner")
	require.NoError(t, err)
	assert.Equal(t, "Changed.", stored.SystemPrompt)
	assert.Contains(t, h.registry.IDs(), "runner")
}

func TestToolNamesReturnsSortedCatalogKeys(t *testing.T) {
	h := newHarness(t)

	names := h.service.ToolNames()
	assert.Equal(t, []string{"search_faq", "web.search"}, names,
		"the assignable names are the catalog's sorted keys")

	// The listing is a copy: mutating it must not reach the catalog map
	// the service validates assignments against.
	names[0] = "mutated"
	assert.Equal(t, []string{"search_faq", "web.search"}, h.service.ToolNames(),
		"each call returns a fresh sorted slice")
}
