// Package domain holds fantastic's core business types and errors,
// independent of frameworks and infrastructure.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Conversation is a chat thread owned by exactly one user and bound to
// exactly one agent for its whole lifetime.
type Conversation struct {
	ID              uuid.UUID
	UserID          string
	AgentID         string
	Title           string
	RollingSummary  string
	SummaryUntilSeq int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// NewConversation builds a conversation for the given owner, bound to the
// given agent. Timestamps are left zero: the store assigns them server-side
// so every row shares one clock.
func NewConversation(userID, agentID string) *Conversation {
	return &Conversation{
		ID:              uuid.New(),
		UserID:          userID,
		AgentID:         agentID,
		SummaryUntilSeq: 0,
	}
}

// SetTitleFrom derives the conversation title from the first user message,
// truncating to maxTitleRunes.
func (c *Conversation) SetTitleFrom(content string) {
	if c.Title != "" {
		return
	}
	const maxTitleRunes = 80
	runes := []rune(content)
	if len(runes) > maxTitleRunes {
		runes = runes[:maxTitleRunes]
	}
	c.Title = string(runes)
}
