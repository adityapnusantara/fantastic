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

- `POST /chat` — SSE stream (`data:` frames with `type`:
  `stream_chunk`, `message`, `tool_result`, `done`, `error` — eino
  `http-sse-service` format). Body: `{"message": "...", "conversation_id": "uuid?"}`.
  Without a conversation id a new one is created (see `Conversation-Id` response header).
- `POST /auth/token` — dev-only login (`{username, password}` → access token).
  Present only when `auth.dev_token_endpoint: true`; otherwise 404. Disable
  before exposing the service beyond localhost.
- `GET /whoami` — verified identity (connectivity check).
- `POST|GET /conversations`, `GET /conversations/{id}/messages`,
  `DELETE /conversations/{id}` — conversation management.
- `GET /swagger/doc.json` — OpenAPI document.
- `GET /health` — liveness.

## Configuration

See `config.yaml` (defaults inline) and `.env.example`:

| Key | Purpose |
|---|---|
| `llm.base_url` / `llm.model` | OpenAI-compatible endpoint and model id |
| `llm.api` | OpenAI surface: `responses` (default; `/v1/responses` — required for reasoning models with function tools, e.g. gpt-6.1-sol) or `chat` (`/v1/chat/completions`) |
| `memory.history_window` | recent messages sent with the rolling summary (default 20) |
| `auth.*` | Casdoor issuer, client id, organization, JWKS URL; `dev_token_endpoint` gates the dev login route |
| `mcp_servers.<name>` | `url`, `transport` (`http` = streamable HTTP, `sse`), `headers` (supports `${ENV}`), `allow_tools` (absent = expose all) |

One unreachable MCP server degrades the agent to the remaining tools; it never
blocks startup.

## Memory model

Conversations and messages live in Postgres (owner-scoped by the token's
user id). Every turn: the user message is stored **before** the model runs,
assembled output is stored after it (partial output survives disconnects),
and when messages overflow the window a rolling summary is folded in —
raw history is never deleted. `migrations/0001` enables pgvector so future
long-term memory needs no infra change.

## Development

```bash
go build ./... && go vet ./... && go test ./...     # unit + integration (uses the compose DB)
$(go env GOPATH)/bin/swag init -g main.go -o docs   # regenerate OpenAPI docs after annotation changes
docker compose down -v                              # full local reset (drops data)
```

Integration tests target `fantastic_test` on the compose Postgres
(`TEST_DATABASE_URL` overrides).
