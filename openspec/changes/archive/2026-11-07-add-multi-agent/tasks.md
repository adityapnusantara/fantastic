# Tasks

## 1. Schema & config foundation

- [x] 1.1 Migration 0005: create `agents` table (slug TEXT PK, display_name, description, system_prompt, nullable model/api/temperature/reasoning_effort, tools TEXT[], enabled, sort_order, timestamps) and `conversations.agent_id` (FK ON DELETE RESTRICT, backfill to `general`, then NOT NULL, index `(user_id, agent_id, updated_at DESC)`) with down migration; the migration itself seeds the default `general` agent row first (FK anchor for the backfill, canonical FAQ-answer prompt baked into the SQL). Verify: `migrate up` then `migrate down` then `up` on the compose Postgres; inspect columns, backfill values, and index via psql.
- [x] 1.2 Config: add `agents:` seed block (id, display_name, description, icon, system_prompt, tools, model overrides, enabled, sort_order) with a `general` default whose prompt carries the FAQ answering rules, and `auth.admin_roles` (default `["fantastic-admin"]`); validate slug format, api enum, and temperature range at load. Verify: table-driven cases in `config_test.go` covering defaults, overrides, and validation failures.

## 2. Tool catalog

- [x] 2.1 Rework MCP registry to expose a named catalog `map[string]tool.BaseTool` with `<server>.<tool>` names (plus a name listing), keeping per-server degrade-not-fail. Verify: extend `registry_test.go` — two servers with overlapping tool names produce distinct prefixed entries; an unreachable server is skipped without error.
- [x] 2.2 Register the local FAQ search tool in the catalog under `search_faq` at bootstrap (replacing the flat-append wiring). Verify: `server_faq_test.go` shows the catalog contains `search_faq` when FAQ credentials are configured and omits it when not.
- [x] 2.3 Startup seeding: insert agents declared in the `agents:` config block with `INSERT ... ON CONFLICT (id) DO NOTHING` (missing rows created, existing rows untouched — DB edits always win). Verify: bootstrap-level test against the compose DB — first boot seeds configured agents, a second boot after editing a row keeps the edit, a table emptied by hand reseeds.

## 3. Agent store & runtime registry

- [x] 3.1 Port `driven.AgentStore` (list, get, create, update, delete, set-enabled) and the Postgres adapter. Verify: adapter tests on the compose DB following `conversation_store_test.go` patterns (CRUD round-trip, unknown id → domain.ErrNotFound).
- [x] 3.2 Parameterize `chatusecase.NewService` with the agent identity (ADK agent name/description from the agent row) instead of the hardcoded `"fantastic"`. Verify: `service_test.go` asserts the agent name surfaces in constructed agent and emitted events.
- [x] 3.3 `AgentRegistry`: RWMutex-guarded `map[agentID]*chat.Service` with get/list/rebuild/remove; model construction resolves per-agent overrides against the global `llm` block (reuse/parameterize `llmModel`). Verify: registry unit test with a fake model builder — rebuild swaps the instance seen by Get; remove makes Get miss; concurrent read during rebuild is race-free (`go test -race`).
- [x] 3.4 Bootstrap: build the catalog once, load all enabled agents from the store into the registry; log and flag agents whose tools cannot all be resolved (degraded list). Verify: server test with an unreachable MCP server in config — service starts, affected agent runs with remaining tools, degraded names recorded; `-race` clean. (folded into 5.3)

## 4. Roles & admin gate

- [x] 4.1 Casdoor verifier: parse the `roles` claim into `domain.Principal.Roles`; absent claim → empty roles. Verify: `verifier_test.go` cases with signed tokens carrying roles, no roles, and empty roles.
- [x] 4.2 `middleware.RequireAdmin(cfg.Auth.AdminRoles)` returning 403 for principals holding none of the configured roles; apply to `POST/PATCH/DELETE /api/v1/agents*` and `GET /api/v1/agents/{id}`. Verify: `middleware_test.go` cases — admin role passes, foreign role 403, empty roles 403; listing route stays ungated.

## 5. Agent management API

- [x] 5.1 `agentadmin` usecase: create/update validation (slug, prompt required, api/temperature ranges, every tool name present in the catalog — 400 naming the offender), id immutability, delete blocked with 409 while conversations reference the agent, disable/enable; each successful write triggers the registry rebuild/remove. Verify: usecase tests with fakes covering the validation matrix, id-change rejection, 409 path, and rebuild invocation per operation.
- [x] 5.2 HTTP handlers: `GET /api/v1/agents` (metadata only — no system prompt), `GET /api/v1/agents/{id}` (full detail, admin), `POST`, `PATCH`, `DELETE` with swagger annotations following the existing handler style. Verify: handler tests — list hides prompts for everyone, detail/CRUD enforce admin (403), validation errors surface 400 with tool name, unknown id 404, delete-with-conversations 409.
- [x] 5.3 Wire routes in bootstrap behind the existing bearer middleware plus `RequireAdmin` where specified. Verify: route table test or curl matrix on a running dev server — 401 unauthenticated, 403 non-admin write, admin CRUD round-trip.

## 6. Agent-scoped chat & binding

- [x] 6.1 Conversation binding: persist `agent_id` on create/chat, expose it on get/list, and support the `?agent_id=` list filter. Verify: store tests (binding set on insert, filter correctness) and conversation handler tests (agent_id present in JSON).
- [x] 6.2 Chat route: `POST /api/v1/agents/{agent}/chat` resolving the agent through the registry — 404 unknown/disabled agent, 409 when the conversation's agent mismatches, `Conversation-Id` header as today; remove `POST /api/v1/chat` and its swagger entry. Verify: handler tests for 404/409/success and updated swagger annotations.
- [x] 6.3 Live-rebuild behavior end to end: after `PATCH`ing an agent's system prompt, the next turn uses the new prompt without restart, and a turn in flight when the PATCH lands completes on the old definition. Verify: integration test or scripted compose check (start turn, PATCH mid-stream, assert stream finished coherently; next turn reflects the edit).

## 7. UI pages (static, no build step)

- [x] 7.1 Demo page: agent picker fed by `GET /api/v1/agents`, POST to the selected agent's chat path, display `agent_name` from SSE events. Verify: manual — log in on `/demo`, switch agents, see per-agent streaming and tool events.
- [x] 7.2 Admin page `/admin`: login (reuse token flow), agent table with enabled/degraded badges, create/edit form (prompt textarea, tool checkboxes from the catalog, model override fields), disable/enable and delete. Verify: manual — admin performs full CRUD from the page; a non-admin token sees every action rejected with 403.

## 8. Integration verification & docs

- [x] 8.1 `go build ./... && go test ./...` (with `-race`) green across the repo. Verify: command output clean.
- [x] 8.2 Compose smoke run: migrate, start, get dev token, chat with seeded `general`, create a custom agent via CRUD, chat with it, PATCH its prompt and observe live effect, attempt cross-agent chat → 409. Verify: recorded curl/SSE transcript of the full sequence. (caught + fixed the PATCH-enabled no-op bug; transcript in results)
- [x] 8.3 Docs: README section (agent model, CRUD examples, admin gate), `config.yaml.example` (agents block, admin_roles), and the Casdoor manual checklist (application token format `JWT`, create `fantastic-admin`, assign operator). Verify: docs reviewed against running behavior. (part B: swagger regenerated, README /admin + agent-tools)

## Workflow follow-up

- Implement via `/opsx:apply` once tasks are approved; archive via `/opsx:archive` after 8.x verifies.
- Casdoor side effects (token format check, role creation, operator assignment) are manual operator steps, tracked in 8.3's checklist.
