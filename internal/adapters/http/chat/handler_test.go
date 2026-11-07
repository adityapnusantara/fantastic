package chat_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/chat"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/middleware"
	chatusecase "github.com/adityapratamanusantara/fantastic/internal/application/usecases/chat"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

type fakeStreamer struct {
	events []chatusecase.Event
	err    error
	// agentIDs records the agent id of each Stream call, in order.
	agentIDs []string
	// conversationIDs records the conversation id of each Stream call.
	conversationIDs []uuid.UUID
	// cancelSeen is closed when the handler's request context is cancelled.
	cancelSeen chan struct{}
	slow       bool
	debugf     func(string, ...any)
}

func (f *fakeStreamer) Stream(ctx context.Context, _ domain.Principal, id uuid.UUID, agentID string, _ string) (<-chan chatusecase.Event, error) {
	if f.debugf != nil {
		f.debugf("stream called agent=%s ctx err=%v", agentID, ctx.Err())
	}
	f.agentIDs = append(f.agentIDs, agentID)
	f.conversationIDs = append(f.conversationIDs, id)
	if f.err != nil {
		return nil, f.err
	}
	if f.cancelSeen != nil {
		go func() {
			<-ctx.Done()
			close(f.cancelSeen)
		}()
	}
	out := make(chan chatusecase.Event, len(f.events))
	for _, ev := range f.events {
		out <- ev
	}
	if f.slow {
		go func() {
			time.Sleep(50 * time.Millisecond)
			out <- chatusecase.Event{Type: chatusecase.EventDone, Content: "late"}
			close(out)
		}()
	} else {
		close(out)
	}
	return out, nil
}

// stubResolver maps agent ids to services the way the real registry will
// (design D4): a lookup miss is an unknown or disabled agent.
type stubResolver struct {
	agents map[string]*fakeStreamer
}

func (r *stubResolver) Resolve(agentID string) (chat.Streamer, bool) {
	svc, ok := r.agents[agentID]
	return svc, ok
}

// stubCreator records the agent id each conversation was created for.
type stubCreator struct {
	agentID string
	calls   int
	// lastID is the id of the most recent conversation minted.
	lastID uuid.UUID
}

func (s *stubCreator) CreateForAgent(_ context.Context, _ domain.Principal, agentID string) (*domain.Conversation, error) {
	s.agentID = agentID
	s.calls++
	s.lastID = uuid.New()
	return &domain.Conversation{ID: s.lastID, UserID: "alice", AgentID: agentID}, nil
}

func principalMW() echo.MiddlewareFunc {
	return middleware.BearerAuth(stubVerifier{}, discardLogger())
}

type stubVerifier struct{}

