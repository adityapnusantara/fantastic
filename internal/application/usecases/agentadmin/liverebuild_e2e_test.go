package agentadmin_test

// Task 6.3 — live-rebuild behavior end to end at the application layer
// (design D4, spec delta "Live rebuild on management changes").
//
// This test wires the real chat usecase, agent registry and agent admin
// layers together over scripted fakes (no bootstrap, no HTTP) and proves
// the two acceptance scenarios plus the disable/enable round trip:
//
//   A. A turn streamed from the OLD *chat.Service blocks inside the model
//      call; while it is in flight, admin.Update swaps the registry entry
//      to a NEW service built from the new prompt. The blocked turn is
//      then released and must complete having received the OLD prompt —
//      the swap replaces the map entry, never the handed-out pointer.
//   B. The next turn, taken from the registry AFTER the update (what the
//      chat handler does per request), must receive the NEW prompt.
//   C. SetEnabled(false) makes registry.Get miss; SetEnabled(true)
//      rebuilds from the stored row and Get hits again with a fresh
//      service that serves the stored prompt.
//
// The evidence is the recorded model input: the ADK agent prepends the
// service's instruction (the agent's system prompt) as the first system
// message of every model call, so asserting on the system text the fake
// model received is asserting on the definition the turn actually ran
// with. Everything runs under -race: the rebuild happens on the test
// goroutine while the in-flight turn streams on the run goroutine.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/application/agentregistry"
	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/agentadmin"
	chatusecase "github.com/adityapratamanusantara/fantastic/internal/application/usecases/chat"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// live-rebuild test fixtures. Names are distinct from the unit-test
// fakes in service_test.go (same test package, separate file).
const (
	liveUserID = "user-live-rebuild"
	liveAgent  = "faq"
	promptOne  = "OLD PROMPT P1: answer strictly from the FAQ index."
	promptTwo  = "NEW PROMPT P2: answer with a limerick and then cite sources."
)

// liveTime is the fake database clock the in-memory stores stamp rows
// with ("timestamps are assigned by the database").
var liveTime = time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC)

// scriptedModel is a fake model.AgenticModel whose calls are recorded and
// whose first Stream call can be parked on a gate, so the test controls a
// turn that is in flight inside the model while the registry is rebuilt.
type scriptedModel struct {
	mu sync.Mutex

	streamCalls    int
	streamInputs   [][]*schema.AgenticMessage
	generateCalls  int
	generateInputs [][]*schema.AgenticMessage

	// started is closed by the first Stream call once its input has been
	// recorded; gate parks that call until closed. Both are armed by
	// holdFirstTurn before the turn starts.
	started chan struct{}
	gate    chan struct{}
	reply   string // assistant text this model produces on every call
}

func newScriptedModel(reply string) *scriptedModel {
	return &scriptedModel{reply: reply}
}

var _ model.AgenticModel = (*scriptedModel)(nil)

// holdFirstTurn arms the parking of the model's first Stream call.
func (m *scriptedModel) holdFirstTurn() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = make(chan struct{})
	m.gate = make(chan struct{})
}

// releaseTurn lets the parked first Stream call proceed.
func (m *scriptedModel) releaseTurn() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gate != nil {
		close(m.gate)
	}
}

// waitTurnStarted blocks until the first Stream call has recorded its
// input (the turn is in flight inside the model), or times out.
func (m *scriptedModel) waitTurnStarted(timeout time.Duration) error {
	m.mu.Lock()
	started := m.started
	m.mu.Unlock()
	if started == nil {
		return fmt.Errorf("holdFirstTurn was not armed before the turn started")
	}
	select {
	case <-started:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("turn never reached the model within %s", timeout)
	}
}

func (m *scriptedModel) streamCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.streamCalls
}

// streamSystemPrompt returns the text of the first system message the
// given Stream call received — the instruction the ADK agent injected
// for that turn.
func (m *scriptedModel) streamSystemPrompt(call int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if call >= len(m.streamInputs) {
		return ""
	}
	return firstSystemText(m.streamInputs[call])
}

// streamInputText returns every text block of the given Stream call's
// input, concatenated, for absence checks.
func (m *scriptedModel) streamInputText(call int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if call >= len(m.streamInputs) {
		return ""
	}
	var b string
	for _, msg := range m.streamInputs[call] {
		if msg == nil {
			continue
		}
		for _, block := range msg.ContentBlocks {
			if block != nil && block.UserInputText != nil {
				b += block.UserInputText.Text + "\n"
			}
			if block != nil && block.AssistantGenText != nil {
				b += block.AssistantGenText.Text + "\n"
			}
		}
	}
	return b
}

