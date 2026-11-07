# Proposal

## Why

The service currently hardcodes exactly one agent ("fantastic"): one system prompt, one flat tool list, one chat endpoint. Adding an agent today means editing bootstrap wiring and redeploying, and the frontend has no way to choose which persona/toolset it talks to. Multiple independent agents — each with its own prompt, tool subset, and optional model override — selectable by the frontend, turn the service into an agent platform instead of a single bot.

## What Changes

- **BREAKING** `POST /api/v1/chat` is removed and replaced by agent-scoped `POST /api/v1/agents/{agent}/chat` (SSE event format unchanged).
- New `agents` table (migration): TEXT slug id (immutable, used in URL), display name, description, system prompt, nullable model overrides (`model`, `api`, `temperature`, `reasoning_effort`), `tools TEXT[]`, `enabled`, `sort_order`.
- Conversations are bound to one agent: `conversations.agent_id` (FK, `ON DELETE RESTRICT`, backfilled to the default agent); chatting into a conversation owned by a different agent returns 409.
- New managed agent CRUD API: `GET /api/v1/agents` (all authenticated users — listing metadata for agent pickers), `GET|POST|PATCH|DELETE /api/v1/agents/{id}` (admin only), with create-time validation (slug format, prompt required, tool names must exist in the runtime catalog) and 409 on delete when conversations still reference the agent.
- Write operations live-rebuild the agent in a runtime registry: changes via the API take effect immediately; in-flight SSE turns finish on the old runner.
- Admin authorization: the token verifier extracts Casdoor roles into the principal; agent write routes require an admin role (e.g. `fantastic-admin`), read/chat routes stay open to all authenticated users.
- Tool catalog boundary stays in `config.yaml` (MCP servers, local FAQ tool); the DB only assigns existing tools to agents. MCP tools are exposed under server-prefixed catalog names (`<server>.<tool>`) to avoid collisions.
- Static `/admin` page (same repo, same pattern as `/demo`) for agent management; `/demo` gains an agent picker.

## Capabilities

### New Capabilities

- `agent-registry`: Durable agent definitions stored in Postgres, managed through an admin CRUD API with live rebuild, tool assignment validated against the runtime tool catalog, and a static admin page for management.

### Modified Capabilities

- `agent-chat-streaming`: The chat endpoint becomes agent-scoped (`POST /api/v1/agents/{agent}/chat`); the unscoped `POST /api/v1/chat` is removed; the demo page selects an agent before chatting.
- `conversation-memory`: Conversations are bound to exactly one agent (`agent_id`); conversation reads expose the agent binding.
- `casdoor-token-auth`: The verified principal additionally carries roles extracted from the token, enabling role-based authorization of admin routes.
- `faq-rag-search`: FAQ tool availability becomes per-agent: agents whose tool assignment includes `search_faq` provide citation-bearing FAQ answers.

## Impact

- **Code**: `config` (agents seed + catalog naming), `internal/bootstrap/server.go` (tool catalog once, agent registry instead of one static agent), `internal/adapters/mcp` (prefixed named catalog), `internal/application/usecases/chat` (agent name/identity parameterized), new ports/usecases/adapters for agent store + management + admin HTTP, `internal/adapters/auth/casdoor` + `middleware` (roles, `RequireAdmin`), `internal/adapters/http/chat|conversation|demo` (agent-scoped routes, agent in responses, admin page).
- **Database**: migration 0005 — `agents` table + `conversations.agent_id` (FK, backfill, index).
- **APIs**: one route removed (`POST /api/v1/chat`), six added; `GET /api/v1/conversations` and conversation reads gain `agent_id`.
- **Operations**: agents seeded from config on first boot (`INSERT ... ON CONFLICT DO NOTHING`); prompt/tool changes via CRUD need no redeploy, but new MCP servers/catalog entries still require config + restart.
- **Casdoor (manual, outside this repo)**: application token format must be `JWT` (not JWT-Empty) so the `roles` claim reaches the verifier; an admin role (proposed `fantastic-admin`) must exist and be assigned to the operator.
