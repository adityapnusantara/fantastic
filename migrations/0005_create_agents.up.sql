CREATE TABLE agents (
    id               TEXT PRIMARY KEY,
    display_name     TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    icon             TEXT NOT NULL DEFAULT '',
    system_prompt    TEXT NOT NULL,
    model            TEXT,
    api              TEXT CHECK (api IN ('responses', 'chat')),
    temperature      REAL,
    reasoning_effort TEXT,
    tools            TEXT[] NOT NULL DEFAULT '{}',
    enabled          BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order       INT NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Default agent seeded first: conversations.agent_id needs the FK anchor,
-- and the canonical FAQ-answer prompt is baked in (design D8).
INSERT INTO agents (id, display_name, tools, system_prompt) VALUES
    ('general', 'General Assistant', ARRAY['search_faq']::TEXT[], '
FAQ answering rules (search_faq tool):
- Answer in the user''s language; Indonesian by default.
- For any question about LinkAja (products, features, fees, how-tos, account matters), call search_faq BEFORE answering and answer from its results.
- When you answer from search_faq results, cite the linkaja.id source_url of the snippet(s) you used.
- Only cite source URLs returned by search_faq — never invent or alter links.
- If search_faq returns no relevant results, say that the FAQ knowledge base does not cover it; do not present your own knowledge as if it came from the FAQ.')
ON CONFLICT (id) DO NOTHING;

ALTER TABLE conversations ADD COLUMN agent_id TEXT REFERENCES agents(id) ON DELETE RESTRICT;
UPDATE conversations SET agent_id = 'general';
ALTER TABLE conversations ALTER COLUMN agent_id SET NOT NULL;
CREATE INDEX idx_conversations_agent ON conversations (user_id, agent_id, updated_at DESC);
