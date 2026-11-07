# fantastic

Personal AI agent backend: eino ADK chat over SSE, tools via MCP, conversation
memory in PostgreSQL, Casdoor bearer auth.

## Prerequisites

- Go 1.27+
- Docker (compose v2)
- An OpenAI API key
- (Optional) a Tebar MCP token for the demo tools

## Quick start

```bash
cp .env.example .env          # isi OPENAI_API_KEY (dan TEBAR_MCP_TOKEN bila ada)
docker compose up -d          # postgres (pgvector) :5555 + casdoor :5560
go build -o fantastic .
./fantastic migrate           # extension vector + conversations + messages
./fantastic api               # http://localhost:5580
```

Open <http://localhost:5580/demo> — the seed creates org `pribadi`, app
`fantastic-app`, and user `aditya / fantastic-dev-123` (dev only, see
`deploy/casdoor/init_data.yaml`). Login, chat, and manage conversations.

## Ports

| Service    | Host port (`.env` overridable) |
|------------|-------------------------------|
| Postgres   | `5555` (container-internal 5432) |
| Casdoor UI | `5560` (container-internal 8000) |
| API/demo   | `5580` |

## Commands

```bash
./fantastic api [--config config.yaml] [--database-url postgres://…]
./fantastic migrate [--down N]     # apply, or roll back N migrations
```

Both commands auto-load `.env` from the working directory (already-set
environment variables win). Config file: `config.yaml` — secrets are
referenced as `${VAR}` and resolved from the environment at load.

## API

Base path `/api/v1`; every route requires `Authorization: Bearer <casdoor token>`.

