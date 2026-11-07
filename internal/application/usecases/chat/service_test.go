package chat_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/chat"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/conversation/mocks"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

var alicePrincipal = domain.Principal{UserID: "alice", Organization: "pribadi"}

func convID() uuid.UUID { return uuid.New() }

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newService(t *testing.T, m *mocks.MockConversationStore, mdl *fakeChatModel, tools []tool.BaseTool) *chat.Service {
	t.Helper()
	svc, err := chat.NewService(m, mdl, tools, chat.Config{
		SystemPrompt:  "you are helpful",
		HistoryWindow: 20,
		SummaryPrefix: "SUMMARY:\n",
	}, newLogger())
	require.NoError(t, err)
	return svc
}

// mustStream starts a turn for a conversation whose fixtures leave
// AgentID unset: the empty agent id matches that binding.
func mustStream(t *testing.T, svc *chat.Service, id uuid.UUID, text string) <-chan chat.Event {
	t.Helper()
	ch, err := svc.Stream(context.Background(), alicePrincipal, id, "", text)
	require.NoError(t, err)
	return ch
}

func drain(t *testing.T, ch <-chan chat.Event) []chat.Event {
	t.Helper()
	var out []chat.Event
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
			if ev.Type == chat.EventDone || ev.Type == chat.EventError {
				return out
			}
		case <-timeout:
			t.Fatal("timed out waiting for turn events")
		}
	}
}

func eventsTypes(events []chat.Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return -1
}

func TestNormalStreamingTurn(t *testing.T) {
	conv := &domain.Conversation{ID: convID(), UserID: "alice", Title: "existing title"}
	// Two window loads are guaranteed: Stream's own and the detached
	// compaction pass's trailing one; awaiting both joins that goroutine
	// before mock.Calls is read below.
	windowLoads := make(chan struct{}, 2)
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(_ context.Context, id uuid.UUID, userID string, _ int) (*domain.Conversation, []domain.Message, error) {
			assert.Equal(t, "alice", userID, "window loads are user-scoped")
			tick(windowLoads)
			return conv, nil, nil
		},
	}
	mdl := &fakeChatModel{scripts: []fakeScript{{chunks: textChunks("hello there")}}}
	svc := newService(t, mock, mdl, nil)

	events := drain(t, mustStream(t, svc, conv.ID, "hi"))

	awaitTokens(t, "compaction window load", windowLoads, 2)

	types := eventsTypes(events)
	assert.Contains(t, types, chat.EventStreamChunk)
	assert.Contains(t, types, chat.EventDone)

	// done carries the assembled content and the persisted message id
	var done chat.Event
	for _, ev := range events {
		if ev.Type == chat.EventDone {
			done = ev
		}
	}
	assert.Equal(t, "hello there", done.Content)
	assert.NotEmpty(t, done.MessageID)

	// durability: user message persisted BEFORE the model output landed.
	appendMsgIdx := indexOf(mock.Calls, "AppendMessage")
	appendMsgsIdx := indexOf(mock.Calls, "AppendMessages")
	require.GreaterOrEqual(t, appendMsgIdx, 0, "user message must be persisted")
	require.Greater(t, appendMsgsIdx, appendMsgIdx, "assistant output persisted after user message")

	// assembled assistant output contains the concatenated content
	require.Len(t, mock.AppendMessagesCalledWith, 1)
	assembled := mock.AppendMessagesCalledWith[0].Msgs
	require.Len(t, assembled, 1)
	assert.Equal(t, domain.RoleAssistant, assembled[0].Role)
	assert.Equal(t, "hello there", assembled[0].Content)
}

func TestFirstMessageSetsTitle(t *testing.T) {
	conv := &domain.Conversation{ID: convID(), UserID: "alice"}
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return conv, nil, nil
		},
	}
	mdl := &fakeChatModel{scripts: []fakeScript{{chunks: textChunks("ok")}}}
	svc := newService(t, mock, mdl, nil)

	drain(t, mustStream(t, svc, conv.ID, "what is a rolling summary?"))

	require.Len(t, mock.AppendMessageCalledWith, 1)
	title := mock.AppendMessageCalledWith[0].Title
	require.NotNil(t, title, "first user message sets the title")
	assert.Equal(t, "what is a rolling summary?", *title)
}

