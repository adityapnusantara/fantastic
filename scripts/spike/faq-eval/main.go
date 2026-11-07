// Command faq-eval is the task 3.2 retrieval eval runner (spike-grade,
// D8-fenced: deleted when the Go CLI formalizes the eval in task 4.x).
//
// It embeds each scored question of the frozen golden set with the
// configured faq.embedding embedder, retrieves top-k through the
// production FAQSearcher SQL path (postgres.FaqSearchStore, embedding <=>
// ordering, score = 1 - distance, no floor / no reranking), and scores
// hits strictly per the fixture's binding CHUNK-level scoring rule
// (design D11): a hit is the top-k containing the chunk whose
// content_hash equals the pinned expected_chunk_hash; MRR on the same
// unit. It writes docs/rag/eval-report.md plus a machine-readable JSON
// dump, and fails loudly if a pinned chunk is absent from faq_chunks.
//
// Re-run (from the repo root):
//
//	go run ./scripts/spike/faq-eval
//
// Flags override the canonical paths; defaults regenerate both artifacts
// deterministically (modulo timestamps and latency/cost run variance).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/repositories/postgres"
)

const (
	defaultGolden = "data/corpus/golden-set.json"
	defaultReport = "docs/rag/eval-report.md"
	defaultJSON   = "scripts/spike/faq-eval/results.json"
	// text-embedding-3-small pricing (USD per 1M tokens, OpenAI price list).
	pricePerMillionTokens = 0.02
	// Chars→tokens heuristic for the cost estimate (calibrated against the
	// measured usage probe recorded in the report).
	charsPerTokenEstimate = 4.0
)

// goldenSet mirrors the frozen fixture schema (data/corpus/golden-set.json).
type goldenSet struct {
	SchemaVersion int               `json:"schema_version"`
	FrozenAt      string            `json:"frozen_at"`
	ScoringRule   string            `json:"scoring_rule"`
	Questions     []goldenQ         `json:"questions"`
	KnownGaps     []json.RawMessage `json:"known_gaps"`
}

type goldenQ struct {
	N                  int     `json:"n"`
	Question           string  `json:"question"`
	Topic              string  `json:"topic"`
	ExpectedChunkHash  *string `json:"expected_chunk_hash"`
	ExpectedSourceURL  *string `json:"expected_source_url"`
	PinnedFact         *string `json:"pinned_fact"`
	SubstitutionStatus string  `json:"substitution_status"`
	UnresolvableReason string  `json:"unresolvable_reason"`
}

// qResult is the per-question scored outcome.
type qResult struct {
	N               int      `json:"n"`
	Question        string   `json:"question"`
	Topic           string   `json:"topic"`
	ExpectedHash    string   `json:"expected_chunk_hash"`
	ExpectedURL     string   `json:"expected_source_url"`
	Substitution    string   `json:"substitution_status,omitempty"`
	Rank            int      `json:"rank"`
	Hit1            bool     `json:"hit_at_1"`
	Hit3            bool     `json:"hit_at_3"`
	Hit5            bool     `json:"hit_at_5"`
	RR              float64  `json:"reciprocal_rank"`
	PinnedScore     float64  `json:"pinned_chunk_score"`
	DiagRank        int      `json:"diag_rank_within_20"`
	URLInTop5       bool     `json:"expected_url_in_top5"`
	Top1SameArticle bool     `json:"top1_same_article"`
	Top1Snippet     string   `json:"top1_snippet"`
	Top1URL         string   `json:"top1_source_url"`
	Top1Score       float64  `json:"top1_score"`
	EmbedMs         float64  `json:"embed_ms"`
	RetrieveMs      float64  `json:"retrieve_ms"`
	Chars           int      `json:"question_chars"`
	Top5            []topHit `json:"top5"`
}

type topHit struct {
	Rank        int     `json:"rank"`
	ContentHash string  `json:"content_hash"`
	SourceURL   string  `json:"source_url"`
	Snippet     string  `json:"snippet"`
	Score       float64 `json:"score"`
	Pinned      bool    `json:"is_pinned"`
}

