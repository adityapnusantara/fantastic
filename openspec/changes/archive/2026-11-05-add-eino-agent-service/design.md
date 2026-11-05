# Design

## Context

Greenfield repo (`fantastic`), no existing Go code or specs. The structure and conventions follow `linkaja/ixion` (hexagonal: `internal/domain`, `internal/application/{ports,usecases}`, `internal/adapters/{http,repositories,auth}`, `internal/{bootstrap,cmd}`, `config/`, `migrations/`, `pkg/`). Motivation and scope: see [proposal.md](proposal.md); behavior contracts: see `specs/`.

The assistant-ui frontend is a future, separate concern: it owns zero persistent state and will hydrate from this backend's API. This repo is backend-only plus a static HTML demo page.

## Goals / Non-Goals

**Goals:**
- One command local stack: `docker compose up` (pgvector Postgres + seeded Casdoor), then `fantastic migrate` + `fantastic api`.
- Streaming agent chat with MCP tools and durable Postgres memory, user-scoped by verified Casdoor identity.
- Every capability behind a port so swapping model provider, memory store, or auth stays local to one adapter.

**Non-Goals:**
- React/assistant-ui frontend (future repo; its SSE→`ThreadMessageLike` adapter is TS work outside this repo).
- Long-term memory (parked: pgvector facts table or mem0-via-MCP; extension enabled so both stay open).
- Tool-approval / interrupt-resume flows (ADK checkpoints), branching/regenerate, PKCE + refresh-token login in the demo, token-usage metadata per message.

## Decisions

### D1. Agent framework: eino ADK (`ChatModelAgent` + `Runner`, `EnableStreaming`)
Chosen over hand-rolled tool-call loops and raw `compose` graphs: the ADK gives the ReAct loop, tool dispatch, and streaming `AgentEvent` iterator out of the box, and the SSE example (`adk/intro/http-sse-service`) defines the wire format we copy verbatim.
*Alternative rejected*: writing the agent loop manually — more control, but reimplements what ADK ships.

### D2. Model: OpenAI via eino-ext `model/openai`, model id `gpt-6.1-sol`, base URL configurable
Base URL default `https://api.openai.com/v1`; swapping to a gateway later is a config change only. API key from env (`OPENAI_API_KEY`).

**Resolution (2026-11-05):** `gpt-6.1-sol` rejects function tools on `/chat/completions` — it requires
`/v1/responses`; eino-ext `components/model/openai` has no responses support. Resolved by adopting
eino-ext `components/model/agenticopenai`: `NewResponsesModel` targets `/v1/responses` and implements eino's
`model.AgenticModel`, so the chat usecase now runs on the ADK agentic message path. Config selects the
surface with `llm.api` (`responses` default | `chat`); `llm.reasoning_effort` maps to `reasoning.effort`
(`low`/`medium`/`high`/`xhigh`) — the earlier "reasoning_effort: none" workaround idea is obsolete.

### D3. Memory: own `ConversationStore` port + postgres adapter — **not** ADK `SessionStore`
eino v0.10's session persistence (`SessionID`/`SessionStore`/session event log) is alpha with unstable APIs; stable eino (v0.9.x) has none. A hexagonal port fits the ixion structure, is mock-testable, and maps 1:1 to what a chat UI needs (flattened messages).
*Alternative rejected*: implementing ADK `SessionStore` on Postgres now — locks us to alpha APIs for no added capability; revisit after v0.10 final.
*Adapted from an event-sourced session design* (agents/sessions/session_events/checkpoints on SQLite): we adopt its **behaviors** — persist user message before provider work, deltas live-only, idempotent inserts, durability-as-contract — but **not** its schema (event sourcing, checkpoints, agent registry), because those solve problems we cut from v1 (approval resume, replay) and a JSON event blob would force "folding" events into messages for UI hydration, while our normalized rows map directly to eino `schema.Message`.

### D4. Schema: two tables, no user table
```
conversations(id PK, user_id idx, title, rolling_summary, summary_until_seq, created_at, updated_at)
messages(id PK, conversation_id FK CASCADE, seq, role CHECK user|assistant|tool,
         content, tool_calls JSONB, tool_call_id, created_at, UNIQUE(conversation_id, seq))
```
- `seq` is app-assigned per conversation (`MAX+1` in the insert transaction); timestamps are not reliable orderers within a turn, UUID tiebreaks are random.
- `id` UUID does double duty as the stable message id UIs need, and as an idempotency key: `INSERT ... ON CONFLICT (id) DO NOTHING`.
- `summary_until_seq` pairs with `seq` to mark which messages the rolling summary already covers; raw messages are never deleted by compaction.
- No user table: identities live in Casdoor; `user_id` is an opaque string from the verified token.
- Migration `0001` runs `CREATE EXTENSION IF NOT EXISTS vector` (image is `pgvector/pgvector:pg17`) so future long-term memory needs no infra change.

