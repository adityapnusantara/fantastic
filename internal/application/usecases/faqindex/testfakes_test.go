package faqindex

import (
	"context"

	"github.com/cloudwego/eino/components/embedding"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
)

// fakeEmbedder records batch sizes and returns deterministic vectors:
// the unit vector along dimension hash(text) % dims.
type fakeEmbedder struct {
	dims  int
	calls []int
	err   error
}

func (f *fakeEmbedder) EmbedStrings(_ context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.calls = append(f.calls, len(texts))
	out := make([][]float64, len(texts))
	for i, t := range texts {
		v := make([]float64, f.dims)
		sum := 0
		for _, b := range []byte(t) {
			sum += int(b)
		}
		v[sum%f.dims] = 1
		out[i] = v
	}
	return out, nil
}

// fakeIndexer records the batches it receives.
type fakeIndexer struct {
	batches []driven.FaqIndexBatch
	err     error
}

func (f *fakeIndexer) Index(_ context.Context, batch driven.FaqIndexBatch) error {
	if f.err != nil {
		return f.err
	}
	f.batches = append(f.batches, batch)
	return nil
}
