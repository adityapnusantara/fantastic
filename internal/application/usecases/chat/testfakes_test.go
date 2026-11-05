package chat_test

import (
	"context"
	"errors"
	"sync"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// fakeChatModel scripts streaming responses per model call. Each entry is
// the chunk sequence of one turn; an entry may carry an error instead.
type fakeChatModel struct {
	mu      sync.Mutex
	calls   int
	scripts []fakeScript

	// generateScript is the response returned by Generate (used by the
	// summarizer); generateInputs records every Generate input.
	generateScript string
	generateErr    error
	generateCalls  int

	debugf func(format string, args ...any)

	lastInputMu  sync.Mutex
	lastStreamIn []*schema.AgenticMessage
}

// lastStreamInput returns the input of the most recent Stream call.
func (f *fakeChatModel) lastStreamInput() []*schema.AgenticMessage {
	f.lastInputMu.Lock()
	defer f.lastInputMu.Unlock()
	return f.lastStreamIn
}

type fakeScript struct {
	chunks []*schema.AgenticMessage
	err    error
	// holdUntil is closed (or cancelled) before the final chunk is sent,
	// simulating a slow stream the test can interrupt.
	holdUntil <-chan struct{}
}

func (f *fakeChatModel) Generate(_ context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.AgenticMessage, error) {
	f.mu.Lock()
	f.generateCalls++
	f.mu.Unlock()
	if f.generateErr != nil {
		return nil, f.generateErr
	}
	if f.generateScript == "" {
		return nil, errors.New("Generate not scripted in tests")
	}
	// Model output is assistant-shaped: AssistantGenText blocks, which is
	// what the service's agenticText extracts for the summary.
	return assistantAgentic(f.generateScript), nil
}

// Stream implements model.AgenticModel, returning the next scripted turn
// as a chunk stream.
func (f *fakeChatModel) Stream(ctx context.Context, input []*schema.AgenticMessage, _ ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	f.lastInputMu.Lock()
	f.lastStreamIn = input
	f.lastInputMu.Unlock()

	f.mu.Lock()
	idx := f.calls
	f.calls++
	f.mu.Unlock()

	if idx >= len(f.scripts) {
		return nil, errors.New("no scripted response for call")
	}
	sc := f.scripts[idx]
	if sc.err != nil {
		return nil, sc.err
	}

	sr, sw := schema.Pipe[*schema.AgenticMessage](len(sc.chunks) + 1)
	if f.debugf != nil {
		f.debugf("pipe created chunks=%d", len(sc.chunks))
	}
	go func() {
		defer func() {
			if f.debugf != nil {
				f.debugf("goroutine exit -> sw.Close()")
			}
			sw.Close()
		}()
		for i, chunk := range sc.chunks {
			if i == len(sc.chunks)-1 && sc.holdUntil != nil {
				select {
				case <-sc.holdUntil:
				case <-ctx.Done():
					if f.debugf != nil {
						f.debugf("ctx done before chunk %d", i)
					}
					return
				}
			}
			closed := sw.Send(chunk, nil)
			if f.debugf != nil {
				f.debugf("send %d closed=%v text=%q", i, closed, agenticChunkText(chunk))
			}
			if closed {
				return
			}
		}
	}()
	return sr, nil
}

func agenticChunkText(m *schema.AgenticMessage) string {
	var s string
	for _, b := range m.ContentBlocks {
		if b != nil && b.Type == schema.ContentBlockTypeAssistantGenText && b.AssistantGenText != nil {
			s += b.AssistantGenText.Text
		}
	}
	return s
}

// textChunks splits s into per-chunk assistant messages.
func textChunks(s string) []*schema.AgenticMessage {
	var out []*schema.AgenticMessage
	for _, part := range splitRunes(s, 3) {
		out = append(out, assistantAgentic(part))
	}
	return out
}

func splitRunes(s string, size int) []string {
	var out []string
	r := []rune(s)
	for i := 0; i < len(r); i += size {
		end := i + size
		if end > len(r) {
			end = len(r)
		}
		out = append(out, string(r[i:end]))
	}
	return out
}

// assistantAgentic builds an assistant message with a single text block.
func assistantAgentic(text string) *schema.AgenticMessage {
	return &schema.AgenticMessage{
		Role: schema.AgenticRoleTypeAssistant,
		ContentBlocks: []*schema.ContentBlock{{
			Type:             schema.ContentBlockTypeAssistantGenText,
			AssistantGenText: &schema.AssistantGenText{Text: text},
		}},
	}
}

// toolCallChunks streams an assistant message that invokes one tool.
func toolCallChunks(id, name, args string) []*schema.AgenticMessage {
	return []*schema.AgenticMessage{
		assistantAgentic(""),
		{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: []*schema.ContentBlock{{
			Type: schema.ContentBlockTypeFunctionToolCall,
			FunctionToolCall: &schema.FunctionToolCall{
				CallID: id, Name: name, Arguments: args,
			},
		}}},
	}
}

// fakeTool is a minimal InvokableTool for the ADK to call.
type fakeTool struct{}

func (fakeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "demo_tool", Desc: "a demo tool"}, nil
}

func (fakeTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return `{"ok":true}`, nil
}
