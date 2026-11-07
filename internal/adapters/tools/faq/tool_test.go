package faq_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/tools/faq"
	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
)

// The fakes for driven.FAQSearcher and embedding.Embedder live in
// testfakes_test.go, following the chat usecase's testfake convention.

func unitVec(v float64) []float64 {
	out := make([]float64, config.DefaultFaqEmbeddingDims)
	out[0] = v
	return out
}

var sampleHits = []driven.FaqHit{
	{
		ChunkText:   "Biaya top up LinkAja dari bank ... gratis.",
		ChunkIndex:  0,
		ContentHash: "sha256:abc",
		SourceURL:   "https://www.linkaja.id/faq/top-up",
		Title:       "Top Up",
		Score:       0.87,
	},
}

func newTool(t *testing.T, searcher *fakeSearcher, embedder *fakeEmbedder, topK int) tool.BaseTool {
	t.Helper()
	tl, err := faq.NewSearchTool(searcher, embedder, topK)
	require.NoError(t, err)
	return tl
}

// invoke calls the tool the way the ADK's tools node does: BaseTool in,
// InvokableTool invoked.
func invoke(t *testing.T, tl tool.BaseTool, arguments string) (string, error) {
	t.Helper()
	it, ok := tl.(tool.InvokableTool)
	require.True(t, ok, "search_faq must be invokable")
	return it.InvokableRun(context.Background(), arguments)
}

func TestSearchToolInfoDescribesWhenToCall(t *testing.T) {
	tl := newTool(t, &fakeSearcher{}, &fakeEmbedder{}, 5)

	info, err := tl.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "search_faq", info.Name)
	assert.NotEmpty(t, info.Desc)
	// The description must tell the agent WHEN to call the tool and that
	// answers must cite the returned source_url.
	assert.Contains(t, info.Desc, "LinkAja")
	assert.Contains(t, info.Desc, "source_url")
	// Input schema inferred: one query field.
	require.NotNil(t, info.ParamsOneOf)
}

func TestSearchToolRequestResponseHandling(t *testing.T) {
	searcher := &fakeSearcher{hits: sampleHits}
	embedder := &fakeEmbedder{vecs: [][]float64{unitVec(1)}}
	tl := newTool(t, searcher, embedder, 5)

	out, err := invoke(t, tl, `{"query":"berapa biaya top up LinkAja?"}`)
	require.NoError(t, err)

	// The question went to the embedder verbatim...
	require.Equal(t, []string{"berapa biaya top up LinkAja?"}, embedder.lastTexts)
	// ...the returned vector (converted to float32) and the configured k
	// went to the searcher.
	require.Equal(t, 1, searcher.calls)
	require.Len(t, searcher.lastQuery, config.DefaultFaqEmbeddingDims)
	require.InDelta(t, 1.0, searcher.lastQuery[0], 1e-9)
	require.Equal(t, 5, searcher.lastK)

	// The output is a JSON array carrying the citation fields.
	var hits []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &hits))
	require.Len(t, hits, 1)
	assert.Equal(t, sampleHits[0].ChunkText, hits[0]["text"])
	assert.Equal(t, sampleHits[0].SourceURL, hits[0]["source_url"])
	assert.Equal(t, sampleHits[0].Title, hits[0]["title"])
	assert.Equal(t, sampleHits[0].ContentHash, hits[0]["content_hash"])
	assert.InDelta(t, sampleHits[0].Score, hits[0]["score"], 1e-9)
}

