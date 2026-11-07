package faqindex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/cloudwego/eino/components/embedding"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
)

// Pipeline runs the corpus -> chunks -> embeddings -> pgvector ->
// manifest flow (task 2.2). It depends on the eino embedding.Embedder
// (satisfied directly by eino-ext components/embedding/openai) and the
// driven.FAQIndexer store (satisfied by the pgx-backed postgres
// adapter), so 4.1/4.2 can absorb both without reshaping this package.
type Pipeline struct {
	embedder embedding.Embedder
	store    driven.FAQIndexer
	// batchSize caps one embedding request; the API accepts arrays and
	// the task caps batches at 100 inputs.
	batchSize int
}

// NewPipeline builds a pipeline. batchSize <= 0 falls back to 100.
func NewPipeline(embedder embedding.Embedder, store driven.FAQIndexer, batchSize int) *Pipeline {
	if batchSize <= 0 || batchSize > 100 {
		batchSize = 100
	}
	return &Pipeline{embedder: embedder, store: store, batchSize: batchSize}
}

// IndexOptions carries the run-level settings read from the faq:
// config section.
type IndexOptions struct {
	// Model is the embedding model id (D2) — recorded in the manifest.
	Model string
	// Dims is the stored vector dimension (1536); every returned
	// embedding must match or the run fails.
	Dims int
	// ManifestPath is where the index manifest (spec 6.2) is written.
	ManifestPath string
	// Now overrides manifest indexed_at (tests); nil means time.Now.
	Now func() time.Time
}

// Result summarizes one indexing run.
type Result struct {
	Documents     int // faq_documents rows in the batch (one per article)
	Chunks        int // faq_chunks rows in the batch (units + window splits)
	Units         int // corpus units processed
	WindowedUnits int // units that used the D4 window fallback
	EmbedCalls    int // embedding API calls issued
}

// IndexCorpus chunks + embeds the corpus, upserts the store, and writes
// the manifest. Idempotent end to end: the same corpus yields the same
// rows and an identical manifest modulo indexed_at.
func (p *Pipeline) IndexCorpus(ctx context.Context, corpus *Corpus, opts IndexOptions) (*Result, error) {
	if opts.Model == "" {
		return nil, fmt.Errorf("index: embedding model is not configured")
	}
	if opts.Dims <= 0 {
		return nil, fmt.Errorf("index: embedding dims not configured")
	}

	// Chunk every unit, grouping per article (one faq_documents row per
	// source_url; units of an article are contiguous in corpus order).
	type article struct {
		sourceURL  string
		title      string
		scrapedAt  time.Time
		unitHashes []string
	}
	articles := make([]*article, 0, 64)
	byURL := make(map[string]*article, 64)
	type pendingChunk struct {
		row      driven.FaqChunkRow
		unitID   string
		srcURL   string
		idx      int  // chunk index within the unit (manifest)
		windowed bool // came from the D4 window fallback
	}
	var pending []pendingChunk
	windowed := 0

	for i := range corpus.Units {
		u := &corpus.Units[i]
		chunks, err := ChunkUnit(u)
		if err != nil {
			return nil, err
		}
		if len(chunks) > 1 {
			windowed++
		}
		a, ok := byURL[u.SourceURL]
		if !ok {
			a = &article{sourceURL: u.SourceURL, title: u.Category}
			byURL[u.SourceURL] = a
			articles = append(articles, a)
		}
		a.unitHashes = append(a.unitHashes, u.ContentHash)
		if t, err := parseRFC3339(u.ScrapedAt); err == nil && t.After(a.scrapedAt) {
			a.scrapedAt = t
		}
		for _, c := range chunks {
			pending = append(pending, pendingChunk{
				row: driven.FaqChunkRow{
					DocContentHash: "", // filled once the article hash is known
					Index:          0,  // per-article index assigned below
					Text:           c.Text,
					ContentHash:    c.ContentHash,
				},
				unitID:   u.ID,
				srcURL:   u.SourceURL,
				idx:      c.Index,
				windowed: len(chunks) > 1,
			})
		}
	}
	for _, a := range articles {
		a.scrapedAt = a.scrapedAt.Truncate(time.Second).UTC()
	}

	// Per-article running chunk index (faq_chunks.chunk_index is unique
	// per document; the manifest keeps its own per-unit indices).
	perArticle := make(map[string]int, len(articles))
	unitChunks := make(map[string][]Chunk, len(corpus.Units))
	for i := range pending {
		pc := &pending[i]
		a := byURL[pc.srcURL]
		pc.row.DocContentHash = DocumentContentHash(a.unitHashes)
		pc.row.Index = perArticle[a.sourceURL]
		perArticle[a.sourceURL]++
		unitChunks[pc.unitID] = append(unitChunks[pc.unitID], Chunk{
			Index:       pc.idx,
			Text:        pc.row.Text,
			ContentHash: pc.row.ContentHash,
			Windowed:    pc.windowed,
		})
	}

	// Embed every chunk text in bounded batches, order-preserving.
	texts := make([]string, len(pending))
	for i := range pending {
		texts[i] = pending[i].row.Text
	}
	embeddings, calls, err := p.embedAll(ctx, texts, opts.Dims)
	if err != nil {
		return nil, err
	}
	for i := range pending {
		pending[i].row.Embedding = embeddings[i]
	}

	docs := make([]driven.FaqDocumentRow, 0, len(articles))
	for _, a := range articles {
		docs = append(docs, driven.FaqDocumentRow{
			ContentHash: DocumentContentHash(a.unitHashes),
			SourceURL:   a.sourceURL,
			Title:       a.title,
			ScrapedAt:   a.scrapedAt,
		})
	}
	chunkRows := make([]driven.FaqChunkRow, len(pending))
	for i := range pending {
		chunkRows[i] = pending[i].row
	}
	if err := p.store.Index(ctx, driven.FaqIndexBatch{Documents: docs, Chunks: chunkRows}); err != nil {
		return nil, fmt.Errorf("index: store batch: %w", err)
	}

	if err := p.writeManifest(corpus, unitChunks, opts); err != nil {
		return nil, err
	}
	return &Result{
		Documents:     len(docs),
		Chunks:        len(chunkRows),
		Units:         len(corpus.Units),
		WindowedUnits: windowed,
		EmbedCalls:    calls,
	}, nil
}

