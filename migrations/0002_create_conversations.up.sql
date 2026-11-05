CREATE TABLE conversations (
    id                UUID PRIMARY KEY,
    user_id           TEXT NOT NULL,
    title             TEXT NOT NULL DEFAULT '',
    rolling_summary   TEXT NOT NULL DEFAULT '',
    summary_until_seq BIGINT NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_conversations_user ON conversations (user_id, updated_at DESC);
