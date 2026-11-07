DROP INDEX IF EXISTS idx_conversations_agent;
ALTER TABLE conversations DROP COLUMN IF EXISTS agent_id;
DROP TABLE IF EXISTS agents;