func TestSearchToolMapsAllHitsInStoreOrder(t *testing.T) {
	// The store returns hits best first; the tool JSON must carry every
	// FaqHit field and preserve that order (the agent cites the first).
	searcher := &fakeSearcher{hits: []driven.FaqHit{
		{ChunkText: "first chunk", ChunkIndex: 0, ContentHash: "hash-1", SourceURL: "https://www.linkaja.id/faq/a", Title: "A", Score: 0.9},
		{ChunkText: "second chunk", ChunkIndex: 1, ContentHash: "hash-2", SourceURL: "https://www.linkaja.id/faq/b", Title: "B", Score: 0.4},
		{ChunkText: "third chunk", ChunkIndex: 2, ContentHash: "hash-3", SourceURL: "https://www.linkaja.id/faq/c", Title: "", Score: -0.1},
	}}
	embedder := &fakeEmbedder{vecs: [][]float64{unitVec(1)}}
	tl := newTool(t, searcher, embedder, 5)

	out, err := invoke(t, tl, `{"query":"cara transfer?"}`)
	require.NoError(t, err)

	var hits []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &hits))
	require.Len(t, hits, 3)
	for i, want := range searcher.hits {
		assert.Equal(t, want.ChunkText, hits[i]["text"], "hit %d text", i)
		assert.Equal(t, want.SourceURL, hits[i]["source_url"], "hit %d source_url", i)
		assert.Equal(t, want.Title, hits[i]["title"], "hit %d title", i)
		assert.Equal(t, want.ContentHash, hits[i]["content_hash"], "hit %d content_hash", i)
		assert.InDelta(t, want.Score, hits[i]["score"], 1e-9, "hit %d score", i)
	}
}

func TestSearchToolHonorsConfiguredTopK(t *testing.T) {
	searcher := &fakeSearcher{}
	embedder := &fakeEmbedder{vecs: [][]float64{unitVec(1)}}
	tl := newTool(t, searcher, embedder, 3)

	_, err := invoke(t, tl, `{"query":"apa itu LinkAja?"}`)
	require.NoError(t, err)
	assert.Equal(t, 3, searcher.lastK)
}

func TestSearchToolEmptyResultIsExplicitEmptyArray(t *testing.T) {
	// Zero hits is not an error: the grounded fallback is the system
	// prompt's job; the tool reports an explicit empty result.
	searcher := &fakeSearcher{hits: []driven.FaqHit{}}
	embedder := &fakeEmbedder{vecs: [][]float64{unitVec(1)}}
	tl := newTool(t, searcher, embedder, 5)

	out, err := invoke(t, tl, `{"query":"siapa presiden pertama Indonesia?"}`)
	require.NoError(t, err)
	assert.Equal(t, "[]", out)
}

func TestSearchToolRejectsBlankQuery(t *testing.T) {
	tl := newTool(t, &fakeSearcher{}, &fakeEmbedder{}, 5)

	_, err := invoke(t, tl, `{"query":"   "}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query")
}

func TestSearchToolSurfacesSearcherError(t *testing.T) {
	// A failing store must reach the agent as an error, never a panic.
	searcher := &fakeSearcher{err: errors.New("db down")}
	embedder := &fakeEmbedder{vecs: [][]float64{unitVec(1)}}
	tl := newTool(t, searcher, embedder, 5)

	_, err := invoke(t, tl, `{"query":"biaya transfer?"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
}

func TestSearchToolSurfacesEmbedderError(t *testing.T) {
	// A failing embedder must surface as an error and must not reach the
	// store with a nil/short embedding.
	searcher := &fakeSearcher{}
	embedder := &fakeEmbedder{err: errors.New("quota exceeded")}
	tl := newTool(t, searcher, embedder, 5)

	_, err := invoke(t, tl, `{"query":"biaya transfer?"}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota exceeded")
	assert.Equal(t, 0, searcher.calls, "store must not be called when embedding fails")
}

func TestSearchToolSurfacesMalformedArguments(t *testing.T) {
	tl := newTool(t, &fakeSearcher{}, &fakeEmbedder{}, 5)

	_, err := invoke(t, tl, `not json`)
	require.Error(t, err)
}

func TestSearchToolRejectsNonPositiveTopK(t *testing.T) {
	_, err := faq.NewSearchTool(&fakeSearcher{}, &fakeEmbedder{}, 0)
	require.Error(t, err)
}

func TestPromptAddendumStatesGroundingRules(t *testing.T) {
	// design D9: answer language, call-first mandate, citation duty,
	// grounded fallback.
	assert.Contains(t, faq.PromptAddendum, "Indonesian")
	assert.Contains(t, faq.PromptAddendum, "call search_faq BEFORE answering")
	assert.Contains(t, faq.PromptAddendum, "source")
	assert.Contains(t, faq.PromptAddendum, "no relevant results")
}
