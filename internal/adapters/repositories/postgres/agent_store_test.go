package postgres

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// AgentStore tests run against the real compose database through the
// package TestMain, like conversation_store_test.go. They use unique
// agent ids and delete their rows afterwards, so they never disturb the
// seeded 'general' agent or the conversation tests.

const (
	testAgentAID = "zz-agent-test-roundtrip"
	testAgentBID = "zz-agent-test-list"
	testAgentSID = "zz-agent-test-if-absent"
)

func agentTestStore() *AgentStore {
	return NewAgentStore(testPool())
}

// newTestAgent builds an agent with every nullable field populated.
func newTestAgent(id string) *domain.Agent {
	model := "test-model"
	api := "chat"
	temp := float32(0.7)
	effort := "low"
	return &domain.Agent{
		ID:              id,
		DisplayName:     "Test Agent",
		Description:     "an agent used by tests",
		Icon:            "sparkles",
		SystemPrompt:    "You are a test agent.",
		Model:           &model,
		API:             &api,
		Temperature:     &temp,
		ReasoningEffort: &effort,
		Tools:           []string{"search_faq", "web_search"},
		Enabled:         true,
		SortOrder:       7,
	}
}

// cleanupAgent deletes the row when the test ends, whatever happened.
func cleanupAgent(t *testing.T, id string) {
	t.Helper()
	t.Cleanup(func() {
		_, err := testPool().Exec(context.Background(), `DELETE FROM agents WHERE id = $1`, id)
		require.NoError(t, err)
	})
}

// assertAgentFields compares every stored field except the timestamps
// (asserted non-zero separately: the database owns the clock).
func assertAgentFields(t *testing.T, want, got *domain.Agent) {
	t.Helper()
	assert.Equal(t, want.ID, got.ID)
	assert.Equal(t, want.DisplayName, got.DisplayName)
	assert.Equal(t, want.Description, got.Description)
	assert.Equal(t, want.Icon, got.Icon)
	assert.Equal(t, want.SystemPrompt, got.SystemPrompt)
	if want.Model == nil {
		assert.Nil(t, got.Model)
	} else {
		require.NotNil(t, got.Model)
		assert.Equal(t, *want.Model, *got.Model)
	}
	if want.API == nil {
		assert.Nil(t, got.API)
	} else {
		require.NotNil(t, got.API)
		assert.Equal(t, *want.API, *got.API)
	}
	if want.Temperature == nil {
		assert.Nil(t, got.Temperature)
	} else {
		require.NotNil(t, got.Temperature)
		assert.Equal(t, *want.Temperature, *got.Temperature)
	}
	if want.ReasoningEffort == nil {
		assert.Nil(t, got.ReasoningEffort)
	} else {
		require.NotNil(t, got.ReasoningEffort)
		assert.Equal(t, *want.ReasoningEffort, *got.ReasoningEffort)
	}
	assert.Equal(t, want.Tools, got.Tools)
	assert.Equal(t, want.Enabled, got.Enabled)
	assert.Equal(t, want.SortOrder, got.SortOrder)
	assert.False(t, got.CreatedAt.IsZero(), "created_at must be assigned by the database")
	assert.False(t, got.UpdatedAt.IsZero(), "updated_at must be assigned by the database")
}

func TestAgentCRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := agentTestStore()
	agent := newTestAgent(testAgentAID)
	cleanupAgent(t, agent.ID)

	require.NoError(t, store.Create(ctx, agent))

	got, err := store.Get(ctx, agent.ID)
	require.NoError(t, err)
	assertAgentFields(t, agent, got)

	// full-row update: overrides cleared, tools emptied, flag flipped
	updated := &domain.Agent{
		ID:              agent.ID,
		DisplayName:     "Renamed Agent",
		Description:     "updated description",
		Icon:            "robot",
		SystemPrompt:    "You are an updated agent.",
		Model:           nil,
		API:             strPtr("responses"),
		Temperature:     nil,
		ReasoningEffort: nil,
		Tools:           []string{},
		Enabled:         false,
		SortOrder:       3,
	}
	require.NoError(t, store.Update(ctx, updated))

	got, err = store.Get(ctx, agent.ID)
	require.NoError(t, err)
	assertAgentFields(t, updated, got)

	// set-enabled round-trip
	require.NoError(t, store.SetEnabled(ctx, agent.ID, true))
	got, err = store.Get(ctx, agent.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled)

	// delete, then the row is gone for good
	require.NoError(t, store.Delete(ctx, agent.ID))
	_, err = store.Get(ctx, agent.ID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.ErrorIs(t, store.Delete(ctx, agent.ID), domain.ErrNotFound)
}