func (m *scriptedModel) Stream(ctx context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	m.mu.Lock()
	idx := m.streamCalls
	m.streamCalls++
	// Snapshot what the model was given: the recorded input is the
	// definition the turn runs with.
	m.streamInputs = append(m.streamInputs, append([]*schema.AgenticMessage(nil), input...))
	started, gate := m.started, m.gate
	m.mu.Unlock()

	if idx == 0 && started != nil {
		close(started)
	}
	if idx == 0 && gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// One full assistant chunk; the service streams it through.
	sr, sw := schema.Pipe[*schema.AgenticMessage](2)
	go func() {
		defer sw.Close()
		sw.Send(assistantText(m.reply), nil)
	}()
	return sr, nil
}

func (m *scriptedModel) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	m.mu.Lock()
	m.generateCalls++
	m.generateInputs = append(m.generateInputs, append([]*schema.AgenticMessage(nil), input...))
	m.mu.Unlock()
	// Used only by the post-turn summarizer; the content is irrelevant.
	return assistantText("compact-summary"), nil
}

func assistantText(text string) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{{
			Type:             schema.ContentBlockTypeAssistantGenText,
			AssistantGenText: &schema.AssistantGenText{Text: text},
		}},
	}
}

// firstSystemText extracts the text of the first system-role message —
// the instruction the ADK ChatModelAgent prepends to the model input.
func firstSystemText(input []*schema.AgenticMessage) string {
	for _, msg := range input {
		if msg == nil || msg.Role != schema.AgenticRoleTypeSystem {
			continue
		}
		for _, block := range msg.ContentBlocks {
			if block != nil && block.UserInputText != nil {
				return block.UserInputText.Text
			}
		}
	}
	return ""
}

// memAgentStore is an in-memory driven.AgentStore mimicking the store
// contract: unknown id -> domain.ErrNotFound, timestamps assigned by the
// "database", created_at never rewritten by Update.
type memAgentStore struct {
	mu   sync.Mutex
	rows map[string]domain.Agent
}

func newMemAgentStore() *memAgentStore {
	return &memAgentStore{rows: make(map[string]domain.Agent)}
}

var _ driven.AgentStore = (*memAgentStore)(nil)

func (s *memAgentStore) List(_ context.Context) ([]domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.Agent, 0, len(s.rows))
	for _, a := range s.rows {
		out = append(out, a)
	}
	return out, nil
}

