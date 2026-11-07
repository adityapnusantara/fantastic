// Package conversation implements the driver-side use cases for
// managing a caller's conversations.
package conversation

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// DefaultAgentID is the agent every conversation created through Create
// is bound to: the seeded default row from migration 0005.
const DefaultAgentID = "general"

// Service manages conversations for verified callers.
type Service struct {
	store driven.ConversationStore
}

// NewService builds a conversation service over the given store.
func NewService(store driven.ConversationStore) *Service {
	return &Service{store: store}
}

// List returns the caller's conversations, newest activity first. When
// agentID is non-nil, only conversations bound to that agent are returned.
func (s *Service) List(ctx context.Context, p domain.Principal, agentID *string) ([]domain.Conversation, error) {
	if p.IsZero() {
		return nil, errors.New("no principal")
	}
	convs, err := s.store.ListConversations(ctx, p.UserID, agentID)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	return convs, nil
}

// Messages returns the flattened message history of one of the caller's
// conversations.
func (s *Service) Messages(ctx context.Context, p domain.Principal, conversationID uuid.UUID) ([]domain.Message, error) {
	if _, err := s.store.GetConversation(ctx, conversationID, p.UserID); err != nil {
		return nil, err
	}
	msgs, err := s.store.GetMessages(ctx, conversationID, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	return msgs, nil
}

// Get returns one of the caller's conversations.
func (s *Service) Get(ctx context.Context, p domain.Principal, conversationID uuid.UUID) (*domain.Conversation, error) {
	return s.store.GetConversation(ctx, conversationID, p.UserID)
}

// Create starts a new, empty conversation for the caller, bound to the
// default agent (general). Convenience for callers that do not choose an
// agent; the chat path uses CreateForAgent.
func (s *Service) Create(ctx context.Context, p domain.Principal) (*domain.Conversation, error) {
	return s.CreateForAgent(ctx, p, DefaultAgentID)
}

// CreateForAgent starts a new, empty conversation for the caller, bound to
// the given agent for its whole lifetime.
func (s *Service) CreateForAgent(ctx context.Context, p domain.Principal, agentID string) (*domain.Conversation, error) {
	if p.IsZero() {
		return nil, errors.New("no principal")
	}
	if agentID == "" {
		return nil, errors.New("no agent id")
	}
	conv := domain.NewConversation(p.UserID, agentID)
	if err := s.store.CreateConversation(ctx, conv); err != nil {
		return nil, fmt.Errorf("create conversation: %w", err)
	}
	return conv, nil
}

// Delete removes one of the caller's conversations and its messages.
func (s *Service) Delete(ctx context.Context, p domain.Principal, conversationID uuid.UUID) error {
	if err := s.store.DeleteConversation(ctx, conversationID, p.UserID); err != nil {
		return err
	}
	return nil
}
