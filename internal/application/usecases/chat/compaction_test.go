package chat_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/conversation/mocks"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// textOf extracts the text carried by an agentic message: assistant
// generated text for assistant messages, user-input text for the system
// and user messages the ADK instruction and buildInput assemble.
func textOf(m *schema.AgenticMessage) string {
	var b strings.Builder
	for _, block := range m.ContentBlocks {
		if block == nil {
			continue
		}
		switch block.Type {
		case schema.ContentBlockTypeAssistantGenText:
			if block.AssistantGenText != nil {
				b.WriteString(block.AssistantGenText.Text)
			}
		case schema.ContentBlockTypeUserInputText:
			if block.UserInputText != nil {
				b.WriteString(block.UserInputText.Text)
			}
		}
	}
	return b.String()
}

// waitFor polls cond until true or timeout.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestCompactionFoldsOverflowIntoSummary(t *testing.T) {
	conv := &domain.Conversation{ID: convID(), UserID: "alice", Title: "t"} // SummaryUntilSeq = 0
	recent := []domain.Message{
		{Seq: 4, Role: domain.RoleUser, Content: "recent q"},
		{Seq: 5, Role: domain.RoleAssistant, Content: "recent a"},
		{Seq: 6, Role: domain.RoleUser, Content: "another q"},
	}
	older := []domain.Message{
		{Seq: 1, Role: domain.RoleUser, Content: "old q1"},
		{Seq: 2, Role: domain.RoleAssistant, Content: "old a1 [called tool demo_tool({})]"},
		{Seq: 3, Role: domain.RoleUser, Content: "old q2"},
	}

	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return conv, recent, nil
		},
		GetMessagesFn: func(context.Context, uuid.UUID, string) ([]domain.Message, error) {
			return append(append([]domain.Message{}, older...), recent...), nil
		},
	}
	mdl := &fakeChatModel{
		scripts:        []fakeScript{{chunks: textChunks("ok")}},
		generateScript: "the conversation was about old questions",
	}
	svc := newService(t, mock, mdl, nil)

	var savedSummary string
	var savedUntil int64
	mock.SaveSummaryFn = func(_ context.Context, _ uuid.UUID, _ string, summary string, untilSeq int64) error {
		savedSummary, savedUntil = summary, untilSeq
		return nil
	}

	drain(t, mustStream(t, svc, conv.ID, "hi"))

	waitFor(t, "summary save", func() bool { return savedSummary != "" })
	assert.Equal(t, "the conversation was about old questions", savedSummary)
	assert.Equal(t, int64(3), savedUntil, "coverage marker advances to just before the window")

	require.Equal(t, 1, mdl.generateCalls, "summarizer runs once, only on overflow")
	assert.Contains(t, mock.Calls, "GetMessages")
}

func TestNoOverflowSkipsSummarizer(t *testing.T) {
	conv := &domain.Conversation{ID: convID(), UserID: "alice", Title: "t"}
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return conv, nil, nil // window has everything; nothing uncovered
		},
	}
	mdl := &fakeChatModel{scripts: []fakeScript{{chunks: textChunks("ok")}}}
	svc := newService(t, mock, mdl, nil)

	drain(t, mustStream(t, svc, conv.ID, "hi"))

	time.Sleep(100 * time.Millisecond) // let any (wrong) async compaction run
	assert.Zero(t, mdl.generateCalls, "no summarizer call without overflow")
	assert.NotContains(t, mock.Calls, "SaveSummary")
}

func TestWindowContentIsSummaryPlusRecentMessages(t *testing.T) {
	conv := &domain.Conversation{
		ID:             convID(),
		UserID:         "alice",
		Title:          "t",
		RollingSummary: "EARLIER STUFF",
	}
	recent := []domain.Message{
		{Seq: 9, Role: domain.RoleUser, Content: "q9"},
		{Seq: 10, Role: domain.RoleAssistant, Content: "a10"},
	}
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return conv, recent, nil
		},
	}
	mdl := &fakeChatModel{scripts: []fakeScript{{chunks: textChunks("ok")}}}
	svc := newService(t, mock, mdl, nil)

	drain(t, mustStream(t, svc, conv.ID, "q11"))

	waitFor(t, "model call", func() bool { return mdl.calls > 0 })
	// the fake's Stream received the context; inspect via a recording hook
	inputs := mdl.lastStreamInput()
	require.Len(t, inputs, 5, "agent instruction + summary system + 2 windowed messages + new user message")
	assert.Equal(t, "system", string(inputs[0].Role))
	assert.Equal(t, "you are helpful", textOf(inputs[0]), "agent instruction comes first")
	assert.Equal(t, "system", string(inputs[1].Role))
	assert.Contains(t, textOf(inputs[1]), "EARLIER STUFF", "rolling summary follows")
	assert.Equal(t, "q9", textOf(inputs[2]))
	assert.Equal(t, "a10", textOf(inputs[3]))
	assert.Equal(t, "q11", textOf(inputs[4]))
}

// TestOrphanToolResultAfterCompactionIsDropped is a live regression from
// task 9.1: after overflow the window can hold a tool result (seq N) whose
// assistant tool-call partner (seq N-1) was folded into the rolling
// summary. Sending that orphan function output is rejected by the provider
// with 400 "No tool call found for function call output" — buildInput must
// drop the orphaned tool row instead.
func TestOrphanToolResultAfterCompactionIsDropped(t *testing.T) {
	conv := &domain.Conversation{
		ID:              convID(),
		UserID:          "alice",
		Title:           "t",
		RollingSummary:  "user asked to check connected accounts; assistant called list_accounts",
		SummaryUntilSeq: 2, // seq 1-2 (user + assistant tool call) are folded
	}
	recent := []domain.Message{
		{Seq: 3, Role: domain.RoleTool, ToolCallID: "call_X", Content: `{"ok":true}`},
		{Seq: 4, Role: domain.RoleUser, Content: "q4"},
	}
	mock := &mocks.MockConversationStore{
		LoadWindowFn: func(context.Context, uuid.UUID, string, int) (*domain.Conversation, []domain.Message, error) {
			return conv, recent, nil
		},
	}
	mdl := &fakeChatModel{scripts: []fakeScript{{chunks: textChunks("ok")}}}
	svc := newService(t, mock, mdl, nil)

	drain(t, mustStream(t, svc, conv.ID, "q5"))

	waitFor(t, "model call", func() bool { return mdl.calls > 0 })
	inputs := mdl.lastStreamInput()
	require.Len(t, inputs, 4, "agent instruction + summary system + q4 + new user message (orphan tool row dropped)")
	for i, m := range inputs {
		for _, b := range m.ContentBlocks {
			if b == nil {
				continue
			}
			assert.NotEqual(t, schema.ContentBlockTypeFunctionToolResult, b.Type,
				"input %d carries an orphan function output", i)
			assert.NotEqual(t, schema.ContentBlockTypeFunctionToolCall, b.Type,
				"input %d carries an unmatched function call", i)
		}
	}
}