// aggregates + metadata for the JSON dump.
type evalOutput struct {
	SchemaVersion int         `json:"schema_version"`
	GeneratedAt   string      `json:"generated_at"`
	ScoringRule   string      `json:"scoring_rule_verbatim"`
	FixtureFrozen string      `json:"fixture_frozen_at"`
	Model         string      `json:"model"`
	Dims          int         `json:"dims"`
	CorpusDocs    int         `json:"corpus_documents"`
	CorpusChunks  int         `json:"corpus_chunks"`
	K             int         `json:"k"`
	DiagK         int         `json:"diag_k"`
	ScoredCount   int         `json:"scored_questions"`
	Dropped       []droppedQ  `json:"dropped_questions"`
	Results       []qResult   `json:"results"`
	Aggregate     aggregate   `json:"aggregate"`
	Cost          costInfo    `json:"cost"`
	Gate          gateVerdict `json:"gate"`
}

type droppedQ struct {
	N      int    `json:"n"`
	Topic  string `json:"topic"`
	Reason string `json:"reason"`
}

type aggregate struct {
	Hit1 float64 `json:"hit_at_1"`
	Hit3 float64 `json:"hit_at_3"`
	Hit5 float64 `json:"hit_at_5"`
	MRR  float64 `json:"mrr"`
}

type costInfo struct {
	TotalChars      int     `json:"total_question_chars"`
	EstTokens       int     `json:"estimated_tokens_chars_div_4"`
	MeasuredTokens  int     `json:"measured_tokens_usage_probe"`
	PricePerMillion float64 `json:"price_usd_per_million_tokens"`
	EstCostUSD      float64 `json:"estimated_cost_usd"`
	MeasuredCostUSD float64 `json:"measured_cost_usd"`
	ProbeOK         bool    `json:"usage_probe_ok"`
	ProbeNote       string  `json:"probe_note"`
}

