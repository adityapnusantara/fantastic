package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// ErrAgentMismatch is returned when the addressed conversation is bound
// to a different agent than the one the turn names (design D7: the
// conversation belongs to its agent for its whole lifetime; the HTTP
// layer maps this to 409). Nothing is persisted on mismatch.
var ErrAgentMismatch = errors.New("conversation is bound to another agent")

// Config tunes the chat service.
type Config struct {
	// AgentName is the ADK agent identity; it is stamped onto every
	// emitted event (Event.AgentName). Empty falls back to "fantastic".
	AgentName string
	// AgentDescription describes the agent to the ADK (used in
	// multi-agent transfer prompts). Empty falls back to the default
	// personal-assistant description.
	AgentDescription string
	// SystemPrompt is the agent instruction (system prompt).
	SystemPrompt string
	// HistoryWindow is how many recent messages accompany the summary.
	HistoryWindow int
	// SummaryPrefix precedes the rolling summary in model context.
	SummaryPrefix string
}

// Service runs chat turns against the agent, persisting history.
//
// The agent runs on eino's agentic message path (model.AgenticModel /
// *schema.AgenticMessage) so reasoning models that require the OpenAI
// Responses API — e.g. gpt-6.1-sol with function tools — work unchanged
// alongside classic chat-completions models (agenticopenai supports both).
type Service struct {
	store  driven.ConversationStore
	model  model.AgenticModel
	runner *adk.TypedRunner[*schema.AgenticMessage]
	cfg    Config
	log    *slog.Logger
}

// NewService builds the streaming agent (ReAct loop) over the model and
// tools.
func NewService(store driven.ConversationStore, chatModel model.AgenticModel, tools []tool.BaseTool, cfg Config, log *slog.Logger) (*Service, error) {
	ctx := context.Background()
	name, description := agentIdentity(cfg)
	agent, err := adk.NewTypedChatModelAgent[*schema.AgenticMessage](ctx, &adk.TypedChatModelAgentConfig[*schema.AgenticMessage]{
		Name:        name,
		Description: description,
		Instruction: cfg.SystemPrompt,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create chat model agent: %w", err)
	}

	return &Service{
		store:  store,
		model:  chatModel,
		runner: adk.NewTypedRunner[*schema.AgenticMessage](adk.TypedRunnerConfig[*schema.AgenticMessage]{Agent: agent, EnableStreaming: true}),
		cfg:    cfg,
		log:    log,
	}, nil
}

// Stream runs one turn for the named agent: it validates ownership and
// the conversation's agent binding, persists the user message before any
// provider work (an errored turn still shows what was asked), streams
// agent events on the returned channel, and persists the assembled output
// once the turn ends — however it ends. The channel closes when the turn
// is over. A conversation bound to another agent fails with
// ErrAgentMismatch and nothing is persisted.
func (s *Service) Stream(ctx context.Context, p domain.Principal, conversationID uuid.UUID, agentID string, text string) (<-chan Event, error) {
	conv, recent, err := s.store.LoadWindow(ctx, conversationID, p.UserID, s.cfg.HistoryWindow)
	if err != nil {
		return nil, err // domain.ErrNotFound for foreign/missing conversations
	}
	if conv.AgentID != agentID {
		return nil, ErrAgentMismatch // refused before anything is persisted
	}

	// Durability: the user message is stored before the provider runs.
	userMsg := domain.NewMessage(conversationID, domain.RoleUser, text)
	var title *string
	if conv.Title == "" {
		t := deriveTitle(text)
		title = &t
	}
	if _, err := s.store.AppendMessage(ctx, conversationID, p.UserID, userMsg, title); err != nil {
		return nil, fmt.Errorf("persist user message: %w", err)
	}

	out := make(chan Event, 64)
	// runCtx follows the caller: a client disconnect cancels the provider.
	runCtx, cancel := context.WithCancel(ctx)
	// persistCtx survives the disconnect so partial output still lands.
	persistCtx := context.WithoutCancel(ctx)

	go func() {
		defer cancel()
		s.run(runCtx, persistCtx, conv, recent, userMsg, out)
	}()
	return out, nil
}

// run consumes the ADK event iterator, emits neutral events, and persists
// the assembled turn output.
func (s *Service) run(runCtx, persistCtx context.Context, conv *domain.Conversation, recent []domain.Message, userMsg *domain.Message, out chan<- Event) {
	defer close(out)

	var assembled []*domain.Message // in order of occurrence
	emit := func(ev Event) {
		select {
		case out <- ev:
		case <-runCtx.Done():
		}
	}

	iterator := s.runner.Run(runCtx, s.buildInput(conv, recent, userMsg))
	for {
		ev, ok := iterator.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			s.log.Warn("agent run failed", slog.String("error", ev.Err.Error()))
			emit(Event{Type: EventError, Error: ev.Err.Error()})
			s.persist(persistCtx, conv, userMsg, assembled, out)
			return
		}
		if ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}
		mo := ev.Output.MessageOutput

		switch {
		case mo.IsStreaming && mo.MessageStream != nil:
			msg, streamErr := s.drainStream(out, mo.MessageStream, ev.AgentName)
			if msg != nil && (agenticText(msg) != "" || agenticHasToolCalls(msg)) {
				// Keep whatever was produced, even on interruption: a
				// cancelled turn still persists its partial output.
				dm := agenticToDomain(msg)
				assembled = append(assembled, dm)
				if agenticHasToolCalls(msg) {
					// After a streamed assistant tool-call message
					// completes, announce it once.
					emit(messageEvent(ev.AgentName, msg))
				}
			}
			if streamErr != nil {
				emit(Event{Type: EventError, Error: "stream interrupted"})
				s.persist(persistCtx, conv, userMsg, assembled, out)
				return
			}
		case mo.Message != nil:
			msg := mo.Message
			if toolRes := toolResultOf(msg); toolRes != nil {
				dm := toolResultToDomain(toolRes)
				assembled = append(assembled, dm)
				emit(Event{
					Type:       EventToolResult,
					AgentName:  ev.AgentName,
					Content:    dm.Content,
					ToolCallID: dm.ToolCallID,
				})
				continue
			}
			assembled = append(assembled, agenticToDomain(msg))
			emit(messageEvent(ev.AgentName, msg))
		}
	}

	if err := s.persist(persistCtx, conv, userMsg, assembled, out); err != nil {
		emit(Event{Type: EventError, Error: "failed to persist turn output"})
		return
	}

	final := lastAssistantContent(assembled)
	id := lastAssistantID(assembled)
	emit(Event{Type: EventDone, Content: final, MessageID: id})

	// Compaction runs after the turn is complete and must not be blocked
	// by the client leaving: it uses the detached context.
	go s.maybeCompact(persistCtx, conv.ID, conv.UserID)
}

