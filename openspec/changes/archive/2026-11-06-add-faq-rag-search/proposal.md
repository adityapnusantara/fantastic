# Proposal: add-faq-rag-search

## Why

The agent answers LinkAja questions ("berapa biaya top up?", "cara reset PIN?") from model priors — ungrounded and free to drift from what linkaja.id actually publishes. This change adds a retrieval capability over the official public FAQ content so answers are grounded, cited, and reproducible. It is the retrieval sibling of the long-term-memory concern the archived `add-eino-agent-service` design explicitly parked, and the owner wants it explored spike-first: prove retrieval quality on a frozen golden set through a decision gate before any productionization.

## What Changes

- New FAQ ingestion path: scrape the linkaja.id FAQ surface (robots.txt/ToS-audited, politeness rules) into a curated intermediate JSON corpus `data/corpus/linkaja-faq.json` (explicit owner-requested artifact; raw HTML cached under gitignored `data/raw/`).
- New vector store: migration `0004_create_faq_chunks` adds `faq_documents` + `faq_chunks` (single `vector(1536)` column for OpenAI `text-embedding-3-small`, HNSW cosine) on the already-provisioned pgvector. Existing `conversations`/`messages` tables untouched.
- New local eino tool `search_faq` exposed to the existing chat agent at bootstrap (first non-MCP tool; the MCP registry stays MCP-only). Answers stay Indonesian and must cite the linkaja.id source URL.
- Embedding is fully config-driven: new `faq.embedding` section in `config.yaml` (model, optional `base_url` for OpenAI-compatible gateways, `api_key` as `${ENV}` ref) mirroring the existing `llm:` pattern. Single model by owner decision — no local-model comparison spike.
- Quality gate before productionization: a frozen 10-question Indonesian golden set scored chunk-level (hit@k / MRR), an eval report, a decision memo, and an explicit GO/NO-GO gate. Productionization (CLI subcommands, port formalization, docs) happens only on GO.
- New CLI subcommands (gated): `./fantastic faq scrape` and `./fantastic faq index` with `content_hash`-based idempotent upsert.
- Out of scope: frontend beyond the existing `/demo` page, re-scrape automation/scheduling, integration with the parked long-term-memory design, docker-compose/deployment changes.

## Capabilities

### New Capabilities

- `faq-rag-search`: FAQ knowledge search for the chat agent — corpus acquisition from the official linkaja.id FAQ surface (scrape etiquette, JSON corpus schema), vector indexing into pgvector, chunk-level quality-scored retrieval, and citation-bearing Indonesian answers delivered through the existing SSE chat.

### Modified Capabilities

- None. Chat streaming requirements, MCP tool integration, conversation memory, and Casdoor auth behaviors are unchanged; the FAQ tool rides the existing `[]tool.BaseTool` injection point at bootstrap, and `tool_result` SSE events are already spec'd behavior.

## Impact

- **Code**: new driven ports (`FAQIndexer`, `FAQSearcher`) + postgres adapter + tool adapter; chat service bootstrap gains one tool append; `config/config.go` gains the `faq:` section; `internal/cmd` gains `faq scrape|index` subcommands; new migration `0004`; throwaway spike scripts under `scripts/spike/` (deleted when the Go subcommands land).
- **Dependencies**: adds `github.com/cloudwego/eino-ext/components/embedding/openai` (v0.0.0-20261019121619-eee37f63ff95; custom `base_url` verified supported); `pgx/v5` and `openai-go` already present. No eino-ext pgvector indexer/retriever exists — indexing/retrieval is hand-rolled `pgx` + SQL.
- **Systems**: pgvector already provisioned in compose (no compose changes); outbound HTTP to linkaja.id (robots.txt measured permissive; ToS verdict produced in the spike); OpenAI API usage for embeddings (owner-approved, public content).
- **Artifacts**: committed corpus JSON + index manifest under `data/corpus/`, gitignored raw HTML under `data/raw/`, reports under `docs/rag/` (inventory, corpus-report, eval-report, decision-memo).
- **Data**: FAQ content is public and global (not user-scoped); corpus carries `source_url` + `content_hash` for attribution and drift-safe re-ingestion.