type gateVerdict struct {
	Hit5Bar float64 `json:"hit_at_5_bar"`
	MRRBar  float64 `json:"mrr_bar"`
	Hit5OK  bool    `json:"hit_at_5_pass"`
	MRROK   bool    `json:"mrr_pass"`
	Pass    bool    `json:"gate_pass_retrieval_only"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "faq-eval: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Local DX parity with internal/cmd.Run: load .env when present,
	// already-set variables win.
	_ = loadDotEnv(".env")

	configPath := flag.String("config", "config.yaml", "path to the YAML config")
	goldenPath := flag.String("golden", defaultGolden, "path to the frozen golden set fixture")
	reportPath := flag.String("report", defaultReport, "path to write the markdown report")
	jsonPath := flag.String("json", defaultJSON, "path to write the machine-readable results")
	dbURL := flag.String("database-url", "", "postgres connection url (overrides config)")
	k := flag.Int("k", 5, "top-k retrieval width (hit@5 gate uses the default 5)")
	diagK := flag.Int("diag-k", 20, "diagnostic retrieval depth for miss analysis (scoring unaffected)")
	skipProbe := flag.Bool("skip-cost-probe", false, "skip the one-shot usage probe (estimate-only cost)")
	flag.Parse()

	if *k <= 0 {
		return fmt.Errorf("--k must be > 0")
	}

	cfg, _, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	apiKey, err := cfg.FAQEmbeddingAPIKey()
	if err != nil {
		return fmt.Errorf("embedding credential: %w", err)
	}
	url := cfg.Database.URL
	if *dbURL != "" {
		url = *dbURL
	}
	if url == "" {
		return fmt.Errorf("no database url (config database.url or --database-url)")
	}

	golden, err := readGolden(*goldenPath)
	if err != nil {
		return err
	}
	if golden.ScoringRule == "" {
		return fmt.Errorf("fixture %s has no scoring_rule field (binding rule must be copied verbatim)", *goldenPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	poolCfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return fmt.Errorf("parse database url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	// Corpus integrity metadata: the report must state the indexed size.
	var docs, chunks int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM faq_documents), (SELECT count(*) FROM faq_chunks)`).Scan(&docs, &chunks); err != nil {
		return fmt.Errorf("count corpus rows (is the index built?): %w", err)
	}

	// Scored set = questions with a pinned chunk hash (fixture n=8 has
	// expected_chunk_hash: null and is dropped per the recorded decision).
	scored, dropped := splitGolden(golden)

	// INTEGRITY GUARD: every pinned chunk must exist in faq_chunks. This
	// is the loud failure mode for a stale DB / stale fixture pair.
	if err := assertPinnedChunks(ctx, pool, scored); err != nil {
		return err
	}

	embedder, err := openai.NewEmbedder(ctx, &openai.EmbeddingConfig{
		APIKey:  apiKey,
		BaseURL: cfg.FAQ.Embedding.BaseURL,
		Model:   cfg.FAQ.Embedding.Model,
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("build embedder: %w", err)
	}
	store := postgres.NewFaqSearchStore(pool)

	results := make([]qResult, 0, len(scored))
	dims := 0
	for _, q := range scored {
		res, err := evalQuestion(ctx, embedder, store, q, *k, *diagK)
		if err != nil {
			return fmt.Errorf("question n=%d: %w", q.N, err)
		}
		if res == nil {
			continue
		}
		if dims == 0 {
			dims = embedDimsCached
		}
		results = append(results, *res)
	}
	if len(results) == 0 {
		return fmt.Errorf("no scored questions evaluated (fixture broken?)")
	}

	// Cost: chars→tokens estimate plus a one-shot measured usage probe
	// (one batched /embeddings call for all questions, same model).
	questions := make([]string, 0, len(scored))
	for _, q := range scored {
		questions = append(questions, q.Question)
	}
	cost := costEstimate(questions, cfg.FAQ.Embedding.Model, cfg.FAQ.Embedding.BaseURL, apiKey, *skipProbe)

	agg := aggregateResults(results)
	gate := gateVerdict{
		Hit5Bar: 0.8, MRRBar: 0.6,
		Hit5OK: agg.Hit5 >= 0.8, MRROK: agg.MRR >= 0.6,
	}
	gate.Pass = gate.Hit5OK && gate.MRROK

	out := evalOutput{
		SchemaVersion: 1,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		ScoringRule:   golden.ScoringRule,
		FixtureFrozen: golden.FrozenAt,
		Model:         cfg.FAQ.Embedding.Model,
		Dims:          dims,
		CorpusDocs:    docs,
		CorpusChunks:  chunks,
		K:             *k,
		DiagK:         *diagK,
		ScoredCount:   len(results),
		Dropped:       dropped,
		Results:       results,
		Aggregate:     agg,
		Cost:          cost,
		Gate:          gate,
	}

	if err := writeJSON(*jsonPath, out); err != nil {
		return err
	}
	report := renderReport(out)
	if err := os.WriteFile(*reportPath, []byte(report), 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	// Stable one-line rank fingerprint for the determinism re-run check.
	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "n=%d:rank=%d ", r.N, r.Rank)
	}
	fmt.Printf("RANKS: %s\n", strings.TrimSpace(sb.String()))
	fmt.Printf("AGGREGATE: hit@1=%.4f hit@3=%.4f hit@5=%.4f mrr=%.4f (gate hit@5>=0.8: %v, mrr>=0.6: %v, pass=%v)\n",
		agg.Hit1, agg.Hit3, agg.Hit5, agg.MRR, gate.Hit5OK, gate.MRROK, gate.Pass)
	fmt.Printf("COST: chars=%d est_tokens=%d measured_tokens=%d est_usd=%.8f measured_usd=%.8f\n",
		cost.TotalChars, cost.EstTokens, cost.MeasuredTokens, cost.EstCostUSD, cost.MeasuredCostUSD)
	fmt.Printf("WROTE: %s\nWROTE: %s\n", *jsonPath, *reportPath)
	return nil
}

