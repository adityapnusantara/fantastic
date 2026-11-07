package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
)

// FaqSearchStore is the pgx-backed FAQSearcher over faq_chunks +
// faq_documents (migration 0004), ranking by pgvector cosine distance
// against the HNSW index idx_faq_chunks_embedding (design D3).
type FaqSearchStore struct {
	pool *pgxpool.Pool
}

// NewFaqSearchStore builds a FaqSearchStore over the given pool.
func NewFaqSearchStore(pool *pgxpool.Pool) *FaqSearchStore {
	return &FaqSearchStore{pool: pool}
}

// compile-time port conformance check.
var _ driven.FAQSearcher = (*FaqSearchStore)(nil)

// Search returns the top-k FAQ chunks most cosine-similar to
// queryEmbedding, best match first, joined with faq_documents for
// source_url and title.
//
// Score convention: score = 1 - cosine distance (<=>), so 1.0 = identical
// direction, 0 = orthogonal, negative = opposite. LIMIT k is applied in
// SQL (k <= 0 returns no hits). The query embedding must have the stored
// dimension (1536, design D2).
func (s *FaqSearchStore) Search(ctx context.Context, queryEmbedding []float32, k int) ([]driven.FaqHit, error) {
	if k <= 0 {
		return []driven.FaqHit{}, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT c.chunk_text, c.chunk_index, c.content_hash,
		       d.source_url, d.title,
		       c.embedding <=> $1::vector AS distance
		FROM faq_chunks c
		JOIN faq_documents d ON d.id = c.document_id
		ORDER BY c.embedding <=> $1::vector
		LIMIT $2`, faqVectorLiteral(queryEmbedding), k)
	if err != nil {
		return nil, fmt.Errorf("search faq chunks: %w", err)
	}
	defer rows.Close()

	hits := make([]driven.FaqHit, 0, k)
	for rows.Next() {
		var h driven.FaqHit
		var distance float64
		if err := rows.Scan(&h.ChunkText, &h.ChunkIndex, &h.ContentHash, &h.SourceURL, &h.Title, &distance); err != nil {
			return nil, fmt.Errorf("scan faq hit: %w", err)
		}
		h.Score = 1 - distance
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate faq hits: %w", err)
	}
	return hits, nil
}

// faqVectorLiteral renders an embedding as pgvector's text input format
// ("[1,2,3]"), bound as $n::vector. Hand-rolled per design D3: no eino
// pgvector component exists and no pgvector-go dependency is needed.
func faqVectorLiteral(vec []float32) string {
	var sb strings.Builder
	sb.Grow(2 + len(vec)*9)
	sb.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	sb.WriteByte(']')
	return sb.String()
}