### D5. Turn lifecycle (durability contract)
```
1. verify token → Principal{UserID}
2. load context: rolling_summary + messages WHERE seq > window_start
3. tx: INSERT user message (+ title if first) + bump updated_at   ← persist BEFORE provider
4. run Runner streaming; deltas → SSE only, never persisted
5. on done/cancel/disconnect: INSERT assembled assistant + tool messages (partial kept)
   — if this fails, the stream ends with an error event (a turn whose output is not
     stored must not look successful)
6. compaction: if uncovered messages exist beyond the window →
   new_summary = LLM(old_summary + uncovered); summary_until_seq = window_start - 1
```
Context sent to the model = `rolling_summary` + last `memory.history_window` (default 20) messages, always in `seq` order.

### D6. Tools: MCP only, via eino-ext `components/tool/mcp` (wraps `mark3labs/mcp-go`)
Config-driven registry, multi-server:
```yaml
mcp_servers:
  tebar:
    url: https://api.tebar.app/mcp
    headers:
      Authorization: "Bearer ${TEBAR_MCP_TOKEN}"   # ${VAR} resolved from env at load
    allow_tools: [list_accounts, list_posts, get_post, get_platform_rules, list_comments]
```
- `allow_tools` present → only those tools exposed to the model; absent → all tools of that server.
- One unreachable server degrades to fewer tools (log + continue); it must not block startup.
- Demo config ships with the Tebar server (streamable HTTP, token from env) — no local MCP server needed. Write tools stay off the default allowlist (agent actions are public-facing posts otherwise).
- stdio transport: deferred until a local server is actually needed.

### D7. Auth: Casdoor local verification, ported from ixion's `casdoor-token-auth` pattern
Bearer required on `/api/v1`; verify RS256 signature via cached JWKS (single refetch on unknown key id), issuer, audience (client id), expiry, token type, organization; failures are uniform 401s without which-check leakage; identity flows to business logic as `Principal{UserID}` — never from client input. No password handling, no per-request call to Casdoor.
Dev token endpoint (`POST /api/v1/auth/token`): config-gated (`auth.dev_token_endpoint`, default off → 404), proxies username/password to the Casdoor login API using `auth.client_id`/`auth.client_secret` held server-side, so the demo page never sees the secret and stays same-origin (no CORS, no secret in page JS). Real PKCE login belongs to the future frontend.

### D8. Wire format and UI story
SSE event format copied exactly from eino's `http-sse-service`. assistant-ui will consume it through a `useExternalStoreRuntime` adapter in the frontend repo (fetch-based streaming — native `EventSource` cannot send `Authorization`). The static demo page in this repo logs in via the dev token endpoint and renders SSE events; no build step.

### D9. Service skeleton: ixion conventions
Module `github.com/adityapratamanusantara/fantastic`; `internal/cmd` (`fantastic api|migrate`, urfave/cli v3); `internal/bootstrap` wires config → pgx pool → repositories → MCP registry → chat model → ADK runner → usecases → echo server; `internal/adapters/http` per-resource packages + `response` envelope (ixion style) for JSON endpoints, raw SSE for chat; swagger annotations; `slog` logging; graceful shutdown. Go 1.27.x.

## Risks / Trade-offs

- [eino ADK API drift (v0.9.x minor releases)] → pin the module version; keep ADK usage confined to `internal/bootstrap` (runner construction) and the SSE mapping so upgrades stay local.
- [Rolling-summary LLM call adds latency/cost on overflow] → fires only when overflow exists, not per turn; window configurable; summary quality tunable later without schema change.
- [Two concurrent turns on one conversation race `seq = MAX+1`] → v1 is single-user; unique index makes the loser fail visibly instead of corrupting order; per-conversation in-flight lock deferred.
- [Casdoor `init_data.yaml` field names drift across image versions] → pin the `casbin/casdoor` image tag in compose; seed file tested against that tag.
- [Model/tool output shape surprises (e.g. tool message pairing rules per provider)] → early task: end-to-end spike (compose → migrate → api → one tool-using turn) before building out CRUD and compaction.
- [Dev token endpoint is password-grant-shaped] → off by default, 404 when off; documented as dev-only; flipping `auth.dev_token_endpoint: false` kills it without code change.

## Migration Plan

Greenfield: `docker compose up` → `fantastic migrate` (extension + 2 tables) → `fantastic api` → demo login → chat. Host ports avoid common dev ranges and default to a `55xx` family — Postgres **5555**, Casdoor **5560**, API/demo/swagger **5580** — all overridable via `.env` (container-internal ports unchanged; Casdoor reaches Postgres over the compose network, not host ports). Rollback = stop services; no external consumers exist yet.

## Open Questions

- Embedding dimensions for the future `memories` table (decided when long-term memory lands; extension + image are already in place).
- Token-usage metadata per message (parked; additive migration later).
