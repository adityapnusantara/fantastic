# Design

## Context

`chatusecase.NewService(store, model, tools, cfg)` is already fully parameterized (model, tool list, system prompt) — only its ADK agent name is hardcoded. Bootstrap wires one agent: MCP registry tools plus the local FAQ tool into a flat list, one `POST /api/v1/chat` route, no agent identity anywhere in the API or schema. The tool *infrastructure* (MCP server URLs, tokens, allowlists; FAQ embedding config) lives in `config.yaml`, following the degrade-not-fail rule (`mcp/registry.go`). See `specs/` deltas for the behavior contract; the FAQ design lineage (D9/D10 of add-faq-rag-search) is partially superseded here.

## Goals / Non-Goals

**Goals:**
- N independently selectable agents; the frontend chooses via the URL path
- Agent lifecycle (create/edit/disable/delete) through the API with live effect, no redeploy
- Clear boundary: config = tool catalog + LLM defaults; DB = agent identity, prompt, tool assignment, overrides

**Non-Goals:**
- ADK orchestration between agents (supervisor/router/delegation) — agents stay independent
- Hot management of the tool catalog itself (new MCP servers still need config + restart)
- Per-user or per-conversation agent permissions beyond the admin gate
- An agent-ownership model where one conversation moves between agents

## Decisions

**D1 — Agent selection in the path, not the body.** `POST /api/v1/agents/{agent}/chat`. The agent is a property of the *conversation*, not of a single message; a body field would allow switching agents mid-conversation, silently mixing prompts/toolsets inside one history. Path param makes the mismatch unrepresentable, gives 404 semantics, and reads cleanly in logs/metrics. `POST /api/v1/chat` is removed outright (personal project, one frontend that ships in the same binary).

**D2 — DB owns "who the agent is"; config owns "what tools exist".** `agents` table holds identity, prompt, tool *names*, overrides, `enabled`, `sort_order`. Config keeps MCP server declarations (URLs, headers, `allow_tools`), the FAQ tool, and the global `llm` block. Rejected: all-config (no live management — the reason this change exists); all-DB including MCP endpoints/tokens (secrets and connection infra in the DB, catalog churn coupled to agent edits).

**D3 — Schema (migration 0005).** `agents`: `id TEXT PRIMARY KEY` (slug, immutable — it is in the URL and referenced by `conversations.agent_id`), `display_name`, `description`, `system_prompt`, nullable overrides `model`/`api`/`temperature`/`reasoning_effort` (NULL = global `llm` default), `tools TEXT[] NOT NULL DEFAULT '{}'`, `enabled BOOLEAN`, `sort_order INT`, timestamps. `conversations.agent_id TEXT REFERENCES agents(id) ON DELETE RESTRICT`, backfilled to the default agent, then `NOT NULL`, indexed `(user_id, agent_id, updated_at DESC)`. `messages` untouched — history inherits the agent through the conversation.

**D4 — Live rebuild registry, catalog dialed once.** Bootstrap builds the tool catalog exactly once (MCP dials are expensive), then an `AgentRegistry` (RWMutex over `map[agentID]*chat.Service`) serves reads. Management writes update the DB row, rebuild just that agent from catalog + row (model wiring + ADK agent construction is cheap), and swap the map entry. Handlers grab the runner per request, so an in-flight SSE turn finishes on its old runner — no cancellation, no torn streams. Rejected: rebuild-on-request (wasted work per turn), restart-on-change (CRUD becomes a slow SQL client).

**D5 — Catalog naming and two different failure policies.** MCP tools get `<server>.<tool>` names (two servers exposing `search` would otherwise collide); local tools keep their names (`search_faq`). Unknown tool names are *rejected* on CRUD writes with 400 naming the tool (a typo is an input bug), but *degrade* at startup (an unreachable MCP server is an infra condition): the agent starts with the resolvable subset, its listing row carries the missing names as `degraded_tools`, and the degradation is logged — extending the registry's existing degrade-not-fail rule to per-agent toolsets.

**D6 — Admin gate from Casdoor roles.** The verifier adds `Roles []string` from the token's `roles` claim to the principal (absent claim → no roles); `RequireAdmin` middleware guards `POST/PATCH/DELETE /api/v1/agents*` and `GET /api/v1/agents/{id}` (prompt detail is kitchen-side). Listing stays open to all authenticated users. The accepted role names come from config (`auth.admin_roles`, default `["fantastic-admin"]`). Non-admin gets 403; unauthenticated stays 401 via the existing middleware. Prerequisite on the Casdoor side (manual): application token format must be `JWT` (not JWT-Empty) and the role must exist with the operator as member.

**D7 — Binding semantics.** Mismatched conversation/agent on chat → 409 (not 404: the conversation exists, the request conflicts with its binding). New conversations bind to the requested agent. `GET /api/v1/conversations` gains `agent_id` on rows and an optional `?agent_id=` filter.

**D8 — Seeding: config-declared agents are declarative.** Migration 0005 seeds the default `general` agent row itself — the FK backfill of `conversations.agent_id` needs the anchor, and the canonical FAQ-answer prompt is baked into the migration SQL. On startup, bootstrap inserts agents declared in the config `agents:` block with `INSERT ... ON CONFLICT (id) DO NOTHING`: fresh deployments get their agents, DB-edited values always win over config, and a hand-emptied table reseeds. The known trade-off — deleting a config-declared agent is undone by the next restart — is accepted for a single-operator service (and logged). This supersedes the FAQ change's D9 bootstrap wiring, not its answering rules.

## Risks / Trade-offs

- [Casdoor token format is JWT-Empty → `roles` never reaches the verifier, admin gate unusable] → documented manual checklist in tasks; gate fails closed (403 for everyone, including admin) rather than open.
- [History replays tool results whose tool the agent no longer assigns] → existing orphan-dropping in `buildInput` covers results without their call partner; calls with results replay as inert context, which providers accept. Accepted: cross-agent history reuse is prevented by binding anyway.
- [Seeded `general` agent deleted, then table emptied by also deleting the rest → config seed resurrects it] → acceptable for a single-operator service; seeding is idempotent and visible in logs.
- [Concurrent PATCH to one agent is last-write-wins] → acceptable; no versioning column until a second operator exists.
- [Registry swap during list] → RWMutex snapshot per request; worst case a list is one rebuild behind, never torn.

## Migration Plan

1. Ship migration 0005 (`agents` table, `conversations.agent_id` + backfill + index) — additive; the old binary ignores the new columns.
2. Deploy the new binary and run migrations together (single-operator deploy); the demo page in the same binary already targets the new route, so the frontend/API break is self-contained.
3. Manual Casdoor steps before flipping admin usage: verify token format `JWT`, create `fantastic-admin`, assign the operator.
4. Rollback: 0005 down migration drops `agent_id` and the `agents` table; redeploying the previous binary restores the single-agent route. Conversations survive; their messages were never agent-scoped.

## Open Questions

None — the deferred Casdoor role-naming is a config default (`auth.admin_roles`) changeable without code.