func TestAgentUnknownID(t *testing.T) {
	ctx := context.Background()
	store := agentTestStore()

	_, err := store.Get(ctx, "zz-agent-test-missing")
	assert.ErrorIs(t, err, domain.ErrNotFound)

	assert.ErrorIs(t, store.Delete(ctx, "zz-agent-test-missing"), domain.ErrNotFound)
	assert.ErrorIs(t, store.Update(ctx, newTestAgent("zz-agent-test-missing")), domain.ErrNotFound)
	assert.ErrorIs(t, store.SetEnabled(ctx, "zz-agent-test-missing", false), domain.ErrNotFound)
}

func TestAgentCreateDuplicateID(t *testing.T) {
	ctx := context.Background()
	store := agentTestStore()
	agent := newTestAgent("zz-agent-test-dup")
	cleanupAgent(t, agent.ID)

	require.NoError(t, store.Create(ctx, agent))

	err := store.Create(ctx, agent)
	require.Error(t, err, "duplicate id must surface as an error")
	assert.ErrorIs(t, err, domain.ErrAgentExists, "a taken slug maps to the domain conflict error, not a raw driver error")
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23505", pgErr.Code, "the unique violation is still inspectable")
}

func TestAgentCreateIfAbsentQuietKeepsOriginal(t *testing.T) {
	ctx := context.Background()
	store := agentTestStore()
	cleanupAgent(t, testAgentSID)

	// first insert lands and is reported as new
	inserted, err := store.CreateIfAbsent(ctx, newTestAgent(testAgentSID))
	require.NoError(t, err)
	assert.True(t, inserted, "first insert must report a new row")

	original, err := store.Get(ctx, testAgentSID)
	require.NoError(t, err)
	assertAgentFields(t, newTestAgent(testAgentSID), original)

	// second insert with different values is quiet and changes nothing
	changed := newTestAgent(testAgentSID)
	changed.DisplayName = "Impostor"
	changed.SystemPrompt = "You are not the original."
	changed.Tools = []string{"other_tool"}
	changed.SortOrder = 99
	inserted, err = store.CreateIfAbsent(ctx, changed)
	require.NoError(t, err)
	assert.False(t, inserted, "conflicting insert must report no new row")

	got, err := store.Get(ctx, testAgentSID)
	require.NoError(t, err)
	assert.Equal(t, "Test Agent", got.DisplayName, "original row must win")
	assert.Equal(t, "You are a test agent.", got.SystemPrompt)
	assert.Equal(t, []string{"search_faq", "web_search"}, got.Tools)

	// even after a management update, reseeding keeps the current row
	updated := newTestAgent(testAgentSID)
	updated.DisplayName = "Operator Edited"
	require.NoError(t, store.Update(ctx, updated))
	inserted, err = store.CreateIfAbsent(ctx, changed)
	require.NoError(t, err)
	assert.False(t, inserted)

	got, err = store.Get(ctx, testAgentSID)
	require.NoError(t, err)
	assert.Equal(t, "Operator Edited", got.DisplayName, "seed must not clobber DB-edited values")
}

func TestAgentListOrdersBySortOrder(t *testing.T) {
	ctx := context.Background()
	store := agentTestStore()
	first := newTestAgent(testAgentAID)
	second := newTestAgent(testAgentBID)
	cleanupAgent(t, first.ID)
	cleanupAgent(t, second.ID)

	require.NoError(t, store.Create(ctx, first)) // sort_order 7
	second.SortOrder = 42
	require.NoError(t, store.Create(ctx, second))

	got, err := store.List(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, got)

	// find our two rows among all agents (the seeded 'general' may be
	// present) and check their relative order
	var idxA, idxB = -1, -1
	for i, a := range got {
		switch a.ID {
		case first.ID:
			idxA = i
		case second.ID:
			idxB = i
		}
	}
	require.NotEqual(t, -1, idxA, "created agent A must be listed")
	require.NotEqual(t, -1, idxB, "created agent B must be listed")
	assert.Less(t, idxA, idxB, "lower sort_order must come first")

	// SetEnabled is visible through List
	require.NoError(t, store.SetEnabled(ctx, first.ID, false))
	got, err = store.List(ctx)
	require.NoError(t, err)
	for _, a := range got {
		if a.ID == first.ID {
			assert.False(t, a.Enabled)
		}
	}
}

func strPtr(s string) *string { return &s }