- `POST /agents/{agent}/chat` — SSE stream (`data:` frames with `type`:
  `stream_chunk`, `message`, `tool_result`, `done`, `error` — eino
  `http-sse-service` format). Body: `{"message": "...", "conversation_id": "uuid?"}`.
  Without a conversation id a new one is created, bound to the agent in
  the path (see `Conversation-Id` response header). See [Agents](#agents).
- `POST /auth/token` — dev-only login (`{username, password}` → access token).
  Present only when `auth.dev_token_endpoint: true`; otherwise 404. Disable
  before exposing the service beyond localhost.
- `GET /whoami` — verified identity (connectivity check).
- `POST|GET /conversations`, `GET /conversations/{id}/messages`,
  `DELETE /conversations/{id}` — conversation management; conversations
  are bound to one agent (`agent_id` on responses, `?agent_id=` list
  filter).
- `GET /swagger/doc.json` — OpenAPI document.
- `GET /health` — liveness.

## Agents

The service hosts multiple independent agents instead of one hardcoded
bot. An agent is a Postgres row (migration 0005): a slug `id` (immutable —
it names the chat URL and the conversation binding), display metadata, a
system prompt, optional per-agent LLM overrides (`model`, `api`,
`temperature`, `reasoning_effort` — unset inherits the global `llm`
block), a tool assignment, `enabled`, and `sort_order`. Rows are seeded
declaratively from the `agents:` block of `config.yaml` at startup
(`ON CONFLICT DO NOTHING`): edits made through the API or DB always win
over the config file, a hand-emptied table reseeds on the next restart,
and deleting a config-declared agent is undone by that restart.

The tool catalog is config-only: MCP tools join it under `<server>.<tool>`
names, local tools (`search_faq`) under their own names. Agent management
assigns catalog tools, it never defines new ones — a new MCP server means
editing `mcp_servers:` and restarting. Create/patch/delete rebuild the
agent live, no restart needed; a turn already streaming finishes on the
definition it started with. At boot, an agent whose tools cannot all be
resolved (e.g. an unreachable MCP server) still starts with the
resolvable subset — the missing names surface as `degraded_tools` in the
listing until the next restart.

| Method | Path | Auth |
|--------|------|------|
| `POST` | `/api/v1/agents/{id}/chat` | any authenticated user |
| `GET` | `/api/v1/agents` | any authenticated user |
| `GET` | `/api/v1/agents/{id}` | admin role |
| `POST` | `/api/v1/agents/{id}` | admin role |
| `PATCH` | `/api/v1/agents/{id}` | admin role |
| `DELETE` | `/api/v1/agents/{id}` | admin role |
| `GET` | `/api/v1/agent-tools` | admin role |

The listing carries metadata only (`id`, `display_name`, `description`,
`icon`, `enabled`, `sort_order`, `degraded_tools`) — never the prompt;
full detail needs the admin role. Admin writes are validated: slug-format
id, prompt required, every tool name must exist in the catalog (400
naming the offender), the id cannot be renamed. Deleting an agent that
still has conversations is refused with 409 — disable it with
`enabled: false` instead (stays listed, chat answers 404, history stays
readable). Chatting into a conversation bound to another agent is 409.

Agent management also has a page: `GET /admin` serves a static single-file
UI (no build step) over the management API — login plus agent CRUD
(metadata, prompt, tool assignment, LLM overrides, enabled, sort order).
Authorization stays enforced by the API (`RequireAdmin`), not by the page:
without the admin role every call is rejected with 403 and the page just
shows the banner.

Walkthrough (dev credentials from the compose seed, localhost only;
`jq` picks the token out of the login response):

```bash
TOKEN=$(curl -s localhost:5580/api/v1/auth/token \
  -H 'Content-Type: application/json' \
  -d '{"username":"aditya","password":"fantastic-dev-123"}' | jq -r .data.access_token)

# List agents (metadata only, open to all authenticated users):
curl -s -H "Authorization: Bearer $TOKEN" localhost:5580/api/v1/agents

# Chat with the seeded general agent (SSE):
curl -N -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"message":"Apa itu LinkAja?"}' localhost:5580/api/v1/agents/general/chat

# Create an agent (admin; the id is the path slug):
curl -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"display_name":"Support","description":"LinkAja support","system_prompt":"Answer only from search_faq results; cite sources.","tools":["search_faq"]}' \
  localhost:5580/api/v1/agents/support

# Patch its prompt — the next turn uses it, no restart:
curl -X PATCH -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"system_prompt":"New prompt."}' localhost:5580/api/v1/agents/support

# Disable (stays listed, chat answers 404, history stays readable):
curl -X PATCH -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"enabled":false}' localhost:5580/api/v1/agents/support

# Delete (409 while conversations still reference it):
curl -X DELETE -H "Authorization: Bearer $TOKEN" localhost:5580/api/v1/agents/support
```

### Operator setup (Casdoor)

The admin gate reads the `roles` claim of the Casdoor token and compares
it against `auth.admin_roles` (default `["fantastic-admin"]`). Two manual
steps in the Casdoor UI before using the admin API — missing either fails
closed (every management call gets 403, including the real admin):

1. **Token format.** Applications → `fantastic-app` → set *Token format*
   to `JWT`. `JWT-Empty` strips the `roles` claim, so no token ever
   carries a role and the gate 403s for everyone.
2. **Admin role.** Roles (organization `pribadi`) → create
   `fantastic-admin` → add the operator as a member.

## Configuration

See `config.yaml` (defaults inline) and `.env.example`:

| Key | Purpose |
|---|---|
| `llm.base_url` / `llm.model` | OpenAI-compatible endpoint and model id |
| `llm.api` | OpenAI surface: `responses` (default; `/v1/responses` — required for reasoning models with function tools, e.g. gpt-6.1-sol) or `chat` (`/v1/chat/completions`) |
| `memory.history_window` | recent messages sent with the rolling summary (default 20) |
| `auth.*` | Casdoor issuer, client id, organization, JWKS URL; `dev_token_endpoint` gates the dev login route; `admin_roles` lists the Casdoor roles allowed to manage agents |
| `mcp_servers.<name>` | `url`, `transport` (`http` = streamable HTTP, `sse`), `headers` (supports `${ENV}`), `allow_tools` (absent = expose all) |
| `agents.<id>` | agent seeds — declarative rows, API/DB edits always win (see [Agents](#agents)) |

One unreachable MCP server degrades the agent to the remaining tools; it never
blocks startup.

## Memory model

Conversations and messages live in Postgres (owner-scoped by the token's
user id). Every turn: the user message is stored **before** the model runs,
assembled output is stored after it (partial output survives disconnects),
and when messages overflow the window a rolling summary is folded in —
raw history is never deleted. `migrations/0001` enables pgvector so future
long-term memory needs no infra change.

## FAQ RAG (LinkAja help corpus)

`faq scrape` builds a curated corpus from linkaja.id help pages, `faq index`
chunks + embeds it into pgvector (idempotent), and the agent exposes a
`search_faq` tool — in `/demo` it answers LinkAja questions in Indonesian
with cited source URLs. Prereqs: `./fantastic migrate` applied (0004) and
`OPENAI_API_KEY` set (like the agent).

```bash
# Scrape (crawl linkaja.id /news, write the curated corpus; polite + cached):
./fantastic faq scrape
./fantastic faq scrape --dry-run          # discover + extract + report, no corpus write
./fantastic faq scrape --cache-dir data/raw --delay-ms 1500

# Index (chunk + embed + upsert pgvector, write the manifest; idempotent):
./fantastic faq index
./fantastic faq index --database-url postgres://... --corpus data/corpus/linkaja-faq.json --manifest data/corpus/index-manifest.json
```

**Politeness & attribution.** linkaja.id is crawled politely: fixed user
agent (`fantastic-faq-spike/0.1 (personal project; contact via repo)`),
≥1 s between requests (`--delay-ms` below 1000 is refused), robots.txt
re-checked every run, only `/news*` paths on www.linkaja.id over https, and
raw HTML is cached under `data/raw/` (gitignored) so re-runs never re-crawl.
Corpus content belongs to LinkAja (PT Fintek Karya Nusantara) — personal
use only per the gate decision; revisit before any public or commercial
exposure.

Design docs and reports live in `docs/rag/` (inventory, corpus-report,
eval-report, decision memo — includes the gate verdict and the amended
URL-level gate metric). The eval fixture is
`data/corpus/golden-set.json`; the eval runner stays at
`scripts/spike/faq-eval/` for future re-evals.

## Development

```bash
go build ./... && go vet ./... && go test ./...     # unit + integration (uses the compose DB)
$(go env GOPATH)/bin/swag init -g main.go -o docs   # regenerate OpenAPI docs after annotation changes
docker compose down -v                              # full local reset (drops data)
```

Integration tests target `fantastic_test` on the compose Postgres
(`TEST_DATABASE_URL` overrides).