// embedAll runs the batching loop (<=100 inputs per API call) and
// validates the response shape.
func (p *Pipeline) embedAll(ctx context.Context, texts []string, dims int) ([][]float32, int, error) {
	embeddings := make([][]float32, 0, len(texts))
	calls := 0
	for start := 0; start < len(texts); start += p.batchSize {
		end := start + p.batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]
		vecs, err := p.embedder.EmbedStrings(ctx, batch)
		if err != nil {
			return nil, calls, fmt.Errorf("index: embed batch %d (%d inputs): %w", calls, len(batch), err)
		}
		if len(vecs) != len(batch) {
			return nil, calls, fmt.Errorf("index: embed batch %d returned %d vectors for %d inputs", calls, len(vecs), len(batch))
		}
		for i, v := range vecs {
			if len(v) != dims {
				return nil, calls, fmt.Errorf("index: embed batch %d input %d returned %d dims, want %d (column is a (model, dims) pair, design D2)", calls, start+i, len(v), dims)
			}
			f32 := make([]float32, len(v))
			for j, x := range v {
				f32[j] = float32(x)
			}
			embeddings = append(embeddings, f32)
		}
		calls++
	}
	return embeddings, calls, nil
}

// manifest types follow inventory spec 6.2 verbatim.
type manifestChunk struct {
	ChunkIndex  int    `json:"chunk_index"`
	ContentHash string `json:"content_hash"`
}

type manifestDocument struct {
	SourceURL   string          `json:"source_url"`
	ContentHash string          `json:"content_hash"`
	ChunkCount  int             `json:"chunk_count"`
	Chunks      []manifestChunk `json:"chunks"`
}

type manifest struct {
	SchemaVersion int                `json:"schema_version"`
	IndexedAt     string             `json:"indexed_at"`
	Model         string             `json:"model"`
	Dims          int                `json:"dims"`
	Documents     []manifestDocument `json:"documents"`
}

// writeManifest emits data/corpus/index-manifest.json (spec 6.2): one
// document entry per corpus unit in corpus order, keyed by the
// (source_url, unit content_hash) pair, with the per-unit chunk hashes
// actually stored by this run.
func (p *Pipeline) writeManifest(corpus *Corpus, unitChunks map[string][]Chunk, opts IndexOptions) error {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now() }
	}
	m := manifest{
		SchemaVersion: 1,
		IndexedAt:     now().UTC().Format(time.RFC3339),
		Model:         opts.Model,
		Dims:          opts.Dims,
		Documents:     make([]manifestDocument, 0, len(corpus.Units)),
	}
	for i := range corpus.Units {
		u := &corpus.Units[i]
		chunks := unitChunks[u.ID]
		entry := manifestDocument{
			SourceURL:   u.SourceURL,
			ContentHash: u.ContentHash,
			ChunkCount:  len(chunks),
			Chunks:      make([]manifestChunk, 0, len(chunks)),
		}
		for _, c := range chunks {
			entry.Chunks = append(entry.Chunks, manifestChunk{ChunkIndex: c.Index, ContentHash: c.ContentHash})
		}
		m.Documents = append(m.Documents, entry)
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("index: encode manifest: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(opts.ManifestPath, raw, 0o644); err != nil {
		return fmt.Errorf("index: write manifest %s: %w", opts.ManifestPath, err)
	}
	return nil
}

// parseRFC3339 parses corpus timestamps; zero time on failure.
func parseRFC3339(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}