func TestForeignConversationIsNotFound(t *testing.T) {
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return nil, nil, domain.ErrNotFound
		},
	}
	svc := newService(t, mock, &fakeChatModel{}, nil)

	_, err := svc.Stream(context.Background(), alicePrincipal, convID(), "general", "hi")
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestAgentMismatchIsRefusedBeforeAnythingIsPersisted(t *testing.T) {
	conv := &domain.Conversation{ID: convID(), UserID: "alice", Title: "t", AgentID: "general"}
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return conv, nil, nil
		},
	}
	mdl := &fakeChatModel{scripts: []fakeScript{{chunks: textChunks("must never run")}}}
	svc := newService(t, mock, mdl, nil)

	_, err := svc.Stream(context.Background(), alicePrincipal, conv.ID, "faq-bot", "hi")

	assert.ErrorIs(t, err, chat.ErrAgentMismatch)
	assert.Equal(t, []string{"LoadWindow"}, mock.Calls, "mismatch refuses the turn with no user message persisted")
}

func TestMatchingAgentPersistsUserMessageAndStreams(t *testing.T) {
	conv := &domain.Conversation{ID: convID(), UserID: "alice", Title: "t", AgentID: "general"}
	// Join the detached compaction goroutine (its trailing LoadWindow)
	// before reading mock.Calls below; see TestNormalStreamingTurn.
	windowLoads := make(chan struct{}, 2)
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			tick(windowLoads)
			return conv, nil, nil
		},
	}
	mdl := &fakeChatModel{scripts: []fakeScript{{chunks: textChunks("hello")}}}
	svc := newService(t, mock, mdl, nil)

	ch, err := svc.Stream(context.Background(), alicePrincipal, conv.ID, "general", "hi")
	require.NoError(t, err)

	events := drain(t, ch)
	assert.Contains(t, eventsTypes(events), chat.EventDone, "a matching agent binding turns normally")
	awaitTokens(t, "compaction window load", windowLoads, 2)
	require.Len(t, mock.AppendMessageCalledWith, 1, "happy path still persists the user message first")
	// user message lands before any assembled output (the trailing
	// LoadWindow is post-turn compaction)
	appendMsgIdx := indexOf(mock.Calls, "AppendMessage")
	appendMsgsIdx := indexOf(mock.Calls, "AppendMessages")
	require.GreaterOrEqual(t, appendMsgIdx, 0)
	require.Greater(t, appendMsgsIdx, appendMsgIdx, "assistant output persisted after user message")
}

func TestModelFailureKeepsUserMessage(t *testing.T) {
	conv := &domain.Conversation{ID: convID(), UserID: "alice", Title: "t"}
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return conv, nil, nil
		},
	}
	mdl := &fakeChatModel{scripts: []fakeScript{{err: errors.New("provider down")}}}
	svc := newService(t, mock, mdl, nil)

	events := drain(t, mustStream(t, svc, conv.ID, "question"))

	assert.Equal(t, []string{chat.EventError}, eventsTypes(events))
	assert.Equal(t, []string{"LoadWindow", "AppendMessage"}, mock.Calls, "window loaded, user message persisted, no output lands")
}

func TestPersistFailureFailsTheTurn(t *testing.T) {
	conv := &domain.Conversation{ID: convID(), UserID: "alice", Title: "t"}
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return conv, nil, nil
		},
		AppendMessagesFn: func(context.Context, uuid.UUID, string, []*domain.Message, *string) ([]int64, error) {
			return nil, errors.New("db write failed")
		},
	}
	mdl := &fakeChatModel{scripts: []fakeScript{{chunks: textChunks("unstored answer")}}}
	svc := newService(t, mock, mdl, nil)

	events := drain(t, mustStream(t, svc, conv.ID, "hi"))

	types := eventsTypes(events)
	assert.Equal(t, chat.EventError, types[len(types)-1], "turn ends with an error event")
	assert.NotContains(t, types, chat.EventDone, "a turn whose output is not stored must not complete")
}

func TestToolUsingTurn(t *testing.T) {
	conv := &domain.Conversation{ID: convID(), UserID: "alice", Title: "t"}
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return conv, nil, nil
		},
	}
	mdl := &fakeChatModel{scripts: []fakeScript{
		{chunks: toolCallChunks("call-1", "demo_tool", `{"x":1}`)},
		{chunks: textChunks("tool says ok")},
	}}
	svc := newService(t, mock, mdl, []tool.BaseTool{fakeTool{}})

	events := drain(t, mustStream(t, svc, conv.ID, "use the tool"))

	types := eventsTypes(events)
	assert.Contains(t, types, chat.EventMessage, "assistant tool-call message is announced")
	assert.Contains(t, types, chat.EventToolResult, "tool execution result is announced")
	assert.Contains(t, types, chat.EventDone)

	require.Len(t, mock.AppendMessagesCalledWith, 1)
	assembled := mock.AppendMessagesCalledWith[0].Msgs
	require.Len(t, assembled, 3, "assistant(toolcall) + tool result + final assistant")
	assert.Equal(t, domain.RoleAssistant, assembled[0].Role)
	require.Len(t, assembled[0].ToolCalls, 1)
	assert.Equal(t, "demo_tool", assembled[0].ToolCalls[0].Name)
	assert.Equal(t, domain.RoleTool, assembled[1].Role)
	assert.Equal(t, "call-1", assembled[1].ToolCallID)
	assert.Equal(t, domain.RoleAssistant, assembled[2].Role)
	assert.Equal(t, "tool says ok", assembled[2].Content)
}