// maybeCompact folds messages that fell outside the window into the
// conversation's rolling summary. Raw messages are never touched.
func (s *Service) maybeCompact(ctx context.Context, conversationID uuid.UUID, userID string) {
	conv, recent, err := s.store.LoadWindow(ctx, conversationID, userID, s.cfg.HistoryWindow)
	if err != nil || len(recent) == 0 {
		return
	}
	windowStart := recent[0].Seq
	if conv.SummaryUntilSeq >= windowStart-1 {
		return // everything outside the window is already covered
	}

	all, err := s.store.GetMessages(ctx, conversationID, userID)
	if err != nil {
		return
	}
	var uncovered []domain.Message
	for _, m := range all {
		if m.Seq > conv.SummaryUntilSeq && m.Seq < windowStart {
			uncovered = append(uncovered, m)
		}
	}
	if len(uncovered) == 0 {
		return
	}

	prompt := summaryPrompt(conv.RollingSummary, uncovered)
	res, err := s.model.Generate(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage(prompt)})
	if err != nil {
		s.log.Warn("summarization failed; summary left as-is", slog.String("error", err.Error()))
		return
	}
	summary := agenticText(res)
	if err := s.store.SaveSummary(ctx, conversationID, userID, summary, windowStart-1); err != nil {
		s.log.Warn("save summary failed", slog.String("error", err.Error()))
	}
}

// summaryPrompt builds the summarization instruction.
func summaryPrompt(previous string, uncovered []domain.Message) string {
	var b strings.Builder
	b.WriteString("Summarize the conversation so far, preserving key facts, decisions, tool results and open questions. Be concise.")
	if previous != "" {
		b.WriteString("\n\nPrevious summary:\n")
		b.WriteString(previous)
	}
	b.WriteString("\n\nNew messages to fold into the summary:\n")
	for _, m := range uncovered {
		switch m.Role {
		case domain.RoleTool:
			b.WriteString(fmt.Sprintf("tool result (%s): %s\n", m.ToolCallID, m.Content))
		case domain.RoleAssistant:
			b.WriteString("assistant: " + m.Content)
			for _, tc := range m.ToolCalls {
				b.WriteString(fmt.Sprintf(" [called tool %s(%s)]", tc.Name, tc.Arguments))
			}
			b.WriteString("\n")
		default:
			b.WriteString(string(m.Role) + ": " + m.Content + "\n")
		}
	}
	return b.String()
}