func (s *memAgentStore) Get(_ context.Context, id string) (*domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.rows[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := a
	return &cp, nil
}

func (s *memAgentStore) Create(_ context.Context, agent *domain.Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rows[agent.ID]; exists {
		return fmt.Errorf("duplicate agent id %q", agent.ID)
	}
	stored := *agent
	stored.CreatedAt = liveTime
	stored.UpdatedAt = liveTime
	s.rows[agent.ID] = stored
	return nil
}

func (s *memAgentStore) CreateIfAbsent(_ context.Context, agent *domain.Agent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rows[agent.ID]; exists {
		return false, nil
	}
	stored := *agent
	stored.CreatedAt = liveTime
	stored.UpdatedAt = liveTime
	s.rows[agent.ID] = stored
	return true, nil
}

func (s *memAgentStore) Update(_ context.Context, agent *domain.Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.rows[agent.ID]
	if !ok {
		return domain.ErrNotFound
	}
	stored := *agent
	stored.CreatedAt = prev.CreatedAt
	stored.UpdatedAt = liveTime
	s.rows[agent.ID] = stored
	return nil
}

func (s *memAgentStore) SetEnabled(_ context.Context, id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.rows[id]
	if !ok {
		return domain.ErrNotFound
	}
	a.Enabled = enabled
	a.UpdatedAt = liveTime
	s.rows[id] = a
	return nil
}

func (s *memAgentStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[id]; !ok {
		return domain.ErrNotFound
	}
	delete(s.rows, id)
	return nil
}

// memConvStore is a thread-safe in-memory driven.ConversationStore: it
// persists messages the way the chat usecase drives it (seq assignment,
// user scoping, optional titles, rolling summary) so real turns run and
// persist against it, including the post-turn compaction goroutine.
type memConvStore struct {
	mu    sync.Mutex
	convs map[uuid.UUID]*domain.Conversation
	msgs  map[uuid.UUID][]domain.Message
}

func newMemConvStore() *memConvStore {
	return &memConvStore{
		convs: make(map[uuid.UUID]*domain.Conversation),
		msgs:  make(map[uuid.UUID][]domain.Message),
	}
}

var _ driven.ConversationStore = (*memConvStore)(nil)

func (s *memConvStore) CreateConversation(_ context.Context, conv *domain.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := *conv
	stored.CreatedAt = liveTime
	stored.UpdatedAt = liveTime
	s.convs[stored.ID] = &stored
	return nil
}

func (s *memConvStore) GetConversation(_ context.Context, conversationID uuid.UUID, userID string) (*domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(conversationID, userID)
}

func (s *memConvStore) getLocked(conversationID uuid.UUID, userID string) (*domain.Conversation, error) {
	conv, ok := s.convs[conversationID]
	if !ok || conv.UserID != userID {
		return nil, domain.ErrNotFound
	}
	cp := *conv
	return &cp, nil
}

func (s *memConvStore) ListConversations(_ context.Context, userID string, agentID *string) ([]domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.Conversation
	for _, conv := range s.convs {
		if conv.UserID != userID {
			continue
		}
		if agentID != nil && conv.AgentID != *agentID {
			continue
		}
		out = append(out, *conv)
	}
	return out, nil
}

func (s *memConvStore) DeleteConversation(_ context.Context, conversationID uuid.UUID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.getLocked(conversationID, userID); err != nil {
		return err
	}
	delete(s.convs, conversationID)
	delete(s.msgs, conversationID)
	return nil
}

func (s *memConvStore) CountByAgent(_ context.Context, agentID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, conv := range s.convs {
		if conv.AgentID == agentID {
			n++
		}
	}
	return n, nil
}

func (s *memConvStore) AppendMessage(_ context.Context, conversationID uuid.UUID, userID string, msg *domain.Message, title *string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, err := s.getLocked(conversationID, userID)
	if err != nil {
		return 0, err
	}
	stored := *msg
	stored.ConversationID = conversationID
	stored.Seq = int64(len(s.msgs[conversationID]) + 1)
	stored.CreatedAt = liveTime
	s.msgs[conversationID] = append(s.msgs[conversationID], stored)
	if title != nil && conv.Title == "" {
		conv.Title = *title
	}
	conv.UpdatedAt = liveTime
	return stored.Seq, nil
}

func (s *memConvStore) AppendMessages(ctx context.Context, conversationID uuid.UUID, userID string, msgs []*domain.Message, title *string) ([]int64, error) {
	seqs := make([]int64, 0, len(msgs))
	for _, m := range msgs {
		seq, err := s.AppendMessage(ctx, conversationID, userID, m, title)
		if err != nil {
			return nil, err
		}
		seqs = append(seqs, seq)
	}
	return seqs, nil
}

func (s *memConvStore) GetMessages(_ context.Context, conversationID uuid.UUID, userID string) ([]domain.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.getLocked(conversationID, userID); err != nil {
		return nil, err
	}
	return append([]domain.Message(nil), s.msgs[conversationID]...), nil
}

func (s *memConvStore) LoadWindow(_ context.Context, conversationID uuid.UUID, userID string, limit int) (*domain.Conversation, []domain.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, err := s.getLocked(conversationID, userID)
	if err != nil {
		return nil, nil, err
	}
	all := s.msgs[conversationID]
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	return conv, append([]domain.Message(nil), all...), nil
}

func (s *memConvStore) SaveSummary(_ context.Context, conversationID uuid.UUID, userID, summary string, untilSeq int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, err := s.getLocked(conversationID, userID)
	if err != nil {
		return err
	}
	conv.RollingSummary = summary
	conv.SummaryUntilSeq = untilSeq
	return nil
}

// liveFactory is the registry Factory seam: it builds a real
// chatusecase.Service per agent row over a fresh scriptedModel, and
// records every build so the test can address the model of a specific
// build (0 = create, then one per rebuild).
type liveFactory struct {
	mu     sync.Mutex
	convs  *memConvStore
	log    *slog.Logger
	built  []*scriptedModel
	prompt []string
}

func newLiveFactory(convs *memConvStore) *liveFactory {
	return &liveFactory{
		convs: convs,
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func (f *liveFactory) build(a domain.Agent) (agentregistry.BuildResult, error) {
	f.mu.Lock()
	reply := fmt.Sprintf("reply-%d", len(f.built)+1)
	m := newScriptedModel(reply)
	f.built = append(f.built, m)
	f.prompt = append(f.prompt, a.SystemPrompt)
	f.mu.Unlock()

	svc, err := chatusecase.NewService(f.convs, m, nil, chatusecase.Config{
		AgentName:        a.DisplayName,
		AgentDescription: a.Description,
		SystemPrompt:     a.SystemPrompt,
		HistoryWindow:    2,
	}, f.log)
	if err != nil {
		return agentregistry.BuildResult{}, fmt.Errorf("build chat service for agent %q: %w", a.ID, err)
	}
	return agentregistry.BuildResult{Service: svc}, nil
}

// model returns the scripted model of the i-th build (0-based).
func (f *liveFactory) model(i int) *scriptedModel {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.built) {
		return nil
	}
	return f.built[i]
}

// drainTurn consumes a turn's events until the channel closes (turn is
// over), with a hard timeout so a wedged turn fails instead of hanging.
func drainTurn(t *testing.T, ch <-chan chatusecase.Event, timeout time.Duration) []chatusecase.Event {
	t.Helper()
	var out []chatusecase.Event
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			t.Fatalf("timed out draining turn events; received %d events", len(out))
		}
	}
}

