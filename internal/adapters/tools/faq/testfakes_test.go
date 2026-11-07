package faq_test

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/components/embedding"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
)

// fakeSearcher is a testfake for driven.FAQSearcher recording the query
// embedding and k it was called with (mirrors the chat usecase fake style).
type fakeSearcher struct {
	mu        sync.Mutex
	calls     int
	lastQuery []float32
	lastK     int
	hits      []driven.FaqHit
	err       error
}

func (f *fakeSearcher) Search(_ context.Context, queryEmbedding []float32, k int) ([]driven.FaqHit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastQuery = queryEmbedding
	f.lastK = k
	if f.err != nil {
		return nil, f.err
	}
	return f.hits, nil
}

// fakeEmbedder is a testfake for embedding.Embedder recording the texts it
// was called with.
type fakeEmbedder struct {
	mu        sync.Mutex
	calls     int
	lastTexts []string
	vecs      [][]float64
	err       error
}

func (f *fakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastTexts = texts
	if f.err != nil {
		return nil, f.err
	}
	return f.vecs, nil
}