// persist writes the assembled output. On failure the turn is reported as
// failed: output that is not stored must not look successful.
func (s *Service) persist(ctx context.Context, conv *domain.Conversation, userMsg *domain.Message, assembled []*domain.Message, out chan<- Event) error {
	if len(assembled) == 0 {
		return nil
	}
	if _, err := s.store.AppendMessages(ctx, conv.ID, conv.UserID, assembled, nil); err != nil {
		s.log.Warn("persist turn output failed", slog.String("error", err.Error()))
		return err
	}
	_ = userMsg
	return nil
}

// drainStream forwards text deltas as stream_chunk events and returns the
// assembled message for persistence. On stream error it returns whatever
// chunks arrived so far so partial output survives.
func (s *Service) drainStream(out chan<- Event, stream *schema.StreamReader[*schema.AgenticMessage], agentName string) (*schema.AgenticMessage, error) {
	var chunks []*schema.AgenticMessage
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			stream.Close()
			if len(chunks) == 0 {
				return nil, err
			}
			partial, concatErr := schema.ConcatAgenticMessages(chunks)
			if concatErr != nil {
				return nil, err
			}
			return partial, err
		}
		if text := agenticText(chunk); text != "" {
			out <- Event{Type: EventStreamChunk, AgentName: agentName, Content: text}
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) == 0 {
		return nil, nil
	}
	assembled, err := schema.ConcatAgenticMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("concat stream chunks: %w", err)
	}
	return assembled, nil
}

// buildInput assembles the model context: rolling summary, windowed
// history, then the new user message. Tool names for stored tool results
// are recovered from the assistant tool-call messages that precede them.
// A tool result whose assistant tool-call partner is no longer in the
// window is dropped: it was folded into the rolling summary, and an
// orphan function output without its call is rejected by the provider
// (OpenAI /responses returns 400 "No tool call found for function call
// output").
func (s *Service) buildInput(conv *domain.Conversation, recent []domain.Message, userMsg *domain.Message) []*schema.AgenticMessage {
	input := make([]*schema.AgenticMessage, 0, len(recent)+2)
	if conv.RollingSummary != "" {
		prefix := s.cfg.SummaryPrefix
		if prefix == "" {
			prefix = defaultSummaryPrefix
		}
		input = append(input, schema.SystemAgenticMessage(prefix+conv.RollingSummary))
	}
	toolNames := map[string]string{} // call id -> tool name
	for i := range recent {
		for _, tc := range recent[i].ToolCalls {
			toolNames[tc.ID] = tc.Name
		}
	}
	for i := range recent {
		m := &recent[i]
		if m.Role == domain.RoleTool && toolNames[m.ToolCallID] == "" {
			continue
		}
		input = append(input, domainToAgentic(m, toolNames))
	}
	input = append(input, domainToAgentic(userMsg, toolNames))
	return input
}

func messageEvent(agentName string, msg *schema.AgenticMessage) Event {
	ev := Event{
		Type:      EventMessage,
		AgentName: agentName,
		Content:   agenticText(msg),
	}
	for _, block := range msg.ContentBlocks {
		if block != nil && block.Type == schema.ContentBlockTypeFunctionToolCall && block.FunctionToolCall != nil {
			fc := block.FunctionToolCall
			ev.ToolCalls = append(ev.ToolCalls, ToolCall{ID: fc.CallID, Name: fc.Name, Arguments: fc.Arguments})
		}
	}
	return ev
}

func lastAssistantContent(assembled []*domain.Message) string {
	for i := len(assembled) - 1; i >= 0; i-- {
		if assembled[i].Role == domain.RoleAssistant {
			return assembled[i].Content
		}
	}
	return ""
}

func lastAssistantID(assembled []*domain.Message) string {
	for i := len(assembled) - 1; i >= 0; i-- {
		if assembled[i].Role == domain.RoleAssistant {
			return assembled[i].ID.String()
		}
	}
	return ""
}

// deriveTitle truncates the first user message into a conversation title.
func deriveTitle(content string) string {
	const maxTitleRunes = 80
	runes := []rune(content)
	if len(runes) > maxTitleRunes {
		runes = runes[:maxTitleRunes]
	}
	return string(runes)
}

const defaultSummaryPrefix = "Summary of the earlier conversation (messages below are more recent):\n"

// Built-in chat agent identity, used when Config leaves the agent fields
// empty (pre-registry callers that do not know about per-agent identity).
const (
	defaultAgentName        = "fantastic"
	defaultAgentDescription = "A personal assistant with MCP tools"
)