// requireCoherentTurn asserts a turn finished cleanly: no error event,
// the scripted reply streamed through the right agent, and the done
// event carries the persisted message id.
func requireCoherentTurn(t *testing.T, events []chatusecase.Event, wantAgent, wantReply string) {
	t.Helper()
	require.NotEmpty(t, events, "turn produced no events")
	var last chatusecase.Event
	var sawChunk bool
	for _, ev := range events {
		require.NotEqual(t, chatusecase.EventError, ev.Type, "turn ended in an error event: %s", ev.Error)
		if ev.Type == chatusecase.EventStreamChunk {
			require.Equal(t, wantAgent, ev.AgentName, "stream chunk carries the wrong agent identity")
			require.Equal(t, wantReply, ev.Content, "stream chunk content is not the scripted reply")
			sawChunk = true
		}
		last = ev
	}
	require.True(t, sawChunk, "no stream_chunk event observed; events: %+v", events)
	require.Equal(t, chatusecase.EventDone, last.Type, "turn must end with a done event; events: %+v", events)
	require.Equal(t, wantReply, last.Content, "done event content must be the assembled reply")
	require.NotEmpty(t, last.MessageID, "done event must carry the persisted assistant message id")
}

// TestLiveRebuildPromptSwapEndToEnd proves design D4 at the application
// layer: management writes swap the registry entry without touching a
// turn already in flight, and the next turn picks the new definition up
// without a restart.
func TestLiveRebuildPromptSwapEndToEnd(t *testing.T) {
	ctx := context.Background()
	principal := domain.Principal{UserID: liveUserID}

	convs := newMemConvStore()
	factory := newLiveFactory(convs)
	registry := agentregistry.New(factory.build)

	admin, err := agentadmin.NewService(newMemAgentStore(), registry, map[string]tool.BaseTool{}, convs)
	require.NoError(t, err)

	// Agent "faq" starts on prompt P1.
	created, err := admin.Create(ctx, liveAgent, agentadmin.Input{
		DisplayName:  "FAQ Agent",
		Description:  "answers questions from the FAQ",
		SystemPrompt: promptOne,
		Enabled:      true,
	})
	require.NoError(t, err)
	require.Equal(t, promptOne, created.SystemPrompt, "created agent must carry the stored prompt")

	s1, ok := registry.Get(liveAgent)
	require.True(t, ok, "created agent must be registered")
	require.NotNil(t, s1)

	// A conversation bound to the agent, as the chat handler resolves it.
	conv := domain.NewConversation(liveUserID, liveAgent)
	require.NoError(t, convs.CreateConversation(ctx, conv))

	// ------------------------------------------------------------------
	// Scenario A: an in-flight turn completes on the OLD definition.
	//
	// The fake model parks its first call, the PATCH (and therefore the
	// registry rebuild) lands while the turn is blocked inside the model,
	// then the turn is released and must have run on prompt P1.
	// ------------------------------------------------------------------
	modelA := factory.model(0)
	require.NotNil(t, modelA, "the create build must have produced a scripted model")
	modelA.holdFirstTurn()

	events1, err := s1.Stream(ctx, principal, conv.ID, liveAgent, "hello")
	require.NoError(t, err)

	// The turn is now in flight inside the model with its context (and
	// therefore its definition) already assembled.
	require.NoError(t, modelA.waitTurnStarted(10*time.Second), "in-flight turn never reached the model")

	// PATCH the system prompt mid-flight: store row replaced, registry
	// entry swapped to a fresh service built from P2.
	updated, err := admin.Update(ctx, liveAgent, agentadmin.Input{
		DisplayName:  "FAQ Agent",
		Description:  "answers questions from the FAQ",
		SystemPrompt: promptTwo,
		Enabled:      true,
	})
	require.NoError(t, err)
	require.Equal(t, promptTwo, updated.SystemPrompt, "update must persist the new prompt")

	s2, ok := registry.Get(liveAgent)
	require.True(t, ok, "updated agent must stay registered")
	require.True(t, s1 != s2, "the registry must hand out a NEW service after update (pointer swap)")

	// Release the parked turn and let it finish.
	modelA.releaseTurn()
	turn1 := drainTurn(t, events1, 10*time.Second)
	requireCoherentTurn(t, turn1, "FAQ Agent", "reply-1")

	// Evidence: the in-flight turn's recorded model input carries P1 and
	// never P2 — it completed on the definition it started with.
	require.Equal(t, 1, modelA.streamCallCount(), "the parked turn must be exactly one model call")
	require.Equal(t, promptOne, modelA.streamSystemPrompt(0),
		"in-flight turn must have received the OLD prompt P1")
	require.NotContains(t, modelA.streamInputText(0), promptTwo,
		"in-flight turn must never see the NEW prompt P2")

	// ------------------------------------------------------------------
	// Scenario B: the NEXT turn uses the NEW prompt without a restart.
	//
	// The chat handler resolves the runner per request, so the second
	// turn goes through registry.Get — which now returns the rebuilt
	// service.
	// ------------------------------------------------------------------
	events2, err := s2.Stream(ctx, principal, conv.ID, liveAgent, "second question")
	require.NoError(t, err)
	turn2 := drainTurn(t, events2, 10*time.Second)
	requireCoherentTurn(t, turn2, "FAQ Agent", "reply-2")

	modelB := factory.model(1)
	require.NotNil(t, modelB, "the update rebuild must have produced a new scripted model")
	require.Equal(t, 1, modelB.streamCallCount(), "the second turn must be exactly one model call")
	require.Equal(t, promptTwo, modelB.streamSystemPrompt(0),
		"next turn after the update must receive the NEW prompt P2")
	require.NotContains(t, modelB.streamInputText(0), promptOne,
		"next turn must never see the OLD prompt P1")

	// ------------------------------------------------------------------
	// Scenario C: disable/enable round trip through the registry.
	//
	// Disabling removes the agent from the running set (the chat handler
	// maps the registry miss to 404); enabling rebuilds from the stored
	// row and the agent serves again — still on the stored (P2) prompt.
	// ------------------------------------------------------------------
	require.NoError(t, admin.SetEnabled(ctx, liveAgent, false))
	_, ok = registry.Get(liveAgent)
	require.False(t, ok, "disabled agent must be absent from the registry")

	require.NoError(t, admin.SetEnabled(ctx, liveAgent, true))
	s3, ok := registry.Get(liveAgent)
	require.True(t, ok, "re-enabled agent must be registered again")
	require.True(t, s2 != s3, "re-enable must rebuild a fresh service, not reuse the old entry")

	events3, err := s3.Stream(ctx, principal, conv.ID, liveAgent, "third question")
	require.NoError(t, err)
	turn3 := drainTurn(t, events3, 10*time.Second)
	requireCoherentTurn(t, turn3, "FAQ Agent", "reply-3")

	modelC := factory.model(2)
	require.NotNil(t, modelC, "the re-enable rebuild must have produced a new scripted model")
	require.Equal(t, 1, modelC.streamCallCount(), "the third turn must be exactly one model call")
	require.Equal(t, promptTwo, modelC.streamSystemPrompt(0),
		"turn after re-enable must serve the stored prompt P2")
}
