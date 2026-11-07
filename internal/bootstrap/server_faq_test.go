package bootstrap

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/config"
	mcpadapter "github.com/adityapratamanusantara/fantastic/internal/adapters/mcp"
	faqtool "github.com/adityapratamanusantara/fantastic/internal/adapters/tools/faq"
)

// The FAQ tool must degrade, not fail: bootstrap registers search_faq in
// the named tool catalog only when the capability can come up (design
// D10), and a construction failure must never break service start. These
// tests pin the newFaqSearchTool contract: every unavailable dependency
// yields nil plus a warning, and a fully configured tool builds without
// any live dependency (no database, no network — the embedder client is
// constructed offline). The catalog tests pin the bootstrap wiring on top
// of that contract: search_faq joins the catalog exactly when the tool
// builds. Agent prompts live in the agents-table rows, so the catalog
// itself carries no prompt addendum since task 5.3 (the FAQ answering
// rules travel with the seeded agent row prompts).

func faqToolLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func TestNewFaqSearchToolDegradesWithoutCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	log, buf := faqToolLogger()

	// nil pool proves the credential gate runs before any store use.
	got := newFaqSearchTool(context.Background(), &config.Config{}, nil, log)

	require.Nil(t, got, "missing embedding credential must degrade to nil, not fail")
	assert.Contains(t, buf.String(), "faq search disabled")
	assert.Contains(t, buf.String(), "credential unavailable")
}

func TestNewFaqSearchToolDegradesOnInvalidTopK(t *testing.T) {
	log, buf := faqToolLogger()
	cfg := &config.Config{}
	cfg.FAQ.Embedding.APIKey = "test-key"
	cfg.FAQ.Search.TopK = 0 // unset/invalid: the tool refuses to serve

	got := newFaqSearchTool(context.Background(), cfg, nil, log)

	require.Nil(t, got, "an invalid top_k must degrade to nil, not fail")
	assert.Contains(t, buf.String(), "faq search disabled")
}

func TestNewFaqSearchToolBuildsWithoutLiveDependencies(t *testing.T) {
	log, buf := faqToolLogger()
	cfg := &config.Config{}
	cfg.FAQ.Embedding.APIKey = "test-key"
	cfg.FAQ.Embedding.Model = config.DefaultFaqEmbeddingModel
	cfg.FAQ.Search.TopK = config.DefaultFaqSearchTopK

	// nil pool: the store keeps the pool reference without using it at
	// construction, so the tool builds with no live database — startup
	// ordering can never fail on FAQ.
	got := newFaqSearchTool(context.Background(), cfg, nil, log)

	require.NotNil(t, got, "a fully configured FAQ tool must build without a live DB")
	info, err := got.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, faqtool.Name, info.Name, "the tool must expose itself as search_faq")
	assert.NotContains(t, buf.String(), "faq search disabled", "no warning on the healthy path")
}

func TestBuildToolCatalogJoinsSearchFaq(t *testing.T) {
	log, _ := faqToolLogger()
	cfg := &config.Config{}
	cfg.FAQ.Embedding.APIKey = "test-key"
	cfg.FAQ.Embedding.Model = config.DefaultFaqEmbeddingModel
	cfg.FAQ.Search.TopK = config.DefaultFaqSearchTopK

	catalog := buildToolCatalog(context.Background(), &mcpadapter.Registry{}, cfg, nil, log)

	require.Contains(t, catalog, faqtool.Name, "search_faq joins the catalog exactly when the tool builds")
	_, ok := catalog[faqtool.Name].(interface {
		Info(context.Context) (*schema.ToolInfo, error)
	})
	assert.True(t, ok, "the catalog entry is a usable eino tool")
}

func TestBuildToolCatalogDegradesWithoutFAQ(t *testing.T) {
	log, buf := faqToolLogger()

	catalog := buildToolCatalog(context.Background(), &mcpadapter.Registry{}, &config.Config{}, nil, log)

	assert.Empty(t, catalog, "without credentials the catalog stays MCP-only")
	assert.Contains(t, buf.String(), "faq search disabled", "the degradation is logged, never fatal")
}
