package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/repositories/postgres"
	faqtool "github.com/adityapratamanusantara/fantastic/internal/adapters/tools/faq"
	"github.com/adityapratamanusantara/fantastic/internal/application/agentregistry"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
	"github.com/adityapratamanusantara/fantastic/migrations"
)

// DB-backed registry-load test (tasks 3.4/5.3): agents come from the
// compose database, the bootstrap factory resolves their tools against
// the boot-wide catalog, and loadAgents puts only enabled agents into the
// registry — warning about assigned-but-missing tool names (design D5
// degrade) instead of failing the batch. It follows the harness style of
// the postgres adapter tests (TEST_DATABASE_URL, same local default) and
// skips when no database is reachable, so the package stays runnable
// without one. Cleanup is hermetic: zz- rows and any conversations
// referencing them are deleted when the test ends.

const (
	wireEnabledAgentID  = "zz-wire-enabled"
	wireDisabledAgentID = "zz-wire-disabled"
	wireMissingTool     = "zz.missing_tool"
)

func wireAgentTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://fantastic:fantastic-dev@localhost:5555/fantastic_test"
	}
	if err := migrations.Up(url, slog.Default()); err != nil {
		t.Skipf("agent registry load test needs a migrated database: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Skipf("agent registry load test needs a database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("agent registry load test needs a reachable database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// cleanupWireAgents removes the test rows and any conversations bound to
// them (conversations first: agents.agent_id FK is ON DELETE RESTRICT).
func cleanupWireAgents(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, err := pool.Exec(ctx, `DELETE FROM conversations WHERE agent_id IN ($1, $2)`, wireEnabledAgentID, wireDisabledAgentID)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `DELETE FROM agents WHERE id IN ($1, $2)`, wireEnabledAgentID, wireDisabledAgentID)
		require.NoError(t, err)
	})
}

func TestLoadAgentsFromDB(t *testing.T) {
	pool := wireAgentTestPool(t)
	cleanupWireAgents(t, pool)
	ctx := context.Background()
	log, buf := faqToolLogger()
	agentStore := postgres.NewAgentStore(pool)

	enabled := &domain.Agent{
		ID:           wireEnabledAgentID,
		DisplayName:  "Wire Enabled",
		Description:  "bootstrap wiring test agent",
		SystemPrompt: "You are the bootstrap wiring test agent.",
		Model:        strPtr("wire-test-model"),
		API:          strPtr("chat"),
		Temperature:  f32Ptr(0.3),
		// one resolvable catalog tool, one unknown name: the agent must
		// build with the subset and carry zz.missing_tool as degraded
		Tools:   []string{faqtool.Name, wireMissingTool},
		Enabled: true,
	}
	disabled := &domain.Agent{
		ID:           wireDisabledAgentID,
		DisplayName:  "Wire Disabled",
		SystemPrompt: "This agent is disabled.",
		Enabled:      false,
	}
	require.NoError(t, agentStore.Create(ctx, enabled))
	require.NoError(t, agentStore.Create(ctx, disabled))

	// the boot-wide catalog: the real search_faq tool (constructed
	// offline, mirroring buildToolCatalog's degrade-not-fail path)
	faqCfg := &config.Config{}
	faqCfg.FAQ.Embedding.APIKey = "test-key"
	faqCfg.FAQ.Embedding.Model = config.DefaultFaqEmbeddingModel
	faqCfg.FAQ.Search.TopK = config.DefaultFaqSearchTopK
	faqTool := newFaqSearchTool(ctx, faqCfg, pool, log)
	require.NotNil(t, faqTool, "the catalog fixture needs the search_faq tool")
	catalog := map[string]tool.BaseTool{faqtool.Name: faqTool}

	cfg := &config.Config{
		LLM:    config.LLMConfig{API: "responses", BaseURL: "http://127.0.0.1:9", Model: "global-model"},
		Memory: config.MemoryConfig{HistoryWindow: 5},
	}
	factory := newAgentFactory(postgres.NewStore(pool), catalog, cfg, "test-key", log)
	registry := agentregistry.New(factory)

	// direct factory call: the enabled agent builds despite the unknown
	// tool, and the unknown name is reported as degraded
	res, err := factory(*enabled)
	require.NoError(t, err, "an unresolvable tool name must degrade the agent, not fail the build")
	require.NotNil(t, res.Service)
	assert.Equal(t, []string{wireMissingTool}, res.DegradedTools)

	// the load helper: enabled agents into the registry, disabled ones out
	loadAgents(ctx, registry, agentStore, log)

	svc, ok := registry.Get(wireEnabledAgentID)
	require.True(t, ok, "the enabled agent must be in the registry")
	assert.NotNil(t, svc, "the enabled agent must carry a runnable chat service")
	assert.Equal(t, []string{wireMissingTool}, registry.Degraded(wireEnabledAgentID),
		"the degraded list must name exactly the unresolvable tool")

	_, ok = registry.Get(wireDisabledAgentID)
	assert.False(t, ok, "the disabled agent must stay out of the registry")
	assert.NotContains(t, registry.IDs(), wireDisabledAgentID)

	// the degradation is logged per agent with the missing names (task 3.4)
	logText := buf.String()
	assert.Contains(t, logText, "running without tools")
	assert.Contains(t, logText, wireEnabledAgentID)
	assert.True(t, strings.Contains(logText, wireMissingTool),
		"the degraded warning must name the missing tool %q; log:\n%s", wireMissingTool, logText)
	assert.NotContains(t, logText, "agent build failed", "a healthy load must not warn about build failures")
}
