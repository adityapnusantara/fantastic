# Tasks

## 1. Spike — inventory & corpus

- [x] 1.1 Inventory the linkaja.id FAQ surface: enumerate candidate pages (sitemap.xml, /faq, help-center paths/subdomains), record HTML vs JS-rendered per page, and write robots.txt + ToS verdicts. Verify: `docs/rag/inventory.md` exists with URL list, rendering notes, and both verdicts.
- [x] 1.2 Specify the JSON corpus schema (per design D5, `content_hash`, language tag) and the politeness contract (UA string, min 1 req/s, cache layout under `data/raw/`); add the `data/` git rule — ignore `data/raw/`, commit `data/corpus/linkaja-faq.json`. Verify: schema + rules appended to `docs/rag/inventory.md`; `git check-ignore data/raw/test.html` succeeds while the corpus path is NOT ignored.
- [x] 1.3 Build the throwaway python3 scraper under `scripts/spike/` (per design D8; deleted in group 4): fetch per politeness rules, extract Q&A units, cache raw HTML, emit `data/corpus/linkaja-faq.json`. Verify: corpus validates against the 1.2 schema and the script prints per-section counts.
- [x] 1.4 Validate the corpus: schema validation, dedupe by `content_hash`, coverage report mapping each inventory URL to captured/missed plus a gap list. Verify: `docs/rag/corpus-report.md` exists with the coverage map.

## 2. Vector store & retrieval

- [x] 2.1 Migration `0004_create_faq_chunks`: `faq_documents` (source_url, title, content_hash, scraped_at) and `faq_chunks` (document FK, chunk_text, chunk_index, `vector(1536)` + HNSW cosine index); clean down migration. Verify: `./fantastic migrate` applies and `./fantastic migrate --down 1 && ./fantastic migrate` round-trips without breaking `conversations`/`messages`; table + index confirmed via `docker compose exec -T postgres psql -U fantastic -d fantastic -c '\d faq_chunks'`.
- [x] 2.2 Embedding pipeline spike (single model per design D2): read model/base_url/key from the `faq.embedding` config section (`${ENV}` credential resolution), prefer eino-ext `components/embedding/openai` (custom base_url verified supported), chunk per D4, upsert idempotently by `content_hash` via hand-rolled `pgx` + SQL, emit `data/corpus/index-manifest.json`. Verify: indexing the corpus fills `faq_chunks` matching the manifest; a re-run produces zero duplicate chunks.
- [x] 2.3 Retrieval store: cosine top-k (configurable, default 5) returning chunk text, source URL, title, and score. Verify: a test query returns the expected chunk with its source URL and score, and honors the configured top-k.
- [x] 2.4 Minimal `search_faq` eino tool appended at bootstrap beside MCP tools (registry untouched) + system-prompt addition per design D9. Verify: in `/demo`, asking "berapa biaya top up LinkAja?" shows a `tool_result` SSE event and the answer cites a linkaja.id URL.

## 3. Evaluation & decision gate

- [x] 3.1 Freeze the golden set (10 owner-reviewed questions from the plan; substitutions confirmed with the owner BEFORE freezing) into an eval fixture pinning per question: expected chunk `content_hash` (preferred; page URL only when no pin-able unit), expected source URL, and the pinned fact. Verify: fixture covers all 10 questions with all three fields, pinned against the actual corpus.
- [x] 3.2 Eval runner: embed with the configured model, retrieve top-k, score hits at CHUNK level strictly per the binding scoring rule (design D11, recorded verbatim at the top of the report), compute hit@k (k=1,3,5) and MRR, record latency and embedding cost. Verify: `docs/rag/eval-report.md` exists with a per-question pass/fail table and is re-runnable against the frozen fixture.
- [x] 3.3 Spike decision memo: retrieval-quality verdict for the configured model, chunking verdict, failure analysis of every missed question (chunking? embedding? corpus gap?), and explicit GO/NO-GO against the gate criteria. Verify: `docs/rag/decision-memo.md` exists with the verdict stated.
- [x] 3.4 GATE REVIEW with the owner: walk the gate criteria (eval bar, robots/ToS, coverage, ≥7/10 demo answers with the pinned-fact + expected-URL rubric judged by the owner, owner go). Verify: verdict recorded in the memo. On NO-GO: stop — groups 4 and 5 MUST NOT start; revisit source content, chunking, or scope with the owner.

## 4. Productionization (gated on 3.4 GO)

- [x] 4.1 CLI subcommands `./fantastic faq scrape` and `./fantastic faq index` (urfave/cli v3, `internal/cmd` patterns; politeness flags; incremental upsert by `content_hash`; keeps emitting the index manifest). Retire and delete `scripts/spike/` python throwaways. Verify: both commands run end-to-end from an empty store, a re-run upserts without duplicates, manifest matches row counts, and `scripts/spike/` no longer exists.
- [x] 4.2 Formalize ports `FAQIndexer`/`FAQSearcher` in `internal/application/ports/driven`, postgres adapter in `internal/adapters/repositories/postgres`, tool adapter wrapping `FAQSearcher` as `tool.BaseTool`, embedding via eino-ext `components/embedding/openai`; unit tests with testfakes following the chat usecase pattern. Verify: `go test ./...` passes with the new tests.
- [x] 4.3 Formalize the `faq:` config section in `config/config.go` (allowed hosts, request delay, `embedding {model, base_url?, dims, api_key ref}`, top-k, cache dir) and update `.env.example`. Verify: the service boots with the section present, credential resolved from the environment only (no secret in `config.yaml`), and top-k/limits are honored.
- [x] 4.4 README section: scrape/index/eval usage, politeness and attribution note. Verify: the documented commands run as written.

## 5. Integration verification

- [x] 5.1 Full-pipeline integration check: scrape → index → eval-report regenerated against the frozen golden set meets hit@5 ≥ 0.8 and MRR ≥ 0.6 (any miss below the bar = fail); in `/demo`, 3 sampled golden questions each trigger a `search_faq` tool call AND the answer contains that question's pinned fact and cites the expected source URL, checked against the frozen fixture. Verify: all checks pass in one sequence from a clean `data/raw/` cache.

## Workflow follow-up

- Archive the change after review requirements are satisfied (`openspec archive add-faq-rag-search`).
- If the 3.4 gate returned NO-GO, archive only after a follow-up change resolves the blockers; this change's productionization groups stay unchecked.
