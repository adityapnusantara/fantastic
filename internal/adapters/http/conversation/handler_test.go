package conversation_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/conversation"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/middleware"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

type stubService struct {
	convs     []domain.Conversation
	msgs      []domain.Message
	getErr    error
	listAgent *string
}

func (s *stubService) List(_ context.Context, _ domain.Principal, agentID *string) ([]domain.Conversation, error) {
	s.listAgent = agentID
	return s.convs, nil
}

func (s *stubService) Get(_ context.Context, _ domain.Principal, id uuid.UUID) (*domain.Conversation, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	for i := range s.convs {
		if s.convs[i].ID == id {
			return &s.convs[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func (s *stubService) Messages(_ context.Context, _ domain.Principal, _ uuid.UUID) ([]domain.Message, error) {
	return s.msgs, nil
}

func (s *stubService) Delete(context.Context, domain.Principal, uuid.UUID) error { return nil }

func (s *stubService) Create(_ context.Context, _ domain.Principal) (*domain.Conversation, error) {
	return &domain.Conversation{ID: uuid.New(), UserID: "alice", AgentID: "general"}, nil
}

func newApp(svc conversation.Service) *echo.Echo {
	e := echo.New()
	g := e.Group("/api/v1", middleware.BearerAuth(stubVerifier{}, discardLogger()))
	conversation.Register(g, svc)
	return e
}

type stubVerifier struct{}

func (stubVerifier) Verify(context.Context, string) (domain.Principal, error) {
	return domain.Principal{UserID: "alice", Organization: "pribadi"}, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func do(e *echo.Echo, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestListNewestFirst(t *testing.T) {
	old := domain.Conversation{ID: uuid.New(), Title: "old", UpdatedAt: time.Now().Add(-time.Hour)}
	recent := domain.Conversation{ID: uuid.New(), Title: "recent", UpdatedAt: time.Now()}
	e := newApp(&stubService{convs: []domain.Conversation{recent, old}})

	rec := do(e, http.MethodGet, "/api/v1/conversations")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Code string `json:"code"`
		Data []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 2)
	assert.Equal(t, "recent", body.Data[0].Title, "most recent activity first")
	assert.Equal(t, "00", body.Code)
}

func TestListForwardsAgentFilter(t *testing.T) {
	e := newApp(&stubService{convs: []domain.Conversation{
		{ID: uuid.New(), UserID: "alice", AgentID: "faq", Title: "faq chat", UpdatedAt: time.Now()},
	}})

	rec := do(e, http.MethodGet, "/api/v1/conversations?agent_id=faq")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Code string `json:"code"`
		Data []struct {
			ID      string `json:"id"`
			AgentID string `json:"agent_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	assert.Equal(t, "faq", body.Data[0].AgentID, "the wire form exposes the binding")
}

func TestListAgentFilterReachesService(t *testing.T) {
	stub := &stubService{}
	e := newApp(stub)

	do(e, http.MethodGet, "/api/v1/conversations?agent_id=social")
	require.NotNil(t, stub.listAgent)
	assert.Equal(t, "social", *stub.listAgent)

	do(e, http.MethodGet, "/api/v1/conversations")
	assert.Nil(t, stub.listAgent, "no query param means no filter")
}

func TestMessagesIncludeToolCalls(t *testing.T) {
	id := uuid.New()
	e := newApp(&stubService{msgs: []domain.Message{
		{ID: uuid.New(), Seq: 1, Role: domain.RoleUser, Content: "q", CreatedAt: time.Now()},
		{ID: uuid.New(), Seq: 2, Role: domain.RoleAssistant, Content: "", CreatedAt: time.Now(),
			ToolCalls: []domain.ToolCall{{ID: "c1", Name: "demo_tool", Arguments: `{"x":1}`}}},
		{ID: uuid.New(), Seq: 3, Role: domain.RoleTool, Content: "result", ToolCallID: "c1", CreatedAt: time.Now()},
	}})

	rec := do(e, http.MethodGet, "/api/v1/conversations/"+id.String()+"/messages")
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data []struct {
			Role      string `json:"role"`
			ToolCall  string `json:"tool_call_id"`
			ToolCalls []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"tool_calls"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 3)
	assert.Equal(t, "assistant", body.Data[1].Role)
	require.Len(t, body.Data[1].ToolCalls, 1)
	assert.Equal(t, "demo_tool", body.Data[1].ToolCalls[0].Name)
	assert.Equal(t, "c1", body.Data[2].ToolCall, "tool result links to its call")
}

func TestForeignConversationIs404(t *testing.T) {
	e := newApp(&stubService{getErr: domain.ErrNotFound})
	rec := do(e, http.MethodGet, "/api/v1/conversations/"+uuid.New().String())
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeleteReturns204(t *testing.T) {
	e := newApp(&stubService{})
	rec := do(e, http.MethodDelete, "/api/v1/conversations/"+uuid.New().String())
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestInvalidIDIs400(t *testing.T) {
	e := newApp(&stubService{})
	rec := do(e, http.MethodGet, "/api/v1/conversations/not-a-uuid")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestNoAuthIs401(t *testing.T) {
	e := echo.New()
	g := e.Group("/api/v1", middleware.BearerAuth(stubVerifier{}, discardLogger()))
	conversation.Register(g, &stubService{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
