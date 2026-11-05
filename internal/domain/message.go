package domain

import (
	"time"

	"github.com/google/uuid"
)

// Role enumerates the message roles persisted in a conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall is a tool invocation recorded on an assistant message.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string as produced/consumed by the model
}

// Message is one persisted turn message. Seq orders messages within a
// conversation; IDs are stable and idempotency keys.
type Message struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	Seq            int64
	Role           Role
	Content        string
	ToolCalls      []ToolCall // set for assistant messages that call tools
	ToolCallID     string     // set for tool-result messages
	CreatedAt      time.Time
}

// NewMessage builds a message with a fresh id (the idempotency key).
func NewMessage(conversationID uuid.UUID, role Role, content string) *Message {
	return &Message{
		ID:             uuid.New(),
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
	}
}
