// Command qayala-usage measures the repository's real chat use case with live
// OpenAI calls, embedding, pgvector retrieval, persisted memory and compaction.
// It creates and drops its own local database; application data is never used.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	embedopenai "github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/openai/openai-go/v3/responses"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/repositories/postgres"
	faqtool "github.com/adityapratamanusantara/fantastic/internal/adapters/tools/faq"
	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/chat"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/faqindex"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
	"github.com/adityapratamanusantara/fantastic/migrations"
)

const agentID = "qayala-usage-benchmark"
const prompt = `Kamu chatbot pusat bantuan FAQ Qayala. Jawab dalam bahasa Indonesia, ringkas dan jelas.
Untuk setiap pertanyaan baru tentang Qayala atau integrasi LinkAja, gunakan search_faq sebelum menjawab, termasuk bila topik ada dalam history.
Jawab hanya berdasarkan hasil pencarian. Bedakan fitur MVP dari fase berikutnya. Jangan melakukan transaksi atau tindakan akun.
Jangan meminta PIN atau OTP. Tidak perlu menampilkan referensi sumber. Jika FAQ tidak cukup, minta klarifikasi tanpa mengarang.`

type fixture struct {
	SourceURL string                               `json:"source_url"`
	Version   int                                  `json:"source_version"`
	Sections  []struct{ ID, Title, Answer string } `json:"sections"`
	Question  string                               `json:"question"`
	Questions []string                             `json:"questions,omitempty"`
	Warmup    []string                             `json:"warmup_questions"`
	Sessions  [][]string                           `json:"session_question_sequences,omitempty"`
}

