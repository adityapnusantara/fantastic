package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
)

// FaqIndexStore tests run against the real pgvector database through the
// package TestMain, like faq_search_test.go. They prove the idempotency
// contract: re-applying a batch leaves identical rows; applying a
// changed batch replaces (never duplicates) the affected rows.

func faqTestDoc(hash, url, title string, scrapedAt time.Time) driven.FaqDocumentRow {
	return driven.FaqDocumentRow{ContentHash: hash, SourceURL: url, Title: title, ScrapedAt: scrapedAt}
}

func faqTestChunk(docHash string, index int, text, hash string, dims int) driven.FaqChunkRow {
	embedding := make([]float32, dims)
	embedding[index%dims] = 1
	return driven.FaqChunkRow{DocContentHash: docHash, Index: index, Text: text, ContentHash: hash, Embedding: embedding}
}

func faqRowCounts(t *testing.T) (docs int, chunks int, dupHashes int) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, testPool().QueryRow(ctx, `SELECT count(*) FROM faq_documents`).Scan(&docs))
	require.NoError(t, testPool().QueryRow(ctx, `SELECT count(*) FROM faq_chunks`).Scan(&chunks))
	require.NoError(t, testPool().QueryRow(ctx,
		`SELECT count(*) - count(DISTINCT content_hash) FROM faq_chunks`).Scan(&dupHashes))
	return
}

func TestFaqIndexUpsertIsIdempotent(t *testing.T) {
	truncateFaq(t)
	ctx := context.Background()
	store := NewFaqIndexStore(testPool())

	scraped := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	batch := driven.FaqIndexBatch{
		Documents: []driven.FaqDocumentRow{
			faqTestDoc("doc-hash-a", "https://www.linkaja.id/news/art-a", "FAQ LinkAja", scraped),
			faqTestDoc("doc-hash-b", "https://www.linkaja.id/news/art-b", "Syariah", scraped),
		},
		Chunks: []driven.FaqChunkRow{
			faqTestChunk("doc-hash-a", 0, "chunk a1", "hash-a1", 1536),
			faqTestChunk("doc-hash-a", 1, "chunk a2", "hash-a2", 1536),
			faqTestChunk("doc-hash-b", 0, "chunk b1", "hash-b1", 1536),
		},
	}

	require.NoError(t, store.Index(ctx, batch))
	docs, chunks, dups := faqRowCounts(t)
	assert.Equal(t, 2, docs, "two article documents")
	assert.Equal(t, 3, chunks, "three chunks")
	assert.Equal(t, 0, dups, "no duplicate chunk hashes")

	// Re-apply the identical batch: same counts, no duplicates.
	require.NoError(t, store.Index(ctx, batch))
	docs, chunks, dups = faqRowCounts(t)
	assert.Equal(t, 2, docs, "re-run must not add documents")
	assert.Equal(t, 3, chunks, "re-run must not add chunks")
	assert.Equal(t, 0, dups, "re-run must not duplicate chunk hashes")

	// Re-apply with one chunk's content changed (windowing drift): the
	// renumbered index must not trip UNIQUE (document_id, chunk_index),
	// the changed chunk replaces its row, and the dropped chunk is gone.
	batch.Chunks = []driven.FaqChunkRow{
		faqTestChunk("doc-hash-a", 0, "chunk a1", "hash-a1", 1536),
		faqTestChunk("doc-hash-a", 1, "chunk a2 windowed", "hash-a2w", 1536),
		faqTestChunk("doc-hash-a", 2, "chunk a3 windowed", "hash-a3w", 1536),
		faqTestChunk("doc-hash-b", 0, "chunk b1", "hash-b1", 1536),
	}
	require.NoError(t, store.Index(ctx, batch))
	docs, chunks, dups = faqRowCounts(t)
	assert.Equal(t, 2, docs)
	assert.Equal(t, 4, chunks, "renumbered batch replaces cleanly")
	assert.Equal(t, 0, dups)

	var text string
	require.NoError(t, testPool().QueryRow(ctx,
		`SELECT chunk_text FROM faq_chunks WHERE content_hash = 'hash-a2w'`).Scan(&text))
	assert.Equal(t, "chunk a2 windowed", text)

	var gone int
	require.NoError(t, testPool().QueryRow(ctx,
		`SELECT count(*) FROM faq_chunks WHERE content_hash = 'hash-a2'`).Scan(&gone))
	assert.Equal(t, 0, gone, "the dropped chunk hash must be deleted (exact replace)")

	// Chunk must sit at its final, non-scratch index.
	var idx int
	require.NoError(t, testPool().QueryRow(ctx,
		`SELECT chunk_index FROM faq_chunks WHERE content_hash = 'hash-a3w'`).Scan(&idx))
	assert.Equal(t, 2, idx, "final chunk_index after the scratch shift")
}