// embedDimsCached records the measured embedding dimension from the first
// question (package-level because evalQuestion validates per call).
var embedDimsCached int

func evalQuestion(ctx context.Context, embedder *openai.Embedder, store *postgres.FaqSearchStore, q goldenQ, k, diagK int) (*qResult, error) {
	t0 := time.Now()
	vecs, err := embedder.EmbedStrings(ctx, []string{q.Question})
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	embedMs := float64(time.Since(t0).Microseconds()) / 1000.0
	if len(vecs) != 1 {
		return nil, fmt.Errorf("embedder returned %d vectors for 1 input", len(vecs))
	}
	f32 := make([]float32, len(vecs[0]))
	for i, v := range vecs[0] {
		f32[i] = float32(v)
	}
	if embedDimsCached == 0 {
		embedDimsCached = len(f32)
	}
	if embedDimsCached != config.DefaultFaqEmbeddingDims {
		return nil, fmt.Errorf("embedding dims = %d, want %d (design D2)", embedDimsCached, config.DefaultFaqEmbeddingDims)
	}

	t1 := time.Now()
	hits, err := store.Search(ctx, f32, k)
	if err != nil {
		return nil, fmt.Errorf("search top-%d: %w", k, err)
	}
	retrieveMs := float64(time.Since(t1).Microseconds()) / 1000.0

	rank, pinnedScore := 0, 0.0
	for i, h := range hits {
		if h.ContentHash == *q.ExpectedChunkHash {
			rank = i + 1
			pinnedScore = h.Score
			break
		}
	}

	// Diagnostic depth (same SQL path, wider LIMIT): where did the pinned
	// chunk actually land? Scoring is unaffected (D11 pins the top-k=5).
	diagRank := 0
	if rank == 0 && diagK > k {
		if deep, err := store.Search(ctx, f32, diagK); err == nil {
			for i, h := range deep {
				if h.ContentHash == *q.ExpectedChunkHash {
					diagRank = i + 1
					break
				}
			}
		}
	}

	res := &qResult{
		N: q.N, Question: q.Question, Topic: q.Topic,
		ExpectedHash: *q.ExpectedChunkHash, ExpectedURL: *q.ExpectedSourceURL,
		Substitution: q.SubstitutionStatus,
		Rank:         rank,
		Hit1:         rank >= 1 && rank <= 1,
		Hit3:         rank >= 1 && rank <= 3,
		Hit5:         rank >= 1 && rank <= 5,
		PinnedScore:  pinnedScore,
		DiagRank:     diagRank,
		EmbedMs:      embedMs, RetrieveMs: retrieveMs,
		Chars: utf8.RuneCountInString(q.Question),
	}
	if rank > 0 {
		res.RR = 1.0 / float64(rank)
	}
	if len(hits) > 0 {
		res.Top1Snippet = snippet(hits[0].ChunkText, 110)
		res.Top1URL = hits[0].SourceURL
		res.Top1Score = hits[0].Score
		res.Top1SameArticle = hits[0].SourceURL == res.ExpectedURL
	}
	for i, h := range hits {
		res.Top5 = append(res.Top5, topHit{
			Rank: i + 1, ContentHash: h.ContentHash, SourceURL: h.SourceURL,
			Snippet: snippet(h.ChunkText, 110), Score: h.Score,
			Pinned: h.ContentHash == res.ExpectedHash,
		})
		res.URLInTop5 = res.URLInTop5 || h.SourceURL == res.ExpectedURL
	}
	return res, nil
}

