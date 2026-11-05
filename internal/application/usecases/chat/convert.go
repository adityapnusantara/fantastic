// Package chat implements the driver-side chat turn use case: it builds
// model context from stored history, runs the streaming agent, and
// persists assembled output.
package chat

import (
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// domainToSchema converts a stored message into the model-facing shape.
func domainToSchema(m *domain.Message) *schema.Message {
	switch m.Role {
	case domain.RoleUser:
		return schema.UserMessage(m.Content)
	case domain.RoleAssistant:
		if len(m.ToolCalls) > 0 {
			calls := make([]schema.ToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				calls = append(calls, schema.ToolCall{
					ID:       tc.ID,
					Type:     "function",
					Function: schema.FunctionCall{Name: tc.Name, Arguments: tc.Arguments},
				})
			}
			return schema.AssistantMessage(m.Content, calls)
		}
		return schema.AssistantMessage(m.Content, nil)
	case domain.RoleTool:
		return schema.ToolMessage(m.Content, m.ToolCallID)
	default:
		return schema.UserMessage(m.Content)
	}
}

// schemaToDomain converts an assembled model message into the stored
// shape. The conversation id and seq are filled by the caller/store.
func schemaToDomain(msg *schema.Message) *domain.Message {
	m := &domain.Message{
		ID:      uuid.New(),
		Role:    domain.Role(msg.Role),
		Content: msg.Content,
	}
	if len(msg.ToolCalls) > 0 {
		calls := make([]domain.ToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			calls = append(calls, domain.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		m.ToolCalls = calls
	}
	if msg.Role == schema.Tool {
		m.ToolCallID = msg.ToolCallID
	}
	return m
}
