package main

import (
	"fmt"
	"strings"
)

// renderReport produces docs/rag/eval-report.md. Everything except the
// timestamp and the latency/cost run-variance is deterministic given the
// frozen fixture and the indexed corpus.
func renderReport(out evalOutput) string {
	var b strings.Builder

	b.WriteString("# FAQ RAG Eval Report — retrieval quality (task 3.2)\n\n")
	fmt.Fprintf(&b, "Generated at: %s by `go run ./scripts/spike/faq-eval` (re-run command at the bottom).\n\n", out.GeneratedAt)

	// ---- Binding scoring rule, VERBATIM from the fixture -----------------
	b.WriteString("## Binding scoring rule (verbatim from `data/corpus/golden-set.json` `scoring_rule`)\n\n")
	fmt.Fprintf(&b, "> %s\n\n", out.ScoringRule)
	b.WriteString("Hit@k therefore means: the pinned chunk (`content_hash` equality) appears within the top-k. URL-level hits are recorded only as failure-analysis evidence, never scored. Retrieval is the production path unchanged: pgvector cosine `embedding <=>` ordering, `score = 1 - distance`, top-k=5, no similarity floor, no reranking (design D7/D11).\n\n")

	// ---- Run metadata -----------------------------------------------------
	b.WriteString("## Run metadata\n\n")
	b.WriteString("| Field | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Embedding model | `%s` (config `faq.embedding`, design D2) |\n", out.Model)
	fmt.Fprintf(&b, "| Embedding dims | %d |\n", out.Dims)
	fmt.Fprintf(&b, "| Indexed corpus | %d documents / %d chunks (`faq_documents` / `faq_chunks` counts at run time; manifest: 44 docs / 421 chunks) |\n", out.CorpusDocs, out.CorpusChunks)
	fmt.Fprintf(&b, "| Fixture | `data/corpus/golden-set.json` frozen at %s |\n", out.FixtureFrozen)
	fmt.Fprintf(&b, "| Scored questions | %d (frozen set of 10 minus 1 unresolvable drop — see substitutions) |\n", out.ScoredCount)
	fmt.Fprintf(&b, "| Top-k | %d (design D7 default) |\n", out.K)
	fmt.Fprintf(&b, "| Diagnostic depth | %d (miss analysis only; not scored) |\n", out.DiagK)
	fmt.Fprintf(&b, "| Retrieval SQL | `internal/adapters/repositories/postgres/faq_search.go` `FaqSearchStore.Search` (unchanged 2.3 code) |\n")
	fmt.Fprintf(&b, "| Embedder build | `internal/cmd/faq.go` `faqEmbedder` pattern: eino-ext `components/embedding/openai` from `faq.embedding` config |\n\n")

	// ---- Aggregate vs gate ------------------------------------------------
	b.WriteString("## Aggregate results vs gate bar (design D11)\n\n")
	b.WriteString("| Metric | Value | Bar | Verdict |\n|---|---|---|---|\n")
	fmt.Fprintf(&b, "| hit@1 | %.4f (%d/%d) | — | — |\n", out.Aggregate.Hit1, frac(out.Aggregate.Hit1, out.ScoredCount), out.ScoredCount)
	fmt.Fprintf(&b, "| hit@3 | %.4f (%d/%d) | — | — |\n", out.Aggregate.Hit3, frac(out.Aggregate.Hit3, out.ScoredCount), out.ScoredCount)
	fmt.Fprintf(&b, "| hit@5 | %.4f (%d/%d) | ≥ 0.8 | %s |\n", out.Aggregate.Hit5, frac(out.Aggregate.Hit5, out.ScoredCount), out.ScoredCount, verdict(out.Gate.Hit5OK))
	fmt.Fprintf(&b, "| MRR | %.4f | ≥ 0.6 | %s |\n", out.Aggregate.MRR, verdict(out.Gate.MRROK))
	fmt.Fprintf(&b, "\n**Retrieval-only gate (hit@5 ≥ 0.8 AND MRR ≥ 0.6): %s.** The full D11 gate also includes corpus-coverage review, ≥7/10 demo answers (owner-judged, task 3.3/3.4) and owner sign-off — not measured here.\n\n", passFail(out.Gate.Pass))

	// ---- Per-question table ----------------------------------------------
	b.WriteString("## Per-question results (scored set)\n\n")
	b.WriteString("Rank 0 = pinned chunk not in top-`k`. MRR contribution = 1/rank of the pinned chunk, 0 when absent.\n\n")
	b.WriteString("| n | Question | Pinned hash in top-`k` | Rank | hit@1 | hit@3 | hit@5 | MRR contrib | embed ms | retrieve ms |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range out.Results {
		b.WriteString("| " + fmt.Sprintf("%d | %s | %s | %d | %s | %s | %s | %s | %.0f | %.0f |\n",
			r.N, mdCell(r.Question), yn(r.Rank > 0), r.Rank,
			pf(r.Hit1), pf(r.Hit3), pf(r.Hit5),
			fmt.Sprintf("%.4f", r.RR), r.EmbedMs, r.RetrieveMs))
	}
	b.WriteString("\n")

	// ---- Provisional substitution / drop decisions ------------------------
	b.WriteString("## Provisional substitution / drop decisions (owner decisions, recorded by the orchestrator — USER HOLDS VETO AT THE GATE)\n\n")
	b.WriteString("These decisions are **provisional** and must be re-confirmed by the owner before the D11 gate verdict is final:\n\n")
	b.WriteString("- **Original Q3 (fee-table question, \"Berapa biaya admin untuk top up LinkAja?\") was substituted** with the limit-saldo question (`n=3`, \"Berapa limit saldo dan transaksi LinkAja?\") because fee tables are images with no extractable amounts (see `known_gaps` in the fixture and `docs/rag/corpus-report.md` §4a). Status: `substitution_status: pending_user_confirmation`.\n")
	b.WriteString("- **Original Q8 (account closure, \"Bagaimana cara menghapus atau menutup akun LinkAja?\") is DROPPED from the scored set** — the topic is genuinely absent from the corpus (no unit or article covers account closure; fixture `n=8` has `expected_chunk_hash: null`, `substitution_status: unresolvable`). The scored set is therefore **9 questions**. Escalation per fixture: add a corpus article or replace the golden question.\n\n")
	b.WriteString("Fixture `known_gaps` (verbatim):\n\n")
	for _, g := range out.Dropped {
		fmt.Fprintf(&b, "- `n=%d` (%s): %s\n", g.N, g.Topic, g.Reason)
	}
	b.WriteString("\n")

	// ---- Failure analysis inputs -----------------------------------------
	b.WriteString("## Failure analysis inputs (for the 3.3 decision memo)\n\n")
	b.WriteString("For every question that is not rank 1: where the pinned chunk actually reached (diagnostic depth, same SQL path), whether the expected article URL surfaced in the top-`k`, and what the top-1 chunk was instead (topical-adjacency evidence). Nothing here changes the score — scoring stays chunk-level per the binding rule.\n\n")
	misses := false
	for _, r := range out.Results {
		if r.Rank == 1 {
			continue
		}
		misses = true
		fmt.Fprintf(&b, "### n=%d — %s\n\n", r.N, r.Question)
		fmt.Fprintf(&b, "- Pinned chunk: `%s` (article %s)\n", short(r.ExpectedHash), r.ExpectedURL)
		if r.Rank > 0 {
			fmt.Fprintf(&b, "- Pinned chunk rank in top-`k`: **%d** (score %.4f) — hit@5 yes, drags MRR to %.4f\n", r.Rank, r.PinnedScore, r.RR)
		} else {
			fmt.Fprintf(&b, "- Pinned chunk rank in top-`k`: **absent (rank 0, score n/a)**\n")
			if r.DiagRank > 0 {
				fmt.Fprintf(&b, "- Diagnostic: pinned chunk **does exist at rank %d of the deeper (k=%d) search** — a ranking/threshold problem, not an indexing problem\n", r.DiagRank, out.DiagK)
			} else {
				fmt.Fprintf(&b, "- Diagnostic: pinned chunk **not found even at depth %d** — embedding-similarity or chunking problem (see 3.3)\n", out.DiagK)
			}
		}
		fmt.Fprintf(&b, "- Expected URL in top-`%d`: %s\n", out.K, yn(r.URLInTop5))
		fmt.Fprintf(&b, "- Top-1 instead: %s (score %.4f, same article as pinned: %s)\n", r.Top1URL, r.Top1Score, yn(r.Top1SameArticle))
		fmt.Fprintf(&b, "- Top-1 text starts with: “%s”\n", r.Top1Snippet)
		b.WriteString("- Top-`k` listing:\n\n")
		b.WriteString("  | # | pinned? | score | source URL | chunk starts with |\n  |---|---|---|---|---|\n")
		for _, h := range r.Top5 {
			fmt.Fprintf(&b, "  | %d | %s | %.4f | %s | %s |\n", h.Rank, yn(h.Pinned), h.Score, mdCell(h.SourceURL), mdCell(h.Snippet))
		}
		b.WriteString("\n")
	}
	if !misses {
		b.WriteString("No misses: every scored question retrieved the pinned chunk at rank 1.\n\n")
	}

	// ---- Cost --------------------------------------------------------------
	b.WriteString("## Embedding cost (estimate, text-embedding-3-small)\n\n")
	b.WriteString("| Item | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Question characters (scored set) | %d |\n", out.Cost.TotalChars)
	fmt.Fprintf(&b, "| Estimated tokens (chars ÷ %.0f) | %d |\n", charsPerTokenEstimate, out.Cost.EstTokens)
	if out.Cost.ProbeOK {
		fmt.Fprintf(&b, "| Measured tokens (one batched `/embeddings` usage probe, same model) | %d |\n", out.Cost.MeasuredTokens)
		fmt.Fprintf(&b, "| Measured cost @ $%.2f / 1M tokens | $%.8f |\n", out.Cost.PricePerMillion, out.Cost.MeasuredCostUSD)
	} else {
		fmt.Fprintf(&b, "| Measured tokens | n/a (%s) |\n", out.Cost.ProbeNote)
	}
	fmt.Fprintf(&b, "| Estimated cost @ $%.2f / 1M tokens | $%.8f |\n", out.Cost.PricePerMillion, out.Cost.EstCostUSD)
	fmt.Fprintf(&b, "| Per-question query embed latency | see table above (embed ms ≈ one OpenAI round trip) |\n\n")
	b.WriteString("Full-corpus re-index cost for scale context: ~49.9k tokens ≈ $0.001 per `faq index` run (task 2.2 measurement).\n\n")

	// ---- Determinism / re-run ----------------------------------------------
	b.WriteString("## Repeatability\n\n")
	b.WriteString("The runner is deterministic against the frozen fixture: same questions → same embeddings pipeline → same per-question ranks, **modulo** (a) the report timestamp and (b) latency/cost columns (real network timing) and (c) OpenAI embedding bit-nondeterminism across calls (same-text cosine ≥ 0.9988 between consecutive runs, task 2.2 measurement) which can in principle flip near-tied ranks.\n\n")
	b.WriteString("Re-run command (from the repo root; docker postgres on 5555 must be up, `OPENAI_API_KEY` resolvable via `.env` or environment):\n\n")
	b.WriteString("```sh\ngo run ./scripts/spike/faq-eval\n```\n\n")
	b.WriteString("Flags: `--config`, `--golden`, `--report`, `--json`, `--database-url`, `--k`, `--diag-k`, `--skip-cost-probe`. The runner exits non-zero if any pinned `expected_chunk_hash` is absent from `faq_chunks` (stale index vs frozen fixture guard).\n")
	return b.String()
}

func yn(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func pf(v bool) string {
	if v {
		return "PASS"
	}
	return "fail"
}

func verdict(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

func passFail(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

func frac(v float64, n int) int {
	return int(v*float64(n) + 0.5)
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}

// mdCell escapes pipe characters for markdown table cells.
func mdCell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
