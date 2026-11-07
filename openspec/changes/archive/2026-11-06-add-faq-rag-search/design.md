# Design

## Context

`fantastic` is a single-binary hexagonal Go backend: eino v0.9.22 ADK chat over SSE (`internal/application/usecases/chat`), tools injected as `[]tool.BaseTool` from an MCP-only config-driven registry, Casdoor bearer auth, PostgreSQL with pgvector already provisioned (migration `0001`; tables `conversations`, `messages` only). Repo doctrine: "every capability behind a port". The archived `add-eino-agent-service` design explicitly parked vector-backed memory — this change builds the retrieval sibling. Verified library status: eino-ext ships `components/embedding/openai` (custom `base_url` honored for non-Azure gateways, `libs/acl/openai@v0.1.15/embedding.go:101-105`) but **no** pgvector indexer/retriever component — all pgvector work is hand-rolled `pgx` + SQL (`pgx/v5` and `openai-go` already in `go.mod`). See proposal.md for motivation; the behavior contract lives in `specs/faq-rag-search/spec.md`.

## Goals / Non-Goals

**Goals:**
- Grounded, citation-bearing Indonesian FAQ answers through the existing SSE chat, proven against a frozen golden set before productionization.
- A reproducible corpus pipeline: scrape → curated JSON corpus → idempotent vector index, all re-runnable without re-crawling.
- Config-driven embedding and retrieval settings following the existing `llm:` pattern.

**Non-Goals:**
- Model comparison spike (owner decision 2026-11-06: single model, `text-embedding-3-small`).
- Frontend work beyond `/demo`, re-scrape automation/scheduling, long-term-memory integration, docker-compose/deployment changes.

## Decisions

- **D1 — Land in the same backend, not a separate service.** New driven ports (`FAQIndexer`, `FAQSearcher`) + postgres adapter + one tool appended at bootstrap. Alternative (standalone RAG service) rejected: duplicates auth/SSE plumbing for a personal-scale feature.
- **D2 — Embedding: OpenAI `text-embedding-3-small` only, fully config-driven.** 1536 dims, multilingual; `config.yaml` gains `faq.embedding {model, base_url?, api_key: ${ENV}}` mirroring `llm:`. A local `bge-m3`/Ollama comparison was planned and dropped by owner decision — no Ollama dependency. Schema note: a vector column is a (model, dimension) pair; a second model later means a new column + migration, never reuse of the 1536 column.
- **D3 — pgvector with HNSW cosine; hand-rolled `pgx` + SQL.** No Qdrant/weaviate/meilisearch — pgvector is already provisioned and pgx is already a dependency; and no eino-ext pgvector indexer/retriever exists to adopt. Corpus is personal-scale (tens–hundreds of chunks), so HNSW + top-k is ample.
- **D4 — Chunking: one chunk per Q&A unit** where page structure allows (FAQ pages are usually question/accordion pairs); 400–800 token windows with 10–15% overlap as fallback for long-form pages. Embedded text = question + answer concatenated (question text materially boosts hit rates).
- **D5 — The JSON corpus is a first-class artifact** (explicit owner request): `data/corpus/linkaja-faq.json` with `{id, source_url, question/title, answer/content, category?, language, content_hash, scraped_at}`. Raw HTML cached under `data/raw/` (gitignored) so re-embedding never re-crawls; the corpus itself is committed so evals are reproducible. The index step emits `data/corpus/index-manifest.json` (per-document chunk counts + chunk hashes) as the integrity reference — chunk counts are derived, not stored in the corpus schema.
- **D6 — Scraping etiquette:** robots.txt + ToS audit is the first spike task; measured robots.txt is permissive (only `/verify`, `/apps_tcash` disallowed; 4 sitemaps). Polite UA (`fantastic-faq-spike/0.1 (personal project; contact via repo)`), ≥1 req/s, cache everything, public pages only. Disallowed paths escalate to the owner at the gate, never bypassed. Fallback if either audit turns adverse: owner-curated saved pages.
- **D7 — Retrieval: pure vector top-k (k=5, configurable) for the spike.** Hybrid (pg_trgm / keyword) only if the eval shows lexical misses (typo'd product names, code-like terms). Tool result carries chunk text + source URL + score so the agent can cite.
- **D8 — Spike code is throwaway-grade but fenced**: python3 scripts under `scripts/spike/` only, deleted when the Go CLI subcommands land; productionization happens exclusively after the gate.
- **D9 — Answering language: Indonesian by default** via a system-prompt addition (answer in the user's language, cite source URLs).
- **D10 — FAQ search is a local eino tool, not MCP.** The MCP registry stays MCP-only; the chat usecase already accepts a `[]tool.BaseTool` slice at `internal/bootstrap/server.go`, so the FAQ tool is appended at bootstrap — the first local tool in the repo, establishing that pattern intentionally.
- **D11 — Quality gate between evaluation and productionization.** *(Amended at gate review 2026-11-06, owner decision Option C: the gate metric is URL-level / best-of-article rank-aware hit@k + MRR; chunk-level scoring remains a reported diagnostic. Amended results passed the bar — see decision-memo.md gate verdict.)* A 10-question Indonesian golden set (owner-reviewed; substitutions confirmed with the owner before freezing) is pinned per question with: expected chunk `content_hash` (preferred; page URL only when no Q&A unit is pin-able), expected source URL, and a pinned fact a correct answer must contain. **Binding scoring rule:** a hit is scored at the CHUNK level — top-k must contain the chunk whose `content_hash` equals the pinned expected chunk; MRR on the same unit; the rule is recorded verbatim at the top of the eval report. Gate bar: hit@5 ≥ 0.8 AND MRR ≥ 0.6 on the frozen set; no robots/ToS blockers; corpus coverage acceptable or gap explained; ≥7/10 demo answers pass (judge = owner, rubric per question: answer contains the pinned fact AND cites the expected source URL); owner says go. NO-GO stops the change after evaluation — productionization tasks must not start.

## Risks / Trade-offs

- [FAQ surface is JS-rendered (accordion widgets)] → task 1 inventories rendering; prefer the help-center vendor's JSON endpoints over headless-browser scraping if so.
- [ToS verdict turns adverse] → D6 escalation: owner-curated saved pages replace scraping; gate blocks until resolved.
- [Colloquial Indonesian queries miss lexically] → measured in eval; D7 hybrid retrieval is the standing fallback.
- [Single-model risk: text-embedding-3-small underperforms the bar] → first lever is D7 hybrid; a second model enters as a new vector column + migration (D2 schema note).
- [FAQ content drifts] → `content_hash` upsert handles re-ingestion; re-scrape scheduling stays manual (non-goal).
- [Attribution/copyright on public FAQ content] → acceptable for personal exploration per owner; must be revisited before any public or commercial exposure.

## Migration Plan

`0004_create_faq_chunks` is purely additive (two new tables; `conversations`/`messages` untouched); its down migration drops cleanly, verified by the `migrate --down 1 && migrate` round-trip in final verification. Rollback of the feature = down-migrate; spike scripts are deleted at productionization, leaving no residue.

## Open Questions

- Verbatim quoting vs summarizing in answers — default: summarize + cite; owner confirms at gate review.
- If the FAQ lives behind a help-center vendor's JS widget, which JSON endpoint to target — task 1's inventory decides, documented there.
