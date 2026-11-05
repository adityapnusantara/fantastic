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
	// cancelSeen is closed when the handler's request context is cancelled.
	cancelSeen chan struct{}
	slow       bool
	debugf     func(string, ...any)
}

func (f *fakeStreamer) Stream(ctx context.Context, _ domain.Principal, id uuid.UUID, _ string) (<-chan chatusecase.Event, error) {
	if f.debugf != nil {
		f.debugf("stream called, ctx err=%v", ctx.Err())
	}
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

func postChat(e *echo.Echo, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestSSEStreamsEvents(t *testing.T) {
	e := echo.New()
	svc := &fakeStreamer{events: []chatusecase.Event{
		{Type: chatusecase.EventStreamChunk, Content: "he"},
		{Type: chatusecase.EventStreamChunk, Content: "llo"},
		{Type: chatusecase.EventDone, Content: "hello", MessageID: "m-1"},
	}}
	e.POST("/api/v1/chat", chat.NewHandler(svc, nil, newLogger()), principalMW())

	rec := postChat(e, `{"message":"hi"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))

	body := rec.Body.String()
	assert.Contains(t, body, `data: {"type":"stream_chunk","content":"he"}`)
	assert.Contains(t, body, `"type":"done"`)
	assert.Contains(t, body, `"message_id":"m-1"`)

	// one data frame per event, blank-line separated
	assert.Equal(t, 3, strings.Count(body, "data: "))
}

func TestSSEToolCallsWireShape(t *testing.T) {
	e := echo.New()
	svc := &fakeStreamer{events: []chatusecase.Event{
		{Type: chatusecase.EventMessage, ToolCalls: []chatusecase.ToolCall{{
			ID: "call-1", Name: "demo_tool", Arguments: `{"x":1}`,
		}}},
	}}
	e.POST("/api/v1/chat", chat.NewHandler(svc, nil, newLogger()), principalMW())

	var raw map[string]any
	line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(postChat(e, `{"message":"hi"}`).Body.String()), "data: "))
	require.NoError(t, json.Unmarshal([]byte(line), &raw))

	calls := raw["tool_calls"].([]any)
	first := calls[0].(map[string]any)
	assert.Equal(t, "call-1", first["id"])
	assert.Equal(t, "function", first["type"])
	assert.Equal(t, "demo_tool", first["function"].(map[string]any)["name"])
}

func TestSSENotFoundForForeignConversation(t *testing.T) {
	e := echo.New()
	svc := &fakeStreamer{err: domain.ErrNotFound}
	e.POST("/api/v1/chat", chat.NewHandler(svc, nil, newLogger()), principalMW())

	rec := postChat(e, `{"conversation_id":"`+uuid.New().String()+`","message":"hi"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSSEHandlerCancelsOnDisconnect(t *testing.T) {
	e := echo.New()
	cancelSeen := make(chan struct{})
	_ = cancelSeen
	// slow streamer keeps the stream open; recorder write never fails, so
	// simulate the disconnect by cancelling the request context directly
	svc := &fakeStreamer{cancelSeen: cancelSeen, slow: true, debugf: t.Logf}
	e.POST("/api/v1/chat", chat.NewHandler(svc, nil, newLogger()), principalMW())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{"message":"hi"}`))
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
