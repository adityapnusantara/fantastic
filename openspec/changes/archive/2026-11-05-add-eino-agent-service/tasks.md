# Tasks

## 1. Project skeleton & local stack

- [x] 1.1 Init Go module `github.com/adityapratamanusantara/fantastic` with ixion-style folders (`internal/{cmd,bootstrap,domain,application/{ports,usecases},adapters/{http,repositories,auth,mcp}}`, `config/`, `migrations/`, `pkg/`) and add deps (eino, eino-ext `model/openai` + `tool/mcp`, pgx/v5, golang-migrate, echo v5, urfave/cli v3); verify `go build ./...` passes
- [x] 1.2 Implement `config/` YAML loading (llm, memory, mcp_servers, auth) with `${VAR}` env interpolation for secret values and unit tests for interpolation and defaults; verify tests pass
- [x] 1.3 Implement `internal/cmd` (`fantastic api`, `fantastic migrate`) + `internal/bootstrap` minimal wiring with echo server, `/health`, graceful shutdown, slog logging; verify `fantastic api` starts and `/health` returns 200
- [x] 1.4 Add `docker-compose.yml` (`pgvector/pgvector:pg17` + `casbin/casdoor` pinned tag, creds via `.env`; host ports default `5555`→postgres, `5560`→casdoor, `5580`→api, all configurable via `.env`, container ports unchanged), `.env.example`, `deploy/casdoor/init_data.yaml` seed (org, app `fantastic`, dev user), `.gitignore` for `.env`; verify `docker compose up -d` brings both up, Postgres is reachable on `localhost:5555`, and the Casdoor UI on `localhost:5560` is reachable with the seeded user

## 2. Conversation persistence (memory core)

- [x] 2.1 Write migrations `0001_enable_pgvector` (`CREATE EXTENSION vector`), `0002_create_conversations`, `0003_create_messages` (columns/indexes per design D4); wire `fantastic migrate` to golang-migrate; verify migrate up/down runs clean against the compose Postgres
- [x] 2.2 Define domain types (`Conversation`, `Message`, roles, `Principal`) and the `ConversationStore` driven port (create/append/append-batch, list-by-user, get-messages, delete, load-window+summary, save-summary); verify `go vet` and a compile-time interface assertion
- [x] 2.3 Implement postgres adapter: app-assigned `seq = MAX+1` inside the insert transaction, `ON CONFLICT (id) DO NOTHING`, `updated_at` bump on append, cascade delete; verify with pgx-based integration tests (testcontainer or compose DB) covering ordering, idempotent duplicate insert, cascade delete, user scoping
- [x] 2.4 Implement mock `ConversationStore` and usecase-level tests for user-scoped reads (owner filtering, 404 on foreign conversation); verify tests pass

## 3. Casdoor auth

- [x] 3.1 Implement JWKS bearer verifier middleware (RS256 signature via cached keys, single refetch on unknown kid, issuer/audience/expiry/token-type/org checks, uniform 401 envelope) ported from the ixion pattern; verify with httptest: missing/non-Bearer/expired/wrong-signature/wrong-audience → 401 without leaking which check failed
- [x] 3.2 Implement `Principal` extraction and pass identity into a protected sample route (conversation routes in §5/§7 use it); verify identity equals the token subject in an httptest
- [x] 3.3 Implement dev token endpoint `POST /api/v1/auth/token` (config-gated, 404 when disabled; proxies Casdoor login API with server-side client id/secret); verify enabled path issues a working token against the compose Casdoor and disabled path returns 404

## 4. MCP tool integration

- [x] 4.1 Implement MCP registry in `internal/adapters/mcp`: per-server streamable HTTP/SSE clients via eino-ext `tool/mcp`, `headers` with env-resolved values, startup continues when a server is unreachable (log + degrade); verify with a fake local MCP server over HTTP: tools discovered, one-down case still starts the service
- [x] 4.2 Implement `allow_tools` filtering before exposing tools to the model; verify unit tests: allowlisted tool exposed, unlisted tool absent, absent allowlist exposes all
- [x] 4.3 Verify integration against the real Tebar MCP server with `TEBAR_MCP_TOKEN` set: allowlisted tools appear and one read tool call succeeds; document required env vars in README

## 5. Chat streaming endpoint

- [x] 5.1 Implement chat usecase turn lifecycle per design D5: load summary+window, persist user message before provider (title on first message), run ADK `Runner` with `EnableStreaming`, persist assembled assistant+tool messages on done/cancel/disconnect, error event on persistence failure; verify usecase tests with a fake model + mock store cover: normal turn, model failure keeps user message, disconnect persists partial output, persistence failure ends stream with error
- [x] 5.2 Implement `internal/adapters/http/chat` SSE handler with the `http-sse-service` event format and context cancellation on client disconnect; verify httptest streaming shows deltas and terminal event, and cancellation cancels the runner
- [x] 5.3 Wire auth + store + MCP tools + model into bootstrap so `/api/v1/chat` is end-to-end; verify a tool-using turn against the compose stack (curl with dev token) completes with tool activity events and correct rows in `messages`

## 6. Rolling-summary compaction

- [x] 6.1 Implement compaction in the chat usecase (post-turn): fold uncovered overflow into `rolling_summary` via the model, advance `summary_until_seq`, raw rows untouched; verify usecase tests: overflow folded, no overflow → no LLM call, window content = summary + last N in seq order
- [x] 6.2 Verify end-to-end with a conversation longer than the window: model context stays within limit (assert on fake-model calls) and `GET messages` still returns full history

## 7. Conversation management API

- [x] 7.1 Implement `internal/adapters/http/conversation`: list (newest activity first), get flattened messages, delete (cascade); response envelope per ixion style; verify httptest: ordering, flattened tool-call history, 404 on foreign conversation, 204 on delete
- [x] 7.2 Add swagger annotations for auth/chat/conversation endpoints and verify `swagger.json` regenerates

## 8. Demo page

- [x] 8.1 Serve static `demo.html` from the binary: login form → dev token endpoint, message box → SSE chat rendering deltas/tool activity/terminal events, conversation list + delete; verify manually per spec scenarios (login, chat with a tool turn, disconnect keeps partial output)

## 9. Integration verification

- [x] 9.1 Full cold-start run from scratch: fresh volume `docker compose up` → `fantastic migrate` → `fantastic api` → demo login → multi-turn chat including tool call and window overflow; verify DB rows (user_id from token, seq order, summary marker) and spec scenarios for all four capabilities checked off. *Done 2026-11-05: 13 turns on a fresh volume (tool call turn 1 via `list_accounts`), overflow at 22 msgs → `rolling_summary` + `summary_until_seq` verified, recall answered from the summary; surfaced and fixed a real bug — a tool result whose assistant tool-call partner was folded into the summary produced an orphan function output (OpenAI 400) — `buildInput` now drops orphaned tool rows, regression test `TestOrphanToolResultAfterCompactionIsDropped` added.*
- [x] 9.2 README: prerequisites, env vars, compose, commands, config reference, dev-only endpoint warning; verify commands run as written

## Workflow follow-up

- Archive the change after review; `openspec validate` before archiving.
- Parked follow-ups (do not implement here): assistant-ui frontend repo + SSE adapter, long-term memory (pgvector facts or mem0 via MCP), tool-approval/checkpoint resume, PKCE login for the real frontend, per-conversation in-flight lock.