func TestFaqIndexReplacesSupersededDocument(t *testing.T) {
	truncateFaq(t)
	ctx := context.Background()
	store := NewFaqIndexStore(testPool())
	scraped := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)

	v1 := driven.FaqIndexBatch{
		Documents: []driven.FaqDocumentRow{faqTestDoc("doc-hash-v1", "https://www.linkaja.id/news/art", "FAQ LinkAja", scraped)},
		Chunks:    []driven.FaqChunkRow{faqTestChunk("doc-hash-v1", 0, "old content", "hash-old", 1536)},
	}
	require.NoError(t, store.Index(ctx, v1))

	// The article's content changes -> new derived document hash; the
	// stale document row (and its chunks, via CASCADE) must go.
	v2 := driven.FaqIndexBatch{
		Documents: []driven.FaqDocumentRow{faqTestDoc("doc-hash-v2", "https://www.linkaja.id/news/art", "FAQ LinkAja", scraped)},
		Chunks:    []driven.FaqChunkRow{faqTestChunk("doc-hash-v2", 0, "new content", "hash-new", 1536)},
	}
	require.NoError(t, store.Index(ctx, v2))

	docs, chunks, dups := faqRowCounts(t)
	assert.Equal(t, 1, docs, "the superseded document row must be garbage-collected")
	assert.Equal(t, 1, chunks, "the stale chunk must be gone, the new one present")
	assert.Equal(t, 0, dups)

	var docHash, text string
	var chunkDocMatches bool
	require.NoError(t, testPool().QueryRow(ctx,
		`SELECT d.content_hash, c.document_id = d.id, c.chunk_text
		 FROM faq_chunks c JOIN faq_documents d ON d.id = c.document_id`).
		Scan(&docHash, &chunkDocMatches, &text))
	assert.Equal(t, "doc-hash-v2", docHash)
	assert.True(t, chunkDocMatches, "chunk must reference the surviving document row")
	assert.Equal(t, "new content", text)
}

func TestFaqIndexRejectsUnknownDocHash(t *testing.T) {
	truncateFaq(t)
	store := NewFaqIndexStore(testPool())
	batch := driven.FaqIndexBatch{
		Documents: []driven.FaqDocumentRow{faqTestDoc("doc-hash-a", "https://www.linkaja.id/news/art-a", "", time.Now())},
		Chunks:    []driven.FaqChunkRow{faqTestChunk("doc-hash-unknown", 0, "orphan", "hash-orphan", 1536)},
	}
	err := store.Index(context.Background(), batch)
	require.Error(t, err, "a chunk referencing an unknown document hash must fail loudly")
	assert.Contains(t, err.Error(), "unknown document hash")
}

func TestFaqIndexRetrievableViaFaqSearchStore(t *testing.T) {
	// End-to-end within the package: an indexed chunk is retrievable by
	// the 2.3 FAQSearcher along its own embedding direction.
	truncateFaq(t)
	ctx := context.Background()
	store := NewFaqIndexStore(testPool())
	scraped := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)

	batch := driven.FaqIndexBatch{
		Documents: []driven.FaqDocumentRow{faqTestDoc("doc-hash-a", "https://www.linkaja.id/news/art-a", "FAQ LinkAja", scraped)},
		Chunks:    []driven.FaqChunkRow{faqTestChunk("doc-hash-a", 0, "chunk a1", "hash-a1", 1536)},
	}
	require.NoError(t, store.Index(ctx, batch))

	search := NewFaqSearchStore(testPool())
	query := make([]float32, 1536)
	query[0] = 1
	hits, err := search.Search(ctx, query, 5)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "chunk a1", hits[0].ChunkText)
	assert.Equal(t, "hash-a1", hits[0].ContentHash)
	assert.Equal(t, "https://www.linkaja.id/news/art-a", hits[0].SourceURL)
}
