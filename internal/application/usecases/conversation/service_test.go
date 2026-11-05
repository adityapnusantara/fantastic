package conversation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/conversation"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/conversation/mocks"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

var alice = domain.Principal{UserID: "alice", Organization: "pribadi"}

func TestListPassesCallerIdentity(t *testing.T) {
	mock := &mocks.MockConversationStore{
		ListConversationsFn: func(_ context.Context, userID string) ([]domain.Conversation, error) {
			assert.Equal(t, "alice", userID, "store must be queried with the verified identity")
			return []domain.Conversation{{ID: uuid.New(), UserID: userID, Title: "only mine"}}, nil
		},
	}

	svc := conversation.NewService(mock)
	got, err := svc.List(context.Background(), alice)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "alice", got[0].UserID, "results are scoped to the caller")
}

func TestMessagesRejectsForeignConversation(t *testing.T) {
	target := uuid.New()
	mock := &mocks.MockConversationStore{
		GetConversationFn: func(_ context.Context, _ uuid.UUID, userID string) (*domain.Conversation, error) {
			assert.Equal(t, "alice", userID)
			return nil, domain.ErrNotFound // foreign conversation looks missing
		},
	}

	svc := conversation.NewService(mock)
	_, err := svc.Messages(context.Background(), alice, target)
	assert.ErrorIs(t, err, domain.ErrNotFound, "foreign conversation maps to not-found, not error leak")

	calls := 0
	mock.GetMessagesFn = func(context.Context, uuid.UUID, string) ([]domain.Message, error) {
		calls++
		return nil, nil
	}
	_, err = svc.Messages(context.Background(), alice, target)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.Zero(t, calls, "messages are not fetched when ownership check fails")
}

func TestMessagesReturnsHistoryForOwner(t *testing.T) {
	target := uuid.New()
	mock := &mocks.MockConversationStore{
		GetMessagesFn: func(_ context.Context, conversationID uuid.UUID, userID string) ([]domain.Message, error) {
			assert.Equal(t, target, conversationID)
			assert.Equal(t, "alice", userID)
			return []domain.Message{
				{Seq: 1, Role: domain.RoleUser, Content: "q"},
				{Seq: 2, Role: domain.RoleAssistant, Content: "a", ToolCalls: []domain.ToolCall{{ID: "c1", Name: "t"}}},
				{Seq: 3, Role: domain.RoleTool, Content: "result", ToolCallID: "c1"},
			}, nil
		},
	}

	svc := conversation.NewService(mock)
	msgs, err := svc.Messages(context.Background(), alice, target)
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.Equal(t, "c1", msgs[2].ToolCallID)
}

func TestDeleteScopesToCaller(t *testing.T) {
	target := uuid.New()
	var calledWith string
	mock := &mocks.MockConversationStore{
		DeleteConversationFn: func(_ context.Context, _ uuid.UUID, userID string) error {
			calledWith = userID
			return nil
		},
	}

	svc := conversation.NewService(mock)
	require.NoError(t, svc.Delete(context.Background(), alice, target))
	assert.Equal(t, "alice", calledWith)

	mock.DeleteConversationFn = func(context.Context, uuid.UUID, string) error {
		return domain.ErrNotFound
	}
	assert.ErrorIs(t, svc.Delete(context.Background(), alice, target), domain.ErrNotFound)
}

func TestZeroPrincipalRejected(t *testing.T) {
	svc := conversation.NewService(&mocks.MockConversationStore{})
	_, err := svc.List(context.Background(), domain.Principal{})
	assert.Error(t, err)
}