type metric struct {
	Case                  string         `json:"case"`
	Stage                 string         `json:"stage"`
	Model                 string         `json:"model"`
	Effort                string         `json:"reasoning_effort,omitempty"`
	ResponseID            string         `json:"response_id"`
	Status                string         `json:"status"`
	HTTPStatus            int            `json:"http_status"`
	Input                 int64          `json:"input_tokens"`
	Output                int64          `json:"output_tokens"`
	Reasoning             int64          `json:"reasoning_tokens"`
	Cached                int64          `json:"cached_input_tokens"`
	CacheWrite            int64          `json:"cache_write_tokens"`
	InputItems            int            `json:"input_items"`
	InputTypes            map[string]int `json:"input_types"`
	Seconds               float64        `json:"seconds"`
	FunctionCalls         int            `json:"function_calls,omitempty"`
	FunctionArgumentBytes int            `json:"function_argument_bytes,omitempty"`
	ArgumentDeltaBytes    int            `json:"argument_delta_bytes,omitempty"`
}
type usageResponse struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Status string `json:"status"`
	Usage  struct {
		Input  int64 `json:"input_tokens"`
		Output int64 `json:"output_tokens"`
		Prompt int64 `json:"prompt_tokens"`
		Total  int64 `json:"total_tokens"`
		In     struct {
			Cached int64 `json:"cached_tokens"`
			Write  int64 `json:"cache_write_tokens"`
		} `json:"input_tokens_details"`
		Out struct {
			Reasoning int64 `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
}
type caseResult struct {
	ID                 string         `json:"id"`
	Question           string         `json:"question,omitempty"`
	Model              string         `json:"model"`
	Effort             string         `json:"effort"`
	Repeat             int            `json:"repeat"`
	ConversationID     uuid.UUID      `json:"conversation_id"`
	Answer             string         `json:"answer"`
	Error              string         `json:"error,omitempty"`
	ToolResults        int            `json:"tool_results"`
	LoadedRows         int            `json:"loaded_history_rows"`
	HistoryRoles       map[string]int `json:"history_roles"`
	HistoryAnswers     int            `json:"history_final_answers"`
	HistoryFingerprint string         `json:"history_fingerprint"`
	SessionTurn        int            `json:"session_turn,omitempty"`
	BeforeSummary      int64          `json:"summary_until_before"`
	AfterSummary       int64          `json:"summary_until_after"`
	Summary            string         `json:"updated_summary"`
	Requests           []metric       `json:"requests"`
}
type recorder struct {
	mu    sync.Mutex
	calls []metric
}
type caseKey struct{}
type transport struct {
	base     http.RoundTripper
	recorder *recorder
}
type observedBody struct {
	io.ReadCloser
	data bytes.Buffer
	once sync.Once
	done func([]byte)
}

func (b *observedBody) finish() { b.once.Do(func() { b.done(b.data.Bytes()) }) }
func (b *observedBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	b.data.Write(p[:n])
	if e != nil {
		b.finish()
	}
	return n, e
}
func (b *observedBody) Close() error { e := b.ReadCloser.Close(); b.finish(); return e }

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Observe usage on the wire, including terminal SSE usage that the chat
	// event API does not expose. Never capture headers, credentials or vectors.
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body.Close()
	var payload map[string]any
	if err = json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	modelName, _ := payload["model"].(string)
	isResponse := strings.HasSuffix(req.URL.Path, "/responses")
	if isResponse {
		payload["prompt_cache_options"] = map[string]any{"mode": "explicit"}
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))
	clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	caseID, _ := req.Context().Value(caseKey{}).(string)
	stage := "embedding"
	if isResponse {
		if stream, _ := payload["stream"].(bool); stream {
			stage = "agent"
		} else {
			stage = "summary"
		}
	}
	m := metric{Case: caseID, Stage: stage, Model: modelName, InputTypes: map[string]int{}}
	if reasoning, ok := payload["reasoning"].(map[string]any); ok {
		m.Effort, _ = reasoning["effort"].(string)
	}
	if items, ok := payload["input"].([]any); ok {
		m.InputItems = len(items)
		for _, v := range items {
			if o, ok := v.(map[string]any); ok {
				k, _ := o["type"].(string)
				if k == "" {
					k, _ = o["role"].(string)
				}
				m.InputTypes[k]++
			}
		}
	}
	started := time.Now()
	resp, err := t.base.RoundTrip(clone)
	if err != nil {
		return nil, err
	}
	m.HTTPStatus = resp.StatusCode
	isSSE := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")
	resp.Body = &observedBody{ReadCloser: resp.Body, done: func(raw []byte) {
		var u usageResponse
		if isSSE {
			scanner := bufio.NewScanner(bytes.NewReader(raw))
			scanner.Buffer(make([]byte, 4096), 8<<20)
			for scanner.Scan() {
				line := scanner.Text()
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				var event struct {
					Type     string        `json:"type"`
					Response usageResponse `json:"response"`
					Delta    string        `json:"delta"`
					Item     struct {
						Type      string `json:"type"`
						Arguments string `json:"arguments"`
					} `json:"item"`
				}
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
					continue
				}
				if event.Type == "response.function_call_arguments.delta" {
					m.ArgumentDeltaBytes += len(event.Delta)
				}
				if event.Type == "response.output_item.done" && event.Item.Type == "function_call" {
					m.FunctionCalls++
					m.FunctionArgumentBytes += len(event.Item.Arguments)
				}
				if event.Type == "response.completed" || event.Type == "response.incomplete" || event.Type == "response.failed" {
					u = event.Response
				}
			}
		} else {
			_ = json.Unmarshal(raw, &u)
		}
		m.ResponseID = u.ID
		m.Status = u.Status
		m.Input = u.Usage.Input
		m.Output = u.Usage.Output
		m.Reasoning = u.Usage.Out.Reasoning
		m.Cached = u.Usage.In.Cached
		m.CacheWrite = u.Usage.In.Write
		if !isResponse {
			m.Input = u.Usage.Prompt
			if m.Input == 0 {
				m.Input = u.Usage.Total
			}
			if m.HTTPStatus == 200 {
				m.Status = "completed"
			}
		}
		m.Seconds = time.Since(started).Seconds()
		t.recorder.mu.Lock()
		t.recorder.calls = append(t.recorder.calls, m)
		t.recorder.mu.Unlock()
	}}
	return resp, nil
}
func (r *recorder) forCase(id string) []metric {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []metric
	for _, m := range r.calls {
		if m.Case == id {
			out = append(out, m)
		}
	}
	return out
}

// Only the tool's model-facing description is adapted to Qayala. Execution
// remains the repository's original embed-query -> SQL pgvector search path.
type qayalaTool struct{ tool.InvokableTool }

func (t qayalaTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, e := t.InvokableTool.Info(ctx)
	if e != nil {
		return nil, e
	}
	copy := *info
	copy.Desc = "Search the Qayala MVP FAQ, including registration, prayer times, Quran, Journey, Lifestyle, LinkAja Syariah account binding and donations. Call before answering each Qayala question. Results contain text, source_url, title and similarity score."
	return &copy, nil
}

type runner struct {
	pool              *pgxpool.Pool
	store             driven.ConversationStore
	cfg               *config.Config
	key               string
	client            *http.Client
	rec               *recorder
	log               *slog.Logger
	historyWindow     int
	parallelToolCalls *bool
}

func (r *runner) service(ctx context.Context, modelName, effort string) (*chat.Service, error) {
	noStore := false
	zero := 0
	m, e := agenticopenai.NewResponsesModel(ctx, &agenticopenai.ResponsesConfig{APIKey: r.key, BaseURL: "https://api.openai.com/v1", Model: modelName, HTTPClient: r.client, Reasoning: &responses.ReasoningParam{Effort: responses.ReasoningEffort(effort)}, MaxTokens: &r.cfg.LLM.MaxTokens, Store: &noStore, MaxRetries: &zero, ParallelToolCalls: r.parallelToolCalls})
	if e != nil {
		return nil, e
	}
	emb, e := embedopenai.NewEmbedder(ctx, &embedopenai.EmbeddingConfig{APIKey: r.key, Model: r.cfg.FAQ.Embedding.Model, HTTPClient: r.client})
	if e != nil {
		return nil, e
	}
	t, e := faqtool.NewSearchTool(postgres.NewFaqSearchStore(r.pool), emb, r.cfg.FAQ.Search.TopK)
	if e != nil {
		return nil, e
	}
	inv, ok := t.(tool.InvokableTool)
	if !ok {
		return nil, errors.New("search_faq is not invokable")
	}
	return chat.NewService(r.store, m, []tool.BaseTool{qayalaTool{inv}}, chat.Config{AgentName: agentID, SystemPrompt: prompt, HistoryWindow: r.historyWindow}, r.log)
}
func (r *runner) turn(ctx context.Context, svc *chat.Service, c *domain.Conversation, text string) (caseResult, error) {
	id, _ := ctx.Value(caseKey{}).(string)
	out := caseResult{ID: id, Question: text, ConversationID: c.ID, HistoryRoles: map[string]int{}}
	before, recent, e := r.store.LoadWindow(ctx, c.ID, c.UserID, r.historyWindow)
	if e != nil {
		return out, e
	}
	out.BeforeSummary = before.SummaryUntilSeq
	out.HistoryFingerprint = historyFingerprint(before.RollingSummary, recent)
	out.LoadedRows = len(recent)
	for _, m := range recent {
		out.HistoryRoles[string(m.Role)]++
		if m.Role == domain.RoleAssistant && len(m.ToolCalls) == 0 {
			out.HistoryAnswers++
		}
	}
	stream, e := svc.Stream(ctx, domain.Principal{UserID: c.UserID}, c.ID, agentID, text)
	if e != nil {
		return out, e
	}
	for ev := range stream {
		switch ev.Type {
		case chat.EventDone:
			out.Answer = ev.Content
		case chat.EventToolResult:
			out.ToolResults++
		case chat.EventError:
			out.Error = ev.Error
		}
	}
	if out.Error != "" {
		out.Requests = r.rec.forCase(id)
		return out, errors.New(out.Error)
	}
	_, afterWindow, e := r.store.LoadWindow(ctx, c.ID, c.UserID, r.historyWindow)
	if e != nil {
		return out, e
	}
	want := int64(0)
	if len(afterWindow) > 0 {
		want = afterWindow[0].Seq - 1
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		conv, e := r.store.GetConversation(ctx, c.ID, c.UserID)
		if e != nil {
			return out, e
		}
		if conv.SummaryUntilSeq >= want {
			out.AfterSummary = conv.SummaryUntilSeq
			out.Summary = conv.RollingSummary
			break
		}
		if time.Now().After(deadline) {
			return out, fmt.Errorf("summary did not finish (want seq %d)", want)
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	out.Requests = r.rec.forCase(id)
	for _, m := range out.Requests {
		if m.HTTPStatus != 200 || m.Status != "completed" || m.Input == 0 || m.Cached != 0 || m.CacheWrite != 0 {
			return out, fmt.Errorf("invalid observed usage: stage=%s status=%s HTTP=%d", m.Stage, m.Status, m.HTTPStatus)
		}
	}
	return out, nil
}

// historyFingerprint ignores clone IDs/timestamps and checks that every trial
// gets exactly the same summary, text, tool arguments, and tool results.
func historyFingerprint(summary string, recent []domain.Message) string {
	type row struct {
		Role       domain.Role
		Content    string
		ToolCalls  []domain.ToolCall
		ToolCallID string
	}
	rows := make([]row, 0, len(recent))
	for _, m := range recent {
		rows = append(rows, row{m.Role, m.Content, m.ToolCalls, m.ToolCallID})
	}
	raw, _ := json.Marshal(struct {
		Summary string
		Rows    []row
	}{summary, rows})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func loadEnv() error {
	raw, e := os.ReadFile(".env")
	if e != nil {
		return e
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if ok {
			key = strings.TrimSpace(key)
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, strings.Trim(strings.TrimSpace(val), "\"'"))
			}
		}
	}
	return nil
}
func writeJSON(path string, v any) error {
	raw, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(raw, '\n'), 0644)
}

func isolatedDatabaseURL(raw, database string) (string, error) {
	if !strings.HasPrefix(database, "qayala_bench_") || len(database) <= len("qayala_bench_") {
		return "", errors.New("benchmark database name must use its dedicated prefix")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", errors.New("benchmark requires a postgres URL")
	}
	if q := u.Query(); q.Has("dbname") || q.Has("database") {
		return "", errors.New("database must be specified in URL path only")
	}
	u.Path, u.RawPath = "/"+database, ""
	return u.String(), nil
}

func run() error {
	fixturePath := flag.String("fixture", "scripts/spike/qayala-usage/fixture.json", "Qayala FAQ fixture")
	outDir := flag.String("out", "/tmp/qayala-repo-usage", "output directory")
	repeats := flag.Int("repeats", 5, "repeated turns per configuration")
	modelsFlag := flag.String("models", "gpt-6-luna,gpt-6.1-terra", "comma-separated supported models")
	historyTurns := flag.Int("history-turns", 0, "retain this many complete warmup Q/A turns, with their tool messages; 0 retains the original 20-row window")
	sequentialTools := flag.Bool("sequential-tools", false, "disable parallel function calls for this benchmark")
	sessionAverage := flag.Bool("session-average", false, "measure empty-start sessions and derive 1/3/5/15-turn prefixes")
	flag.Parse()
	if *repeats < 1 || *repeats > 10 {
		return errors.New("repeats must be 1..10")
	}
	models := strings.Split(*modelsFlag, ",")
	for _, modelName := range models {
		if modelName != "gpt-6-luna" && modelName != "gpt-6.1-terra" {
			return errors.New("models must be gpt-6-luna or gpt-6.1-terra")
		}
	}
	if *historyTurns < 0 || *historyTurns > 20 {
		return errors.New("history-turns must be 0..20")
	}
	if e := loadEnv(); e != nil {
		return e
	}
	cfg, _, e := config.Load("config.yaml")
	if e != nil {
		return e
	}
	key, e := cfg.APIKey()
	if e != nil {
		return e
	}
	raw, e := os.ReadFile(*fixturePath)
	if e != nil {
		return e
	}
	var fx fixture
	if e = json.Unmarshal(raw, &fx); e != nil {
		return e
	}
	if len(fx.Sections) == 0 || len(fx.Warmup) < 6 {
		return errors.New("fixture requires FAQ sections and at least six warmup turns")
	}
	questions := fx.Questions
	if len(questions) == 0 && fx.Question != "" {
		questions = []string{fx.Question}
	}
	if len(questions) == 0 || len(questions) > 10 {
		return errors.New("fixture requires 1..10 questions")
	}
	if *historyTurns > 0 && len(fx.Warmup) <= *historyTurns {
		return errors.New("warmup must exceed history-turns to create a rolling summary")
	}
	if *sessionAverage {
		if *historyTurns != 10 || !*sequentialTools || len(fx.Sessions) < *repeats {
			return errors.New("session average requires history-turns=10, sequential-tools and enough question sequences")
		}
		for _, sequence := range fx.Sessions[:*repeats] {
			if len(sequence) != 15 {
				return errors.New("each session sequence must contain 15 questions")
			}
		}
	}
	if e = os.MkdirAll(*outDir, 0755); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	pc, e := pgxpool.ParseConfig(cfg.Database.URL)
	if e != nil {
		return errors.New("invalid database config")
	}
	host := pc.ConnConfig.Host
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return errors.New("benchmark requires a local PostgreSQL host")
	}
	pc.ConnConfig.Database = "postgres"
	admin, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		return e
	}
	defer admin.Close()
	dbName := "qayala_bench_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, e = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); e != nil {
		return errors.New("cannot create isolated benchmark database")
	}
	defer func() {
		_, err := admin.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{dbName}.Sanitize()+" WITH (FORCE)")
		if err != nil {
			fmt.Fprintln(os.Stderr, "cleanup failed for benchmark database", dbName)
		} else {
			fmt.Println("isolated benchmark database removed", dbName)
		}
	}()
	// ConnConfig.ConnString preserves the original DSN, so changing Database
	// on a copied config is insufficient. Build an explicit URL and verify
	// current_database() before migrations or any fixture write.
	benchURL, e := isolatedDatabaseURL(cfg.Database.URL, dbName)
	if e != nil {
		return e
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pool, e := pgxpool.New(ctx, benchURL)
	if e != nil {
		return e
	}
	defer pool.Close()
	var connectedDatabase string
	if e = pool.QueryRow(ctx, "SELECT current_database()").Scan(&connectedDatabase); e != nil {
		return e
	}
	if connectedDatabase != dbName {
		return errors.New("database isolation verification failed")
	}
	fmt.Println("database isolation verified", connectedDatabase)
	if e = migrations.Up(benchURL, log); e != nil {
		return e
	}
	if _, e = pool.Exec(ctx, `INSERT INTO agents (id,display_name,system_prompt,tools) VALUES ($1,'Qayala benchmark',$2,ARRAY['search_faq'])`, agentID, prompt); e != nil {
		return e
	}
	rec := &recorder{}
	client := &http.Client{Timeout: 3 * time.Minute, Transport: &transport{base: http.DefaultTransport, recorder: rec}}
	initialWindow := 20
	if *historyTurns > 0 {
		initialWindow = 4 * *historyTurns
	}
	r := &runner{pool: pool, store: postgres.NewStore(pool), cfg: cfg, key: key, client: client, rec: rec, log: log, historyWindow: initialWindow}
	if *sequentialTools {
		parallel := false
		r.parallelToolCalls = &parallel
	}
	indexCtx := context.WithValue(ctx, caseKey{}, "indexing")
	emb, e := embedopenai.NewEmbedder(indexCtx, &embedopenai.EmbeddingConfig{APIKey: key, Model: cfg.FAQ.Embedding.Model, HTTPClient: client})
	if e != nil {
		return e
	}
	corpus := &faqindex.Corpus{SchemaVersion: 1, Source: fx.SourceURL, ScrapedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, s := range fx.Sections {
		corpus.Units = append(corpus.Units, faqindex.Unit{ID: s.ID, SourceURL: fx.SourceURL + "#" + s.ID, Question: s.Title, Answer: s.Answer, Category: s.Title, Language: "id", ContentHash: faqindex.UnitContentHash(s.Title, s.Answer), ScrapedAt: corpus.ScrapedAt})
	}
	indexed, e := faqindex.NewPipeline(emb, postgres.NewFaqIndexStore(pool), 100).IndexCorpus(indexCtx, corpus, faqindex.IndexOptions{Model: cfg.FAQ.Embedding.Model, Dims: cfg.FAQ.Embedding.Dims, ManifestPath: filepath.Join(*outDir, "index-manifest.json")})
	if e != nil {
		return e
	}
	fmt.Printf("indexed Qayala fixture: %d documents, %d chunks; top_k=%d; initial_history_window=%d; max_output_tokens=%d\n", indexed.Documents, indexed.Chunks, cfg.FAQ.Search.TopK, r.historyWindow, cfg.LLM.MaxTokens)
	if *sessionAverage {
		return runSessionAverage(ctx, r, fx, models, *repeats, *outDir, connectedDatabase)
	}
	baseline := domain.NewConversation("qayala-benchmark", agentID)
	if e = r.store.CreateConversation(ctx, baseline); e != nil {
		return e
	}
	svc, e := r.service(ctx, "gpt-6-luna", "low")
	if e != nil {
		return e
	}
	var warmup []caseResult
	for i, q := range fx.Warmup {
		turnCtx := context.WithValue(ctx, caseKey{}, fmt.Sprintf("warmup-%d", i+1))
		res, e := r.turn(turnCtx, svc, baseline, q)
		warmup = append(warmup, res)
		_ = writeJSON(filepath.Join(*outDir, "warmup.json"), warmup)
		if e != nil {
			return fmt.Errorf("warmup %d: %w", i+1, e)
		}
		fmt.Printf("warmup %d: tool results=%d, requests=%d, summary_until=%d\n", i+1, res.ToolResults, len(res.Requests), res.AfterSummary)
	}
	source, e := r.store.GetConversation(ctx, baseline.ID, baseline.UserID)
	if e != nil {
		return e
	}
	sourceMessages, e := r.store.GetMessages(ctx, baseline.ID, baseline.UserID)
	if e != nil {
		return e
	}
	if source.RollingSummary == "" {
		return errors.New("warmup did not create rolling summary")
	}
	if e = writeJSON(filepath.Join(*outDir, "baseline.json"), map[string]any{"conversation": source, "messages": sourceMessages}); e != nil {
		return e
	}
	if *historyTurns > 0 {
		// Derive a complete-turn boundary from the actual warmup instead of
		// assuming four rows per turn. Tool calls/results remain intact.
		seen := 0
		for i := len(sourceMessages) - 1; i >= 0; i-- {
			if sourceMessages[i].Role == domain.RoleUser {
				seen++
				if seen == *historyTurns {
					r.historyWindow = len(sourceMessages) - i
					break
				}
			}
		}
		if seen != *historyTurns {
			return errors.New("not enough complete warmup turns")
		}
		start := sourceMessages[len(sourceMessages)-r.historyWindow].Seq
		if source.SummaryUntilSeq != start-1 {
			return errors.New("summary boundary does not match complete warmup history")
		}
		fmt.Printf("complete warmup history: %d Q/A turns in %d raw rows\n", *historyTurns, r.historyWindow)
	}
	var results []caseResult
	for _, effort := range []string{"low", "medium"} {
		for _, modelName := range models {
			for questionIndex, question := range questions {
				for repeat := 1; repeat <= *repeats; repeat++ {
					id := fmt.Sprintf("%s-%s-%d", modelName, effort, repeat)
					if len(questions) > 1 {
						id = fmt.Sprintf("%s-%s-q%d-%d", modelName, effort, questionIndex+1, repeat)
					}
					caseCtx := context.WithValue(ctx, caseKey{}, id)
					conv := domain.NewConversation("qayala-benchmark", agentID)
					conv.RollingSummary = source.RollingSummary
					conv.SummaryUntilSeq = source.SummaryUntilSeq
					if e = r.store.CreateConversation(ctx, conv); e != nil {
						return e
					}
					var cloned []*domain.Message
					for _, msg := range sourceMessages {
						m := domain.NewMessage(conv.ID, msg.Role, msg.Content)
						m.ToolCalls = msg.ToolCalls
						m.ToolCallID = msg.ToolCallID
						cloned = append(cloned, m)
					}
					if _, e = r.store.AppendMessages(ctx, conv.ID, conv.UserID, cloned, nil); e != nil {
						return e
					}
					svc, e = r.service(caseCtx, modelName, effort)
					if e != nil {
						return e
					}
					res, turnErr := r.turn(caseCtx, svc, conv, question)
					res.Model = modelName
					res.Effort = effort
					res.Repeat = repeat
					if turnErr != nil {
						res.Error = turnErr.Error()
					}
					results = append(results, res)
					if e = writeJSON(filepath.Join(*outDir, "results.json"), results); e != nil {
						return e
					}
					fmt.Printf("%s: history=%d, tools=%d, requests=%d, summary=%d->%d, error=%q\n", id, res.LoadedRows, res.ToolResults, len(res.Requests), res.BeforeSummary, res.AfterSummary, res.Error)
					if turnErr != nil {
						return fmt.Errorf("case %s failed: %w", id, turnErr)
					}
				}
			}
		}
	}
	meta := map[string]any{"generated_at": time.Now().UTC(), "question": fx.Question, "source_url": fx.SourceURL, "source_version": fx.Version, "history_window_rows": 20, "top_k": cfg.FAQ.Search.TopK, "max_output_tokens": cfg.LLM.MaxTokens, "repeats": *repeats, "indexing_requests": rec.forCase("indexing"), "warmup_turns": len(warmup), "warmup_rows": len(sourceMessages), "warmup_summary_until": source.SummaryUntilSeq, "warmup_summary": source.RollingSummary, "caching": "disabled", "test_scope": "chat.NewService -> Eino ADK -> search_faq -> live embedding -> PostgreSQL pgvector -> streaming answer -> persisted messages -> asynchronous rolling summary; HTTP/auth not exercised"}
	meta["database_isolation_verified"] = true
	meta["database_name"] = connectedDatabase
	meta["history_window_rows"] = r.historyWindow
	meta["history_qa_turns_requested"] = *historyTurns
	meta["models"] = models
	meta["questions"] = questions
	meta["agent_instruction"] = prompt
	meta["agent_instruction_changed_from_previous_benchmark"] = false
	meta["parallel_tool_calls_disabled"] = *sequentialTools
	return writeJSON(filepath.Join(*outDir, "metadata.json"), meta)
}
func main() {
	if e := run(); e != nil {
		msg := e.Error()
		if key := os.Getenv("OPENAI_API_KEY"); key != "" {
			msg = strings.ReplaceAll(msg, key, "[REDACTED]")
		}
		if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
			msg = strings.ReplaceAll(msg, dsn, "[REDACTED]")
		}
		fmt.Fprintln(os.Stderr, "qayala-usage:", msg)
		os.Exit(1)
	}
}