func TestDisconnectPersistsPartialOutput(t *testing.T) {
	conv := &domain.Conversation{ID: convID(), UserID: "alice", Title: "t"}
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return conv, nil, nil
		},
	}
	hold := make(chan struct{})
	mdl := &fakeChatModel{scripts: []fakeScript{{
		chunks:    textChunks("partial out"), // several chunks; the last is held
		holdUntil: hold,
	}}}
	svc := newService(t, mock, mdl, nil)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := svc.Stream(ctx, alicePrincipal, conv.ID, "", "hi") // "" matches the fixture's unset AgentID
	require.NoError(t, err)

	// let the first chunks arrive, then simulate the client disconnecting
waitLoop:
	for got := 0; got < 2; {
		select {
		case _, ok := <-ch:
			if !ok {
				break waitLoop
			}
			got++
		case <-time.After(5 * time.Second):
			t.Fatal("no chunks arrived")
		}
	}
	cancel()

	// the turn ends (channel closes) and partial output is persisted
	var persisted bool
	deadline := time.After(5 * time.Second)
	for !persisted {
		select {
		case _, ok := <-ch:
			if !ok {
				persisted = true
			}
		case <-deadline:
			t.Fatal("turn did not end after disconnect")
		}
	}

	require.Len(t, mock.AppendMessagesCalledWith, 1)
	assembled := mock.AppendMessagesCalledWith[0].Msgs
	require.NotEmpty(t, assembled)
	assert.Contains(t, assembled[len(assembled)-1].Content, "partial", "partial output survives the disconnect")
}

func TestDefaultAgentNameFallsBackToFantastic(t *testing.T) {
	conv := &domain.Conversation{ID: convID(), UserID: "alice", Title: "t"}
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return conv, nil, nil
		},
	}
	// newService builds Config without AgentName/AgentDescription, so the
	// whole suite exercises the fallback path; this test asserts it.
	mdl := &fakeChatModel{scripts: []fakeScript{{chunks: textChunks("hi there")}}}
	svc := newService(t, mock, mdl, nil)

	events := drain(t, mustStream(t, svc, conv.ID, "hi"))

	assert.Contains(t, eventsTypes(events), chat.EventStreamChunk, "fallback turn still streams")
	for _, ev := range events {
		if ev.Type == chat.EventStreamChunk {
			assert.Equal(t, "fantastic", ev.AgentName, "unnamed Config falls back to the built-in agent name")
		}
	}
}

func TestCustomAgentNameSurfacesInConstructedAgentAndEvents(t *testing.T) {
	conv := &domain.Conversation{ID: convID(), UserID: "alice", Title: "t"}
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return conv, nil, nil
		},
	}
	mdl := &fakeChatModel{scripts: []fakeScript{
		{chunks: toolCallChunks("call-1", "demo_tool", `{"x":1}`)},
		{chunks: textChunks("tool says ok")},
	}}
	svc, err := chat.NewService(mock, mdl, []tool.BaseTool{fakeTool{}}, chat.Config{
		AgentName:        "faq-bot",
		AgentDescription: "answers frequently asked questions",
		SystemPrompt:     "you are helpful",
		HistoryWindow:    20,
	}, newLogger())
	require.NoError(t, err)

	events := drain(t, mustStream(t, svc, conv.ID, "use the tool"))

	types := eventsTypes(events)
	assert.Contains(t, types, chat.EventDone)

	// The ADK stamps the constructed agent's Name onto every event it
	// emits, so a matching AgentName here proves the configured name
	// reached the agent construction (chatmodel.go: event.AgentName =
	// a.name) and flows through the streamed message/tool events.
	sawChunk, sawMessage, sawToolResult := false, false, false
	for _, ev := range events {
		switch ev.Type {
		case chat.EventStreamChunk:
			sawChunk = true
			assert.Equal(t, "faq-bot", ev.AgentName, "stream_chunk carries the configured agent name")
		case chat.EventMessage:
			sawMessage = true
			assert.Equal(t, "faq-bot", ev.AgentName, "message event carries the configured agent name")
		case chat.EventToolResult:
			sawToolResult = true
			assert.Equal(t, "faq-bot", ev.AgentName, "tool_result carries the configured agent name")
		}
	}
	assert.True(t, sawChunk, "turn streamed text deltas")
	assert.True(t, sawMessage, "assistant tool-call message was announced")
	assert.True(t, sawToolResult, "tool execution result was announced")
}