// TestAgentStoreSeedAgents exercises the design D8 seeding lifecycle on
// the real database: the first call creates every config-declared agent,
// a re-seed leaves DB-edited values untouched, and a table emptied by
// hand is refilled on the next call. The delete-everything phase
// snapshots the pre-existing rows (including the migration-seeded
// 'general' agent) and restores them afterwards, so the shared database
// is left as found.
func TestAgentStoreSeedAgents(t *testing.T) {
	ctx := context.Background()
	store := agentTestStore()

	seedAID := "zz-agent-test-seed-a"
	seedBID := "zz-agent-test-seed-b"
	cleanupAgent(t, seedAID)
	cleanupAgent(t, seedBID)

	enabled := true
	seeds := config.AgentSeeds{
		seedAID: { // every field populated
			DisplayName:     "Seed Agent A",
			Description:     "fully overridden seed",
			Icon:            "sparkles",
			SystemPrompt:    "You are seeded agent A.",
			Tools:           []string{"search_faq"},
			Model:           "seed-model-a",
			API:             "chat",
			Temperature:     0.4,
			ReasoningEffort: "low",
			Enabled:         &enabled,
			SortOrder:       5,
		},
		seedBID: { // bare minimum: overrides inherit the global llm block
			DisplayName:  "Seed Agent B",
			SystemPrompt: "You are seeded agent B.",
		},
	}

	before, err := store.List(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { restoreAgents(t, before) })

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	// Phase 1: the first call creates both rows and reports each one.
	seeded, err := store.SeedAgents(ctx, seeds, log)
	require.NoError(t, err)
	assert.Equal(t, 2, seeded, "first seeding must create both agents")

	gotA, err := store.Get(ctx, seedAID)
	require.NoError(t, err)
	assertAgentFields(t, &domain.Agent{
		ID:              seedAID,
		DisplayName:     "Seed Agent A",
		Description:     "fully overridden seed",
		Icon:            "sparkles",
		SystemPrompt:    "You are seeded agent A.",
		Model:           strPtr("seed-model-a"),
		API:             strPtr("chat"),
		Temperature:     float32Ptr(0.4),
		ReasoningEffort: strPtr("low"),
		Tools:           []string{"search_faq"},
		Enabled:         true,
		SortOrder:       5,
	}, gotA)

	// minimal seed: empty overrides are stored as nil columns, tools as
	// an empty slice, enabled defaults to true
	gotB, err := store.Get(ctx, seedBID)
	require.NoError(t, err)
	assertAgentFields(t, &domain.Agent{
		ID:           seedBID,
		DisplayName:  "Seed Agent B",
		SystemPrompt: "You are seeded agent B.",
		Tools:        []string{},
		Enabled:      true,
	}, gotB)

	assert.Contains(t, logs.String(), "agent_id="+seedAID, "newly created rows must be logged at Info")
	assert.Contains(t, logs.String(), "agent_id="+seedBID)

	// Phase 2: a DB edit wins — re-seeding must not clobber it.
	logs.Reset()
	edited := &domain.Agent{
		ID:           seedAID,
		DisplayName:  "Operator Edited A",
		SystemPrompt: "The operator rewrote this prompt.",
		Tools:        []string{},
		Enabled:      true,
	}
	require.NoError(t, store.Update(ctx, edited))

	seeded, err = store.SeedAgents(ctx, seeds, log)
	require.NoError(t, err)
	assert.Equal(t, 0, seeded, "re-seeding must create nothing")

	gotA, err = store.Get(ctx, seedAID)
	require.NoError(t, err)
	assert.Equal(t, "The operator rewrote this prompt.", gotA.SystemPrompt, "DB edit must survive re-seeding")
	assert.Equal(t, "Operator Edited A", gotA.DisplayName)
	assert.NotContains(t, logs.String(), "agent seeded from config", "absent rows must not log creations")

	// Phase 3: a hand-emptied table reseeds on the next boot.
	_, err = testPool().Exec(ctx, `DELETE FROM agents`)
	require.NoError(t, err)

	seeded, err = store.SeedAgents(ctx, seeds, log)
	require.NoError(t, err)
	assert.Equal(t, 2, seeded, "empty table must reseed every config agent")

	all, err := store.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2, "after reseed the table holds exactly the config agents")
	ids := []string{all[0].ID, all[1].ID}
	assert.ElementsMatch(t, []string{seedAID, seedBID}, ids)
}

// restoreAgents puts the pre-test agent rows back (used after the
// delete-all phase of the seeding test).
func restoreAgents(t *testing.T, rows []domain.Agent) {
	t.Helper()
	for i := range rows {
		require.NoError(t, agentTestStore().Create(context.Background(), &rows[i]))
	}
}

func float32Ptr(f float32) *float32 { return &f }
