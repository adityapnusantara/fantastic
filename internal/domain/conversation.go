// Package domain holds fantastic's core business types and errors,
// independent of frameworks and infrastructure.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Conversation is a chat thread owned by exactly one user.
type Conversation struct {
	ID              uuid.UUID
	UserID          string
	Title           string
	RollingSummary  string
	SummaryUntilSeq int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// NewConversation builds a conversation for the given owner. Timestamps
// are left zero: the store assigns them server-side so every row shares
// one clock.
func NewConversation(userID string) *Conversation {
	return &Conversation{
		ID:              uuid.New(),
		UserID:          userID,
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
