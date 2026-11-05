# Proposal

## Why

This repo currently has no implementation. We are starting it as a personal AI agent backend: a Go service that streams LLM chat, calls tools through MCP, and persists conversations in PostgreSQL. The service is built on `cloudwego/eino` (ADK + streaming) and follows the hexagonal folder structure established by `linkaja/ixion`, so future internal LinkAja adoption can reuse the same patterns. Auth (Casdoor) is included from day one so conversation data is user-scoped by a verified identity instead of a hardcoded stub.

## What Changes

- New Go module `github.com/adityapratamanusantara/fantastic` in this repo: an agent chat backend built on eino ADK (`ChatModelAgent` + `Runner`, `EnableStreaming`) with an OpenAI chat model (`gpt-6.1-sol`, configurable base URL).
- SSE chat endpoint whose event format matches eino's `adk/intro/http-sse-service` example, plus a static HTML demo page served by the same binary (backend only — no frontend framework, no build step).
- Conversation memory in PostgreSQL: `conversations` + `messages` tables, app-assigned per-conversation `seq`, message rows persisted before provider work and after each turn (partial output included on disconnect), rolling-summary compaction that keeps a configurable window (default 20) of recent messages and folds older overflow into a per-conversation `rolling_summary`.
- Tool integration via MCP only: config-driven multi-server registry (streamable HTTP / SSE transports), per-server `headers` support for Bearer tokens, and per-server `allow_tools` filtering before tools are exposed to the model. Demo server: Tebar MCP (`https://api.tebar.app/mcp`), token supplied via env var.
- Bearer-token auth with Casdoor (local JWKS verification: RS256 signature, issuer, audience, expiry, organization) feeding a `Principal` that scopes every conversation query. A dev-only token endpoint (`POST /api/v1/auth/token`, config-gated) lets the demo page obtain tokens via the Casdoor login API without exposing the client secret to the browser.
- Local infrastructure as one `docker compose up`: pgvector-capable Postgres (`pgvector/pgvector:pg17`, credentials via `.env`) plus Casdoor seeded on first boot from a committed `init_data.yaml` (organization, application, dev user). golang-migrate migrations run via `fantastic migrate`; migrations enable the `vector` extension.
- CLI surface `fantastic api` / `fantastic migrate` (urfave/cli v3), echo v5 HTTP server, swagger docs, health endpoint, graceful shutdown, `slog` logging.

## Capabilities

### New Capabilities

- `agent-chat-streaming`: The SSE chat API and demo page — how a turn is submitted, streamed, interrupted, and what events the client receives; durability contract (persist before provider work; persist output, or fail the stream visibly).
- `conversation-memory`: Postgres persistence of conversations and messages, history windowing, rolling-summary compaction, conversation list/get/delete for UIs such as assistant-ui.
- `mcp-tool-integration`: How external MCP servers are declared, connected (transports, headers), filtered (`allow_tools`), and exposed as eino tools to the agent.
- `casdoor-token-auth`: Which routes require a bearer token, how tokens are verified (JWKS, claims), how the verified identity reaches business logic, and the dev-only token endpoint.

### Modified Capabilities

None — greenfield; no specs exist in this repo yet.

## Impact

- **New code**: `config/`, `internal/{cmd,bootstrap,domain,application/{ports,usecases},adapters/{http,repositories/postgres,auth/casdoor,mcp}}`, `pkg/`, `migrations/`, `deploy/casdoor/init_data.yaml`, `docker-compose.yml`, `.env.example`, `Dockerfile`.
- **Dependencies**: `github.com/cloudwego/eino` (ADK), `github.com/cloudwego/eino-ext` components (`model/openai`, `tool/mcp`), `github.com/mark3labs/mcp-go` (via eino-ext), `github.com/jackc/pgx/v5`, `github.com/golang-migrate/migrate/v4`, `github.com/labstack/echo/v5`, `github.com/urfave/cli/v3`, `github.com/swaggo/swag`.
- **Infrastructure (local dev only)**: Postgres `pgvector/pgvector:pg17`, Casdoor `casbin/casdoor` (seeded), Tebar MCP over streamable HTTP.
- **APIs**: `POST /api/v1/chat` (SSE), `GET/POST /api/v1/auth/token` (dev-only), conversation CRUD (list, get messages, delete), health.
- **Out of scope (v1)**: React/assistant-ui frontend (its SSE adapter lives in a future frontend repo), long-term memory (parked: pgvector native or mem0-via-MCP), tool-approval/interrupt-resume flows, PKCE/refresh-token login in the demo page, multi-agent registries.
