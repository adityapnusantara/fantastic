// Package mocks provides a hand-written mock of the driven
// ConversationStore port for usecase-level tests.
package mocks

import (
	"context"

	"github.com/google/uuid"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// MockConversationStore records configured return values; each method
// may be overridden per test. Nil overrides fall back to zero values.
type MockConversationStore struct {
	CreateConversationFn func(ctx context.Context, conv *domain.Conversation) error
	GetConversationFn    func(ctx context.Context, conversationID uuid.UUID, userID string) (*domain.Conversation, error)
	ListConversationsFn  func(ctx context.Context, userID string, agentID *string) ([]domain.Conversation, error)
	DeleteConversationFn func(ctx context.Context, conversationID uuid.UUID, userID string) error
	CountByAgentFn       func(ctx context.Context, agentID string) (int64, error)
	AppendMessageFn      func(ctx context.Context, conversationID uuid.UUID, userID string, msg *domain.Message, title *string) (int64, error)
	AppendMessagesFn     func(ctx context.Context, conversationID uuid.UUID, userID string, msgs []*domain.Message, title *string) ([]int64, error)
	GetMessagesFn        func(ctx context.Context, conversationID uuid.UUID, userID string) ([]domain.Message, error)
	LoadWindowFn         func(ctx context.Context, conversationID uuid.UUID, userID string, limit int) (*domain.Conversation, []domain.Message, error)
	SaveSummaryFn        func(ctx context.Context, conversationID uuid.UUID, userID, summary string, untilSeq int64) error

	// Calls records invocations for assertions.
	Calls []string

	// AppendMessageCalledWith / AppendMessagesCalledWith record arguments
	// of append calls for assertions.
	AppendMessageCalledWith  []AppendMessageCall
	AppendMessagesCalledWith []AppendMessagesCall
}

// AppendMessageCall captures one AppendMessage invocation.
type AppendMessageCall struct {
	ConversationID uuid.UUID
	UserID         string
	Msg            *domain.Message
	Title          *string
}

// AppendMessagesCall captures one AppendMessages invocation.
type AppendMessagesCall struct {
	ConversationID uuid.UUID
	UserID         string
	Msgs           []*domain.Message
	Title          *string
}

func (m *MockConversationStore) record(method string) {
	m.Calls = append(m.Calls, method)
}

func (m *MockConversationStore) CreateConversation(ctx context.Context, conv *domain.Conversation) error {
	m.record("CreateConversation")
	if m.CreateConversationFn != nil {
		return m.CreateConversationFn(ctx, conv)
	}
	return nil
}

func (m *MockConversationStore) GetConversation(ctx context.Context, conversationID uuid.UUID, userID string) (*domain.Conversation, error) {
	m.record("GetConversation")
	if m.GetConversationFn != nil {
		return m.GetConversationFn(ctx, conversationID, userID)
	}
	return &domain.Conversation{ID: conversationID, UserID: userID}, nil
}

func (m *MockConversationStore) ListConversations(ctx context.Context, userID string, agentID *string) ([]domain.Conversation, error) {
	m.record("ListConversations")
	if m.ListConversationsFn != nil {
		return m.ListConversationsFn(ctx, userID, agentID)
	}
	return nil, nil
}

func (m *MockConversationStore) DeleteConversation(ctx context.Context, conversationID uuid.UUID, userID string) error {
	m.record("DeleteConversation")
	if m.DeleteConversationFn != nil {
		return m.DeleteConversationFn(ctx, conversationID, userID)
	}
	return nil
}

func (m *MockConversationStore) CountByAgent(ctx context.Context, agentID string) (int64, error) {
	m.record("CountByAgent")
	if m.CountByAgentFn != nil {
		return m.CountByAgentFn(ctx, agentID)
	}
	return 0, nil
}

func (m *MockConversationStore) AppendMessage(ctx context.Context, conversationID uuid.UUID, userID string, msg *domain.Message, title *string) (int64, error) {
	m.record("AppendMessage")
	m.AppendMessageCalledWith = append(m.AppendMessageCalledWith, AppendMessageCall{
		ConversationID: conversationID, UserID: userID, Msg: msg, Title: title,
	})
	if m.AppendMessageFn != nil {
		return m.AppendMessageFn(ctx, conversationID, userID, msg, title)
	}
	return 1, nil
}

func (m *MockConversationStore) AppendMessages(ctx context.Context, conversationID uuid.UUID, userID string, msgs []*domain.Message, title *string) ([]int64, error) {
	m.record("AppendMessages")
	m.AppendMessagesCalledWith = append(m.AppendMessagesCalledWith, AppendMessagesCall{
		ConversationID: conversationID, UserID: userID, Msgs: msgs, Title: title,
	})
	if m.AppendMessagesFn != nil {
		return m.AppendMessagesFn(ctx, conversationID, userID, msgs, title)
	}
	seqs := make([]int64, len(msgs))
	for i := range msgs {
		seqs[i] = int64(i + 1)
	}
	return seqs, nil
}

func (m *MockConversationStore) GetMessages(ctx context.Context, conversationID uuid.UUID, userID string) ([]domain.Message, error) {
	m.record("GetMessages")
	if m.GetMessagesFn != nil {
		return m.GetMessagesFn(ctx, conversationID, userID)
	}
	return nil, nil
}

func (m *MockConversationStore) LoadWindow(ctx context.Context, conversationID uuid.UUID, userID string, limit int) (*domain.Conversation, []domain.Message, error) {
	m.record("LoadWindow")
	if m.LoadWindowFn != nil {
		return m.LoadWindowFn(ctx, conversationID, userID, limit)
	}
	return &domain.Conversation{ID: conversationID, UserID: userID}, nil, nil
}

func (m *MockConversationStore) SaveSummary(ctx context.Context, conversationID uuid.UUID, userID, summary string, untilSeq int64) error {
	m.record("SaveSummary")
	if m.SaveSummaryFn != nil {
		return m.SaveSummaryFn(ctx, conversationID, userID, summary, untilSeq)
	}
	return nil
}

// compile-time port conformance.
var _ driven.ConversationStore = (*MockConversationStore)(nil)
