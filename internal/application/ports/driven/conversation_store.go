// Package driven defines the driven (secondary) ports implemented by
// infrastructure adapters.
package driven

import (
	"context"

	"github.com/google/uuid"

	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// ConversationStore persists conversations and messages. Every method is
// user-scoped: userID must match the conversation owner or the operation
// behaves as if the conversation does not exist (domain.ErrNotFound).
type ConversationStore interface {
	// CreateConversation persists a new conversation.
	CreateConversation(ctx context.Context, conv *domain.Conversation) error

	// GetConversation returns the conversation when owned by userID.
	GetConversation(ctx context.Context, conversationID uuid.UUID, userID string) (*domain.Conversation, error)

	// ListConversations returns the caller's conversations ordered by
	// most recent activity first.
	ListConversations(ctx context.Context, userID string) ([]domain.Conversation, error)

	// DeleteConversation removes the conversation and its messages.
	// Returns domain.ErrNotFound when not owned by userID.
	DeleteConversation(ctx context.Context, conversationID uuid.UUID, userID string) error

	// AppendMessage persists one message atomically: assigns the next seq
	// within the conversation, ignores duplicate message ids (idempotent
	// retry), bumps updated_at, and optionally sets the title (when
	// title is non-nil) so a conversation is auto-titled from its first
	// user message.
	AppendMessage(ctx context.Context, conversationID uuid.UUID, userID string, msg *domain.Message, title *string) (int64, error)

	// AppendMessages persists several messages in one transaction with
	// consecutive seq values, same idempotency and updated_at semantics
	// as AppendMessage.
	AppendMessages(ctx context.Context, conversationID uuid.UUID, userID string, msgs []*domain.Message, title *string) ([]int64, error)

	// GetMessages returns the conversation's messages in seq order.
	GetMessages(ctx context.Context, conversationID uuid.UUID, userID string) ([]domain.Message, error)

	// LoadWindow returns the conversation plus its most recent messages
	// (seq order preserved) for building model context.
	LoadWindow(ctx context.Context, conversationID uuid.UUID, userID string, limit int) (*domain.Conversation, []domain.Message, error)

	// SaveSummary updates the rolling summary and its coverage marker.
	SaveSummary(ctx context.Context, conversationID uuid.UUID, userID, summary string, untilSeq int64) error
}