// agentIdentity resolves the configured agent identity against the
// built-in defaults.
func agentIdentity(cfg Config) (name, description string) {
	name = cfg.AgentName
	if name == "" {
		name = defaultAgentName
	}
	description = cfg.AgentDescription
	if description == "" {
		description = defaultAgentDescription
	}
	return name, description
}

// agenticText concatenates the generated text blocks of an assistant
// message (or generated deltas of a stream chunk).
func agenticText(msg *schema.AgenticMessage) string {
	var b strings.Builder
	for _, block := range msg.ContentBlocks {
		if block != nil && block.Type == schema.ContentBlockTypeAssistantGenText && block.AssistantGenText != nil {
			b.WriteString(block.AssistantGenText.Text)
		}
	}
	return b.String()
}

func agenticHasToolCalls(m *schema.AgenticMessage) bool {
	for _, block := range m.ContentBlocks {
		if block != nil && block.Type == schema.ContentBlockTypeFunctionToolCall && block.FunctionToolCall != nil {
			return true
		}
	}
	return false
}

// toolResultOf extracts the tool result carried by a message produced by
// the tools node (AgenticRoleTypeUser with a function_tool_result block).
func toolResultOf(msg *schema.AgenticMessage) *schema.FunctionToolResult {
	if msg.Role != schema.AgenticRoleTypeUser {
		return nil
	}
	for _, block := range msg.ContentBlocks {
		if block != nil && block.Type == schema.ContentBlockTypeFunctionToolResult && block.FunctionToolResult != nil {
			return block.FunctionToolResult
		}
	}
	return nil
}

// agenticToDomain converts an assistant message into a domain message.
func agenticToDomain(msg *schema.AgenticMessage) *domain.Message {
	dm := domain.NewMessage(uuid.Nil, domain.RoleAssistant, agenticText(msg))
	for _, block := range msg.ContentBlocks {
		if block != nil && block.Type == schema.ContentBlockTypeFunctionToolCall && block.FunctionToolCall != nil {
			fc := block.FunctionToolCall
			dm.ToolCalls = append(dm.ToolCalls, domain.ToolCall{ID: fc.CallID, Name: fc.Name, Arguments: fc.Arguments})
		}
	}
	return dm
}

// toolResultToDomain converts a tool result block into a domain message.
func toolResultToDomain(fr *schema.FunctionToolResult) *domain.Message {
	var b strings.Builder
	for _, cb := range fr.Content {
		if cb != nil && cb.Type == schema.FunctionToolResultContentBlockTypeText && cb.Text != nil {
			b.WriteString(cb.Text.Text)
		}
	}
	dm := domain.NewMessage(uuid.Nil, domain.RoleTool, b.String())
	dm.ToolCallID = fr.CallID
	return dm
}

// domainToAgentic converts a persisted message back into model context.
// Assistant tool-call messages carry function_tool_call blocks; tool
// results become user-role messages with function_tool_result blocks, the
// shape the ADK itself produces in-flight.
func domainToAgentic(m *domain.Message, toolNames map[string]string) *schema.AgenticMessage {
	switch m.Role {
	case domain.RoleUser:
		return schema.UserAgenticMessage(m.Content)
	case domain.RoleTool:
		blocks := []*schema.ContentBlock{{
			Type: schema.ContentBlockTypeFunctionToolResult,
			FunctionToolResult: &schema.FunctionToolResult{
				CallID: m.ToolCallID,
				Name:   toolNames[m.ToolCallID],
				Content: []*schema.FunctionToolResultContentBlock{{
					Type: schema.FunctionToolResultContentBlockTypeText,
					Text: &schema.UserInputText{Text: m.Content},
				}},
			},
		}}
		return &schema.AgenticMessage{Role: schema.AgenticRoleTypeUser, ContentBlocks: blocks}
	default: // assistant
		var blocks []*schema.ContentBlock
		if m.Content != "" {
			blocks = append(blocks, &schema.ContentBlock{
				Type:             schema.ContentBlockTypeAssistantGenText,
				AssistantGenText: &schema.AssistantGenText{Text: m.Content},
			})
		}
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, &schema.ContentBlock{
				Type: schema.ContentBlockTypeFunctionToolCall,
				FunctionToolCall: &schema.FunctionToolCall{
					CallID:    tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		if blocks == nil {
			blocks = []*schema.ContentBlock{{
				Type:             schema.ContentBlockTypeAssistantGenText,
				AssistantGenText: &schema.AssistantGenText{Text: ""},
			}}
		}
		return &schema.AgenticMessage{Role: schema.AgenticRoleTypeAssistant, ContentBlocks: blocks}
	}
}
