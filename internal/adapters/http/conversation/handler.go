// Package conversation exposes the conversation management API: list,
// read flattened messages, and delete.
package conversation

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/middleware"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/response"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// Service is the conversation capability the handlers need.
type Service interface {
	List(ctx context.Context, p domain.Principal, agentID *string) ([]domain.Conversation, error)
	Get(ctx context.Context, p domain.Principal, id uuid.UUID) (*domain.Conversation, error)
	Messages(ctx context.Context, p domain.Principal, id uuid.UUID) ([]domain.Message, error)
	Delete(ctx context.Context, p domain.Principal, id uuid.UUID) error
	Create(ctx context.Context, p domain.Principal) (*domain.Conversation, error)
}

// conversationDTO is the wire form of a conversation.
type conversationDTO struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// messageDTO is the flattened wire form of a message, including tool
// calls and tool results, ready for chat UI hydration.
type messageDTO struct {
	ID         string        `json:"id"`
	Seq        int64         `json:"seq"`
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	ToolCalls  []toolCallDTO `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	CreatedAt  string        `json:"created_at"`
}

type toolCallDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func toConversationDTO(c domain.Conversation) conversationDTO {
	return conversationDTO{
		ID:        c.ID.String(),
		AgentID:   c.AgentID,
		Title:     c.Title,
		CreatedAt: c.CreatedAt.UTC().Format(timeFormat),
		UpdatedAt: c.UpdatedAt.UTC().Format(timeFormat),
	}
}

func toMessageDTOs(msgs []domain.Message) []messageDTO {
	out := make([]messageDTO, 0, len(msgs))
	for _, m := range msgs {
		dto := messageDTO{
			ID:         m.ID.String(),
			Seq:        m.Seq,
			Role:       string(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			CreatedAt:  m.CreatedAt.UTC().Format(timeFormat),
		}
		for _, tc := range m.ToolCalls {
			dto.ToolCalls = append(dto.ToolCalls, toolCallDTO{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		out = append(out, dto)
	}
	return out
}

const timeFormat = "2006-01-02T15:04:05.000Z07:00"

// notFound maps domain.ErrNotFound to the standard 404 envelope.
func notFound(c *echo.Context, err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return response.Fail(c, http.StatusNotFound, "not_found", "conversation not found")
	}
	return err
}

// Register mounts all conversation routes on the (authenticated) group.
func Register(g *echo.Group, svc Service) {
	h := &handlers{svc: svc}
	g.POST("/conversations", h.Create)
	g.GET("/conversations", h.List)
	g.GET("/conversations/:id", h.Get)
	g.GET("/conversations/:id/messages", h.Messages)
	g.DELETE("/conversations/:id", h.Delete)
}

type handlers struct {
	svc Service
}

func (h *handlers) principal(c *echo.Context) (domain.Principal, error) {
	p, ok := middleware.PrincipalFrom(c)
	if !ok {
		return domain.Principal{}, response.Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
	}
	return p, nil
}

// Create starts a new conversation.
//
//	@Summary	Create a conversation
//	@Tags		conversations
//	@Success	201	{object}	response.Envelope
//	@Security	bearerAuth
//	@Router		/conversations [post]
func (h *handlers) Create(c *echo.Context) error {
	p, err := h.principal(c)
	if err != nil {
		return err
	}
	conv, err := h.svc.Create(c.Request().Context(), p)
	if err != nil {
		return notFound(c, err)
	}
	return response.Created(c, toConversationDTO(*conv))
}

// List returns the caller's conversations, newest activity first.
//
//	@Summary		List my conversations
//	@Description	When agent_id is set, only conversations bound to that agent are returned.
//	@Tags			conversations
//	@Param			agent_id	query		string	false	"filter by bound agent id"
//	@Success		200	{object}	response.Envelope
//	@Security		bearerAuth
//	@Router			/conversations [get]
func (h *handlers) List(c *echo.Context) error {
	p, err := h.principal(c)
	if err != nil {
		return err
	}
	var agentID *string
	if v := c.QueryParam("agent_id"); v != "" {
		agentID = &v
	}
	convs, err := h.svc.List(c.Request().Context(), p, agentID)
	if err != nil {
		return notFound(c, err)
	}
	dtos := make([]conversationDTO, 0, len(convs))
	for _, cv := range convs {
		dtos = append(dtos, toConversationDTO(cv))
	}
	return response.OK(c, dtos)
}

// Get returns one conversation.
func (h *handlers) Get(c *echo.Context) error {
	p, err := h.principal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, "invalid_request", "invalid conversation id")
	}
	conv, err := h.svc.Get(c.Request().Context(), p, id)
	if err != nil {
		return notFound(c, err)
	}
	return response.OK(c, toConversationDTO(*conv))
}

// Messages returns the conversation's flattened history.
//
//	@Summary	Get conversation messages
//	@Description	Flattened user/assistant/tool messages in order, including tool calls and their results.
//	@Tags		conversations
//	@Param		id	path	string	true	"conversation id (uuid)"
//	@Success	200	{object}	response.Envelope
//	@Failure	404	{object}	response.Envelope
//	@Security	bearerAuth
//	@Router		/conversations/{id}/messages [get]
func (h *handlers) Messages(c *echo.Context) error {
	p, err := h.principal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, "invalid_request", "invalid conversation id")
	}
	msgs, err := h.svc.Messages(c.Request().Context(), p, id)
	if err != nil {
		return notFound(c, err)
	}
	return response.OK(c, toMessageDTOs(msgs))
}

// Delete removes the conversation and its messages.
//
//	@Summary	Delete a conversation
//	@Tags		conversations
//	@Param		id	path	string	true	"conversation id (uuid)"
//	@Success	204	{string}	string
//	@Failure	404	{object}	response.Envelope
//	@Security	bearerAuth
//	@Router		/conversations/{id} [delete]
func (h *handlers) Delete(c *echo.Context) error {
	p, err := h.principal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, "invalid_request", "invalid conversation id")
	}
	if err := h.svc.Delete(c.Request().Context(), p, id); err != nil {
		return notFound(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
