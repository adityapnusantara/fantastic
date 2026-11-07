package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
)

// FaqIndexStore implements driven.FAQIndexer over faq_documents +
// faq_chunks (migration 0004) with pgx + SQL (design D3: hand-rolled, no
// eino pgvector component exists).
//
// Idempotency (spec: re-indexing replaces without duplicating):
//   - documents upsert ON CONFLICT (content_hash), one row per article;
//   - chunks upsert ON CONFLICT (content_hash) DO UPDATE, which also
//     moves a chunk between documents when content drifts;
//   - because (document_id, chunk_index) is UNIQUE and a re-run may
//     renumber windows, all chunk_index values of the affected documents
//     are shifted to a negative scratch range before the upserts;
//   - chunks of affected documents that the batch no longer contains are
//     deleted, and stale document rows (same source_url, superseded
//     content_hash) are garbage-collected (chunks via ON DELETE CASCADE).
type FaqIndexStore struct {
	pool *pgxpool.Pool
}

// NewFaqIndexStore builds a FaqIndexStore over the given pool.
func NewFaqIndexStore(pool *pgxpool.Pool) *FaqIndexStore {
	return &FaqIndexStore{pool: pool}
}

// compile-time port conformance check.
var _ driven.FAQIndexer = (*FaqIndexStore)(nil)

// Index applies one batch atomically: every document and chunk of the
// run lands or none of it does.
func (s *FaqIndexStore) Index(ctx context.Context, batch driven.FaqIndexBatch) error {
	if len(batch.Documents) == 0 {
		return fmt.Errorf("faq index batch: no documents")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("faq index batch: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // committed below; rollback after commit is a no-op

	docIDs, err := upsertFaqDocuments(ctx, tx, batch.Documents)
	if err != nil {
		return err
	}
	if err := shiftFaqChunkIndices(ctx, tx, docIDs); err != nil {
		return err
	}
	if err := upsertFaqChunks(ctx, tx, batch.Chunks, docIDs); err != nil {
		return err
	}
	if err := deleteStaleFaqChunks(ctx, tx, docIDs, batch.Chunks); err != nil {
		return err
	}
	if err := deleteStaleFaqDocuments(ctx, tx, batch.Documents); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("faq index batch: commit: %w", err)
	}
	return nil
}

// upsertFaqDocuments upserts the batch documents and returns a map of
// content_hash -> id for the chunks to reference.
func upsertFaqDocuments(ctx context.Context, tx pgx.Tx, docs []driven.FaqDocumentRow) (map[string]uuid.UUID, error) {
	ids := make(map[string]uuid.UUID, len(docs))
	for _, d := range docs {
		if d.ContentHash == "" || d.SourceURL == "" {
			return nil, fmt.Errorf("faq index batch: document row missing content_hash or source_url")
		}
		var id uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO faq_documents (id, source_url, title, content_hash, scraped_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (content_hash) DO UPDATE
			SET source_url = EXCLUDED.source_url,
			    title = EXCLUDED.title,
			    scraped_at = EXCLUDED.scraped_at
			RETURNING id`,
			uuid.New(), d.SourceURL, d.Title, d.ContentHash, d.ScrapedAt).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("faq index batch: upsert document %s: %w", d.SourceURL, err)
		}
		ids[d.ContentHash] = id
	}
	return ids, nil
}

// shiftFaqChunkIndices moves every chunk_index of the affected documents
// into a negative scratch range so the upserts below can write final
// indices without tripping the UNIQUE (document_id, chunk_index)
// constraint when windowing renumbers chunks across runs.
func shiftFaqChunkIndices(ctx context.Context, tx pgx.Tx, docIDs map[string]uuid.UUID) error {
	ids := uuidSlice(docIDs)
	_, err := tx.Exec(ctx, `
		UPDATE faq_chunks SET chunk_index = -1 - chunk_index
		WHERE document_id = ANY($1)`, ids)
	if err != nil {
		return fmt.Errorf("faq index batch: shift chunk indices: %w", err)
	}
	return nil
}

// upsertFaqChunks upserts every batch chunk, resolving its document by
// content_hash (same tx — the documents were just upserted).
func upsertFaqChunks(ctx context.Context, tx pgx.Tx, chunks []driven.FaqChunkRow, docIDs map[string]uuid.UUID) error {
	b := &pgx.Batch{}
	for _, c := range chunks {
		docID, ok := docIDs[c.DocContentHash]
		if !ok {
			return fmt.Errorf("faq index batch: chunk %s references unknown document hash %s", c.ContentHash, c.DocContentHash)
		}
		if len(c.Embedding) == 0 {
			return fmt.Errorf("faq index batch: chunk %s has an empty embedding", c.ContentHash)
		}
		b.Queue(`
			INSERT INTO faq_chunks (id, document_id, chunk_index, chunk_text, content_hash, embedding)
			VALUES ($1, $2, $3, $4, $5, $6::vector)
			ON CONFLICT (content_hash) DO UPDATE
			SET document_id = EXCLUDED.document_id,
			    chunk_index = EXCLUDED.chunk_index,
			    chunk_text = EXCLUDED.chunk_text,
			    embedding = EXCLUDED.embedding`,
			uuid.New(), docID, c.Index, c.Text, c.ContentHash, faqVectorLiteral(c.Embedding))
	}
	results := tx.SendBatch(ctx, b)
	defer results.Close()
	for range chunks {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("faq index batch: upsert chunk: %w", err)
		}
	}
	return nil
}

// deleteStaleFaqChunks drops chunks of the affected documents that this
// batch no longer contains (corpus shrank / re-windowed), keeping exact
// replace semantics per article.
func deleteStaleFaqChunks(ctx context.Context, tx pgx.Tx, docIDs map[string]uuid.UUID, chunks []driven.FaqChunkRow) error {
	hashes := make([]string, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.ContentHash
	}
	_, err := tx.Exec(ctx, `
		DELETE FROM faq_chunks
		WHERE document_id = ANY($1) AND NOT (content_hash = ANY($2))`,
		uuidSlice(docIDs), hashes)
	if err != nil {
		return fmt.Errorf("faq index batch: delete stale chunks: %w", err)
	}
	return nil
}

// deleteStaleFaqDocuments drops superseded document rows: same
// source_url as a batch document but a content_hash the batch no longer
// carries (article content changed -> new derived document hash). Their
// chunks go via ON DELETE CASCADE. URLs that left the corpus entirely
// are out of a corpus-scoped run's scope (noted for task 4.1).
func deleteStaleFaqDocuments(ctx context.Context, tx pgx.Tx, docs []driven.FaqDocumentRow) error {
	urls := make([]string, len(docs))
	hashes := make([]string, len(docs))
	for i, d := range docs {
		urls[i] = d.SourceURL
		hashes[i] = d.ContentHash
	}
	_, err := tx.Exec(ctx, `
		DELETE FROM faq_documents
		WHERE source_url = ANY($1) AND NOT (content_hash = ANY($2))`,
		urls, hashes)
	if err != nil {
		return fmt.Errorf("faq index batch: delete stale documents: %w", err)
	}
	return nil
}

// uuidSlice flattens a hash->id map into a uuid array for ANY($1).
func uuidSlice(m map[string]uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(m))
	for _, id := range m {
		out = append(out, id)
	}
	return out
}
