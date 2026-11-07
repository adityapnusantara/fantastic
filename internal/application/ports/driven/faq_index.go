package driven

import (
	"context"
	"time"
)

// FaqDocumentRow is one faq_documents upsert input: one row per article
// (source_url is the article identity; content_hash is the derived
// article hash, see faqindex.DocumentContentHash).
type FaqDocumentRow struct {
	ContentHash string
	SourceURL   string
	Title       string
	ScrapedAt   time.Time
}

// FaqChunkRow is one faq_chunks upsert input. DocContentHash resolves
// the chunk's document inside the store (documents are upserted in the
// same batch). Embedding must carry the stored dimension (1536, D2).
type FaqChunkRow struct {
	DocContentHash string
	Index          int
	Text           string
	ContentHash    string
	Embedding      []float32
}

// FaqIndexBatch is one atomically applied index write: the full doc and
// chunk set of a corpus indexing run.
type FaqIndexBatch struct {
	Documents []FaqDocumentRow
	Chunks    []FaqChunkRow
}

// FAQIndexer persists FAQ documents and chunk embeddings idempotently:
// applying the same batch twice leaves the same rows (upserts keyed on
// content_hash); applying a changed batch replaces the affected rows.
type FAQIndexer interface {
	Index(ctx context.Context, batch FaqIndexBatch) error
}
