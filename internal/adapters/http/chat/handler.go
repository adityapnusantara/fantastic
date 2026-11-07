// Package chat exposes the SSE chat endpoint. Its wire format matches
// eino's adk/intro/http-sse-service example: one `data: <json>` frame per
// event with a "type" field (stream_chunk, message, tool_result, error,
// done).
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	chatusecase "github.com/adityapratamanusantara/fantastic/internal/application/usecases/chat"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/middleware"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/response"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// Streamer is the chat capability the handler needs: one streaming turn
// for the agent the request names.
type Streamer interface {
	Stream(ctx context.Context, p domain.Principal, conversationID uuid.UUID, agentID string, text string) (<-chan chatusecase.Event, error)
}

// AgentResolver resolves the agent id named in the request path to that
// agent's streaming service. ok is false for unknown or disabled agents,
// which the handler rejects with 404 before anything else happens.
type AgentResolver interface {
	Resolve(agentID string) (Streamer, bool)
}

// ResolverFunc adapts a plain function to AgentResolver (the bootstrap
// wiring uses it for its transitional single-service resolver).
type ResolverFunc func(agentID string) (Streamer, bool)

// Resolve implements AgentResolver.
func (f ResolverFunc) Resolve(agentID string) (Streamer, bool) { return f(agentID) }

// ConversationCreator creates new conversation rows bound to an agent, so
// a chat request without a conversation id can start one bound to the
// agent in the path.
type ConversationCreator interface {
	CreateForAgent(ctx context.Context, p domain.Principal, agentID string) (*domain.Conversation, error)
}

// chatRequest is the POST /api/v1/agents/{agent}/chat payload.
type chatRequest struct {
	ConversationID *uuid.UUID `json:"conversation_id"` // optional: absent starts a new conversation
	Message        string     `json:"message"`
}

// sseEvent mirrors the example's SSEEvent JSON shape.
type sseEvent struct {
	Type       string         `json:"type"`
	AgentName  string         `json:"agent_name,omitempty"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Error      string         `json:"error,omitempty"`
	MessageID  string         `json:"message_id,omitempty"`
}

// wireToolCall keeps the example's tool_calls JSON shape.
type wireToolCall struct {
	ID       string     `json:"id"`
	Type     string     `json:"type"`
	Function wireFnCall `json:"function"`
}

type wireFnCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// NewHandler builds the POST /api/v1/agents/{agent}/chat handler: the
// path names the agent, whose service is resolved per request (design
// D1/D4); unknown or disabled agents are 404, and a conversation bound to
// another agent is 409 (design D7).
//
//	@Summary		Stream one chat turn for an agent
//	@Description	SSE: emits data frames with types stream_chunk, message, tool_result, done, error (eino http-sse-service format). The Conversation-Id response header carries the conversation id (created on the fly and bound to the path agent when the request omits it). Unknown or disabled agents are rejected with 404; chatting into a conversation bound to another agent is rejected with 409.
//	@Tags			chat
//	@Accept			json
//	@Produce		text/event-stream
//	@Param			agent	path	string		true	"agent id (slug)"
//	@Param			request	body	chatRequest	true	"message and optional conversation id"
//	@Success		200		{string}	string
//	@Failure		400		{object}	response.Envelope
//	@Failure		401		{object}	response.Envelope
//	@Failure		404		{object}	response.Envelope
//	@Failure		409		{object}	response.Envelope
//	@Security		bearerAuth
//	@Router			/agents/{agent}/chat [post]
func NewHandler(resolver AgentResolver, conversations ConversationCreator, log *slog.Logger) echo.HandlerFunc {
	return func(c *echo.Context) error {
		p, ok := middleware.PrincipalFrom(c)
		if !ok {
			return response.Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
		}

		agentID := c.Param("agent")
		if resolver == nil || agentID == "" {
			return response.Fail(c, http.StatusNotFound, "not_found", "unknown or disabled agent")
		}
		svc, ok := resolver.Resolve(agentID)
		if !ok {
			return response.Fail(c, http.StatusNotFound, "not_found", "unknown or disabled agent")
		}

		var req chatRequest
		if err := c.Bind(&req); err != nil || req.Message == "" {
			return response.Fail(c, http.StatusBadRequest, "invalid_request", "message is required")
		}

		convID := uuid.New()
		if req.ConversationID != nil {
			convID = *req.ConversationID
		} else if conversations != nil {
			// starting a new conversation: persist its row first, bound to
			// the agent in the path, so the turn's user message has an owner
			// row to attach to
			conv, err := conversations.CreateForAgent(c.Request().Context(), p, agentID)
			if err != nil {
				return response.Fail(c, http.StatusInternalServerError, "internal", "failed to create conversation")
			}
			convID = conv.ID
		}

		events, err := svc.Stream(c.Request().Context(), p, convID, agentID, req.Message)
		if err != nil {
			switch {
			case errors.Is(err, domain.ErrNotFound):
				return response.Fail(c, http.StatusNotFound, "not_found", "conversation not found")
			case errors.Is(err, chatusecase.ErrAgentMismatch):
				return response.Fail(c, http.StatusConflict, "conflict", "conversation belongs to another agent")
			default:
				log.Warn("chat stream failed to start", slog.String("error", err.Error()))
				return response.Fail(c, http.StatusInternalServerError, "internal", "failed to start chat turn")
			}
		}

		w := c.Response()
		header := w.Header()
		header.Set("Content-Type", "text/event-stream")
		header.Set("Cache-Control", "no-cache")
		header.Set("Connection", "keep-alive")
		header.Set("X-Accel-Buffering", "no")
		header.Set("Conversation-Id", convID.String())
		w.WriteHeader(http.StatusOK)

		flusher, canFlush := w.(http.Flusher)

		for ev := range events {
			data, err := json.Marshal(toWire(ev))
			if err != nil {
				return nil
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				// client disconnected mid-stream: returning cancels the
				// request context, so the usecase stops the provider and
				// persists the partial output
				return nil
			}
			if canFlush {
				flusher.Flush()
			}
		}
		return nil
	}
}

// toWire converts a usecase event into the example's JSON shape.
func toWire(ev chatusecase.Event) sseEvent {
	out := sseEvent{
		Type:       ev.Type,
		AgentName:  ev.AgentName,
		Content:    ev.Content,
		ToolCallID: ev.ToolCallID,
		Error:      ev.Error,
		MessageID:  ev.MessageID,
	}
	for _, tc := range ev.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, wireToolCall{
			ID:       tc.ID,
			Type:     "function",
			Function: wireFnCall{Name: tc.Name, Arguments: tc.Arguments},
		})
	}
	return out
}