// assertPinnedChunks fails loudly when a pinned expected_chunk_hash has no
// faq_chunks row (stale index vs frozen fixture).
func assertPinnedChunks(ctx context.Context, pool *pgxpool.Pool, scored []goldenQ) error {
	rows, err := pool.Query(ctx, `SELECT content_hash FROM faq_chunks`)
	if err != nil {
		return fmt.Errorf("list chunk hashes: %w", err)
	}
	defer rows.Close()
	known := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return fmt.Errorf("scan chunk hash: %w", err)
		}
		known[h] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate chunk hashes: %w", err)
	}
	for _, q := range scored {
		if !known[*q.ExpectedChunkHash] {
			return fmt.Errorf("INTEGRITY FAIL: pinned chunk %s for question n=%d is absent from faq_chunks (stale index or stale fixture)",
				*q.ExpectedChunkHash, q.N)
		}
	}
	return nil
}

func splitGolden(g *goldenSet) (scored []goldenQ, dropped []droppedQ) {
	for _, q := range g.Questions {
		if q.ExpectedChunkHash == nil || *q.ExpectedChunkHash == "" {
			reason := q.UnresolvableReason
			if reason == "" {
				reason = "no pinned chunk hash in fixture"
			}
			dropped = append(dropped, droppedQ{N: q.N, Topic: q.Topic, Reason: reason})
			continue
		}
		if q.ExpectedSourceURL == nil {
			q.ExpectedSourceURL = new(string)
		}
		scored = append(scored, q)
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].N < scored[j].N })
	return scored, dropped
}

func aggregateResults(results []qResult) aggregate {
	var agg aggregate
	n := float64(len(results))
	for _, r := range results {
		if r.Hit1 {
			agg.Hit1++
		}
		if r.Hit3 {
			agg.Hit3++
		}
		if r.Hit5 {
			agg.Hit5++
		}
		agg.MRR += r.RR
	}
	agg.Hit1 /= n
	agg.Hit3 /= n
	agg.Hit5 /= n
	agg.MRR /= n
	return agg
}

// costEstimate builds the chars→tokens→price estimate and, when possible,
// measures the real token count with one batched /embeddings call.
func costEstimate(questions []string, model, baseURL, apiKey string, skip bool) costInfo {
	total := 0
	for _, q := range questions {
		total += utf8.RuneCountInString(q)
	}
	est := int(math.Ceil(float64(total) / charsPerTokenEstimate))
	c := costInfo{
		TotalChars:      total,
		EstTokens:       est,
		PricePerMillion: pricePerMillionTokens,
		EstCostUSD:      float64(est) / 1e6 * pricePerMillionTokens,
	}
	if skip || apiKey == "" {
		c.ProbeNote = "usage probe skipped; cost is the chars/4 estimate only"
		return c
	}
	measured, err := probeUsage(questions, model, baseURL, apiKey)
	if err != nil {
		c.ProbeNote = fmt.Sprintf("usage probe failed (%v); cost is the chars/4 estimate only", err)
		return c
	}
	c.ProbeOK = true
	c.MeasuredTokens = measured
	c.MeasuredCostUSD = float64(measured) / 1e6 * pricePerMillionTokens
	return c
}

func probeUsage(questions []string, model, baseURL, apiKey string) (int, error) {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	body, _ := json.Marshal(map[string]any{"model": model, "input": questions})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/embeddings", strings.NewReader(string(body)))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("embeddings probe http %d", resp.StatusCode)
	}
	var parsed struct {
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, err
	}
	if parsed.Usage.TotalTokens <= 0 {
		return 0, fmt.Errorf("usage.total_tokens missing")
	}
	return parsed.Usage.TotalTokens, nil
}

func readGolden(path string) (*goldenSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read golden set: %w", err)
	}
	var g goldenSet
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("parse golden set %s: %w", path, err)
	}
	if len(g.Questions) == 0 {
		return nil, fmt.Errorf("golden set %s has no questions", path)
	}
	return &g, nil
}

func writeJSON(path string, out evalOutput) error {
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

// snippet collapses whitespace and truncates to n runes.
func snippet(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n]) + "…"
}

// loadDotEnv mirrors internal/cmd's .env loader (KEY=VALUE, already-set
// environment variables win, a missing file is not an error).
func loadDotEnv(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil // no .env is normal
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			continue
		}
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
	return nil
}
