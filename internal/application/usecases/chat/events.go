package chat

// Event is the neutral, UI-facing event of one chat turn. The SSE handler
// renders it in the wire format of eino's http-sse-service example.
type Event struct {
	// Type is one of: stream_chunk, message, tool_result, error, done.
	Type string `json:"type"`
	// AgentName identifies which agent produced the event.
	AgentName string `json:"agent_name,omitempty"`
	// Content carries a stream delta or an assembled message body.
	Content string `json:"content,omitempty"`
	// ToolCalls is set on assistant messages that invoke tools.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID links a tool_result event to its call.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// Error describes a failed turn.
	Error string `json:"error,omitempty"`
	// MessageID is set on done events: the persisted assistant message id.
	MessageID string `json:"message_id,omitempty"`
}

// ToolCall mirrors an assistant tool invocation for the wire.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Event types.
const (
	EventStreamChunk = "stream_chunk"
	EventMessage     = "message"
	EventToolResult  = "tool_result"
	EventError       = "error"
	EventDone        = "done"
)
