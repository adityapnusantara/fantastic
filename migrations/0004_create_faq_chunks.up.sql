-- FAQ RAG corpus (OpenSpec change add-faq-rag-search, D2/D3/D5).
-- Additive only: conversations / messages are untouched.

CREATE TABLE faq_documents (
    id           UUID PRIMARY KEY,
    source_url   TEXT NOT NULL,
    title        TEXT NOT NULL DEFAULT '',
    content_hash TEXT NOT NULL,
    scraped_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- content_hash is the dedupe key: re-indexing upserts on it.
CREATE UNIQUE INDEX idx_faq_documents_content_hash ON faq_documents (content_hash);

CREATE TABLE faq_chunks (
    id           UUID PRIMARY KEY,
    document_id  UUID NOT NULL REFERENCES faq_documents (id) ON DELETE CASCADE,
    chunk_index  INTEGER NOT NULL,
    chunk_text   TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    embedding    VECTOR(1536) NOT NULL
);

-- One chunk per document slot.
CREATE UNIQUE INDEX idx_faq_chunks_doc_index ON faq_chunks (document_id, chunk_index);

-- Chunk-level content hash: the index manifest (D5) and the eval scoring rule
-- (D11) pin chunks by it; unique so re-indexing upserts without duplicates.
CREATE UNIQUE INDEX idx_faq_chunks_content_hash ON faq_chunks (content_hash);

-- Cosine similarity lookup (D3). The vector column is a (model, dimension)
-- pair: OpenAI text-embedding-3-small, 1536 dims. A different model means a
-- NEW column + migration, never reuse of this one (D2).
CREATE INDEX idx_faq_chunks_embedding ON faq_chunks USING hnsw (embedding vector_cosine_ops);

COMMENT ON COLUMN faq_chunks.embedding IS 'OpenAI text-embedding-3-small embeddings (1536 dims); a different model requires a new column + migration (design D2)';
