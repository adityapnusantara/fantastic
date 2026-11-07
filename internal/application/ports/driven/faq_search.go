package driven

import "context"

// FaqHit is one vector-similarity retrieval result from the FAQ store
// (design D7: the tool result carries chunk text + source URL + title +
// score so the agent can cite).
type FaqHit struct {
	// ChunkText is the embedded chunk text (design D4: question + answer
	// concatenated, or a long-form window).
	ChunkText string

	// ChunkIndex is the chunk's zero-based position within its document.
	ChunkIndex int

	// ContentHash is the chunk-level hash — the dedupe key and the eval
	// scoring pin (design D5/D11).
	ContentHash string

	// SourceURL is the document page the chunk came from.
	SourceURL string

	// Title is the document title (may be empty; faq_documents.title
	// defaults to '').
	Title string

	// Score is the cosine similarity: 1 - cosine distance, so 1.0 means
	// identical direction, 0 means orthogonal, and lower (possibly
	// negative) means less similar. Hits are ordered best first.
	Score float64
}

// FAQSearcher retrieves FAQ chunks by embedding similarity (design D3:
// pure vector top-k, cosine, hand-rolled pgx + SQL).
type FAQSearcher interface {
	// Search returns the top-k chunks most cosine-similar to
	// queryEmbedding, best match first. k <= 0 returns no hits.
	// queryEmbedding must match the stored model's dimension (1536,
	// design D2) or the query fails.
	Search(ctx context.Context, queryEmbedding []float32, k int) ([]FaqHit, error)
}
