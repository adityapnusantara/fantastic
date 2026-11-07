package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FaqSearchStore tests run against the real pgvector database through the
// package TestMain (migrations up + pool), like conversation_store_test.go.

// unitBasis builds a 1536-dim unit vector along the given dimension.
func unitBasis(dim int) []float32 {
	vec := make([]float32, 1536)
	vec[dim] = 1
	return vec
}

// seedFaqDoc inserts one faq_documents row and returns its id.
func seedFaqDoc(t *testing.T, sourceURL, title string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testPool().Exec(context.Background(), `
		INSERT INTO faq_documents (id, source_url, title, content_hash)
		VALUES ($1, $2, $3, $4)`,
		id, sourceURL, title, "doc-"+uuid.NewString())
	require.NoError(t, err)
	return id
}

// seedFaqChunk inserts one faq_chunks row with the given embedding.
func seedFaqChunk(t *testing.T, docID uuid.UUID, index int, text string, embedding []float32) {
	t.Helper()
	_, err := testPool().Exec(context.Background(), `
		INSERT INTO faq_chunks (id, document_id, chunk_index, chunk_text, content_hash, embedding)
		VALUES ($1, $2, $3, $4, $5, $6::vector)`,
		uuid.New(), docID, index, text, "chunk-"+uuid.NewString(), faqVectorLiteral(embedding))
	require.NoError(t, err)
}

// truncateFaq clears the FAQ tables between tests.
func truncateFaq(t *testing.T) {
	t.Helper()
	_, err := testPool().Exec(context.Background(), `TRUNCATE faq_chunks, faq_documents CASCADE`)
	require.NoError(t, err)
}

func TestFaqSearchRanksBestChunkFirst(t *testing.T) {
	truncateFaq(t)
	ctx := context.Background()

	docID := seedFaqDoc(t, "https://www.linkaja.id/faq/topup", "Biaya Top Up")
	chunk1 := "Berapa biaya top up LinkAja? Top up gratis untuk semua channel."
	chunk2 := "Cara daftar LinkAja: unduh aplikasi dan registrasi nomor HP."
	seedFaqChunk(t, docID, 0, chunk1, unitBasis(0))
	seedFaqChunk(t, docID, 1, chunk2, unitBasis(1))

	store := NewFaqSearchStore(testPool())

	// Query exactly along chunk1's direction: cosine distance 0, score ~1.
	hits, err := store.Search(ctx, unitBasis(0), 5)
	require.NoError(t, err)
	require.Len(t, hits, 2)

	assert.Equal(t, chunk1, hits[0].ChunkText, "closest chunk ranks first")
	assert.InDelta(t, 1.0, hits[0].Score, 1e-6, "identical direction scores ~1.0")
	assert.Equal(t, "https://www.linkaja.id/faq/topup", hits[0].SourceURL)
	assert.Equal(t, "Biaya Top Up", hits[0].Title)
	assert.Equal(t, 0, hits[0].ChunkIndex)
	assert.NotEmpty(t, hits[0].ContentHash, "chunk content_hash must be carried for eval pinning (D11)")

	// chunk2 is orthogonal: cosine distance 1, score ~0.
	assert.InDelta(t, 0.0, hits[1].Score, 1e-6)
	assert.Equal(t, chunk2, hits[1].ChunkText)
	assert.Equal(t, 1, hits[1].ChunkIndex)
}

func TestFaqSearchHonorsLimit(t *testing.T) {
	truncateFaq(t)
	ctx := context.Background()

	docID := seedFaqDoc(t, "https://www.linkaja.id/faq/topup", "Top Up")
	seedFaqChunk(t, docID, 0, "chunk a", unitBasis(0))
	seedFaqChunk(t, docID, 1, "chunk b", unitBasis(1))

	store := NewFaqSearchStore(testPool())

	hits, err := store.Search(ctx, unitBasis(0), 1)
	require.NoError(t, err)
	require.Len(t, hits, 1, "LIMIT k=1 returns exactly one hit")
	assert.Equal(t, "chunk a", hits[0].ChunkText, "the best match survives the limit")

	// k = 0 yields no hits, no error.
	empty, err := store.Search(ctx, unitBasis(0), 0)
	require.NoError(t, err)
	assert.Empty(t, empty)
}