func (stubVerifier) Verify(context.Context, string) (domain.Principal, error) {
	return domain.Principal{UserID: "alice", Organization: "pribadi"}, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newLogger() *slog.Logger { return discardLogger() }

// newChatApp wires a resolved agent ("general") and optional creator into
// the agent-scoped route, mirroring the bootstrap registration.
func newChatApp(resolver chat.AgentResolver, creator chat.ConversationCreator) *echo.Echo {
	e := echo.New()
	e.POST("/api/v1/agents/:agent/chat", chat.NewHandler(resolver, creator, newLogger()), principalMW())
	return e
}

func postChat(e *echo.Echo, agent, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agent+"/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestSSEStreamsEvents(t *testing.T) {
	svc := &fakeStreamer{events: []chatusecase.Event{
		{Type: chatusecase.EventStreamChunk, Content: "he"},
		{Type: chatusecase.EventStreamChunk, Content: "llo"},
		{Type: chatusecase.EventDone, Content: "hello", MessageID: "m-1"},
	}}
	e := newChatApp(&stubResolver{agents: map[string]*fakeStreamer{"general": svc}}, nil)

	rec := postChat(e, "general", `{"message":"hi"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))

	body := rec.Body.String()
	assert.Contains(t, body, `data: {"type":"stream_chunk","content":"he"}`)
	assert.Contains(t, body, `"type":"done"`)
	assert.Contains(t, body, `"message_id":"m-1"`)

	// one data frame per event, blank-line separated
	assert.Equal(t, 3, strings.Count(body, "data: "))

	// the resolved service ran for the agent named in the path
	assert.Equal(t, []string{"general"}, svc.agentIDs)
}

func TestSSEToolCallsWireShape(t *testing.T) {
	svc := &fakeStreamer{events: []chatusecase.Event{
		{Type: chatusecase.EventMessage, ToolCalls: []chatusecase.ToolCall{{
			ID: "call-1", Name: "demo_tool", Arguments: `{"x":1}`,
		}}},
	}}
	e := newChatApp(&stubResolver{agents: map[string]*fakeStreamer{"general": svc}}, nil)

	var raw map[string]any
	line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(postChat(e, "general", `{"message":"hi"}`).Body.String()), "data: "))
	require.NoError(t, json.Unmarshal([]byte(line), &raw))

	calls := raw["tool_calls"].([]any)
	first := calls[0].(map[string]any)
	assert.Equal(t, "call-1", first["id"])
	assert.Equal(t, "function", first["type"])
	assert.Equal(t, "demo_tool", first["function"].(map[string]any)["name"])
}

func TestSSENotFoundForForeignConversation(t *testing.T) {
	svc := &fakeStreamer{err: domain.ErrNotFound}
	e := newChatApp(&stubResolver{agents: map[string]*fakeStreamer{"general": svc}}, nil)

	rec := postChat(e, "general", `{"conversation_id":"`+uuid.New().String()+`","message":"hi"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUnknownAgentIs404WithoutStartingATurn(t *testing.T) {
	svc := &fakeStreamer{}
	e := newChatApp(&stubResolver{agents: map[string]*fakeStreamer{"general": svc}}, nil)

	rec := postChat(e, "ghost", `{"message":"hi"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "not_found", errCode(t, rec))
	assert.Contains(t, rec.Body.String(), "unknown or disabled agent")
	assert.Empty(t, svc.agentIDs, "no turn starts for an unresolved agent")
}

func TestAgentMismatchIs409(t *testing.T) {
	svc := &fakeStreamer{err: chatusecase.ErrAgentMismatch}
	e := newChatApp(&stubResolver{agents: map[string]*fakeStreamer{"general": svc}}, nil)

	rec := postChat(e, "general", `{"conversation_id":"`+uuid.New().String()+`","message":"hi"}`)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "conflict", errCode(t, rec))
	assert.Contains(t, rec.Body.String(), "conversation belongs to another agent")
}

func TestNewConversationIsCreatedForThePathAgent(t *testing.T) {
	svc := &fakeStreamer{events: []chatusecase.Event{{Type: chatusecase.EventDone, Content: "ok"}}}
	creator := &stubCreator{}
	e := newChatApp(&stubResolver{agents: map[string]*fakeStreamer{"faq-bot": svc}}, creator)

	rec := postChat(e, "faq-bot", `{"message":"hi"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, 1, creator.calls, "absent conversation id creates one row")
	assert.Equal(t, "faq-bot", creator.agentID, "the conversation binds to the agent in the path")
	assert.Equal(t, creator.lastID, svc.conversationIDs[0], "the turn runs on the created conversation")
	assert.Equal(t, creator.lastID.String(), rec.Header().Get("Conversation-Id"))
	assert.Equal(t, []string{"faq-bot"}, svc.agentIDs)
}

func TestSSEHandlerCancelsOnDisconnect(t *testing.T) {
	cancelSeen := make(chan struct{})
	// slow streamer keeps the stream open; recorder write never fails, so
	// simulate the disconnect by cancelling the request context directly
	svc := &fakeStreamer{cancelSeen: cancelSeen, slow: true, debugf: t.Logf}
	e := newChatApp(&stubResolver{agents: map[string]*fakeStreamer{"general": svc}}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/general/chat", strings.NewReader(`{"message":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		e.ServeHTTP(rec, req)
		close(done)
	}()

	cancel() // client goes away mid-turn
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler never returned")
	}
	t.Logf("code=%d body=%q", rec.Code, rec.Body.String())
	select {
	case <-cancelSeen:
	case <-time.After(time.Second):
		t.Fatal("usecase never saw cancellation")
	}
}

// errCode pulls the envelope's code field from a failure response.
func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	return env.Code
}
