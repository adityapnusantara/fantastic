// Package agent exposes the agent management API: an ungated metadata
// listing for every authenticated user, and admin-gated full detail plus
// CRUD (spec: Agent management API). The id is a path slug for every
// /agents/{id} operation, so a write body can never rename an agent
// (spec: Agent id is immutable). The handlers map the agentadmin use
// case's errors onto the HTTP envelope: ValidationError to 400,
// domain.ErrNotFound to 404 and domain.ErrAgentInUse to 409.
package agent

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/middleware"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/response"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/agentadmin"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// Manager is the agent management capability the handlers need. It is
// the agentadmin.Service surface, wire-form agnostic: errors carry the
// semantics (ValidationError, ErrNotFound, ErrAgentInUse), not statuses.
type Manager interface {
	Create(ctx context.Context, id string, in agentadmin.Input) (domain.Agent, error)
	Update(ctx context.Context, id string, in agentadmin.Input) (domain.Agent, error)
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (domain.Agent, []string, error)
	List(ctx context.Context) ([]domain.Agent, map[string][]string, error)
	ToolNames() []string
}

// agentSummaryDTO is the listing wire form: metadata only. The system
// prompt, tool assignment, and overrides are deliberately absent — the
// listing is open to every authenticated user (spec: Agent management
// API), so it must never leak the prompt even to an admin caller.
type agentSummaryDTO struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"display_name"`
	Description   string   `json:"description"`
	Icon          string   `json:"icon"`
	Enabled       bool     `json:"enabled"`
	SortOrder     int      `json:"sort_order"`
	DegradedTools []string `json:"degraded_tools"`
}

// agentDetailDTO adds the admin-only fields to the listing metadata.
// The overrides are pointers and render as null when unset (nil = the
// agent inherits the global llm block).
type agentDetailDTO struct {
	agentSummaryDTO
	SystemPrompt    string   `json:"system_prompt"`
	Tools           []string `json:"tools"`
	Model           *string  `json:"model"`
	API             *string  `json:"api"`
	Temperature     *float32 `json:"temperature"`
	ReasoningEffort *string  `json:"reasoning_effort"`
}

// agentWriteRequest is the POST/PATCH body. Every field is a pointer so
// PATCH is a true partial: absent fields keep their current values, and
// an explicit empty tools array replaces the assignment. The id is
// deliberately absent — it comes from the path and is immutable. On
// POST missing strings default empty, enabled defaults true and sort
// order defaults 0; nil overrides inherit the global llm default.
type agentWriteRequest struct {
	DisplayName     *string   `json:"display_name"`
	Description     *string   `json:"description"`
	Icon            *string   `json:"icon"`
	SystemPrompt    *string   `json:"system_prompt"`
	Tools           *[]string `json:"tools"`
	Model           *string   `json:"model"`
	API             *string   `json:"api"`
	Temperature     *float32  `json:"temperature"`
	ReasoningEffort *string   `json:"reasoning_effort"`
	Enabled         *bool     `json:"enabled"`
	SortOrder       *int      `json:"sort_order"`
}

// Register mounts the agent management routes on the (authenticated)
// group: listing ungated, detail and CRUD behind RequireAdmin. The tool
// catalog listing is admin-only too: it exists for the admin page's tool
// checkboxes (task 7.2), so plain users never need it.
func Register(g *echo.Group, svc Manager, adminRoles []string) {
	h := &handlers{svc: svc}
	admin := middleware.RequireAdmin(adminRoles)
	g.GET("/agents", h.List)
	g.GET("/agents/:id", h.Get, admin)
	g.POST("/agents/:id", h.Create, admin)
	g.PATCH("/agents/:id", h.Update, admin)
	g.DELETE("/agents/:id", h.Delete, admin)
	g.GET("/agent-tools", h.ToolCatalog, admin)
}

type handlers struct {
	svc Manager
}

// List returns every agent's listing metadata (no prompts), available to
// all authenticated users.
//
//	@Summary		List agents (metadata only)
//	@Description	Listing metadata of every agent: display name, description, icon, enabled, sort order and degraded tool names. System prompts, tools and overrides are never included.
//	@Tags			agents
//	@Success		200	{object}	response.Envelope
//	@Failure		401	{object}	response.Envelope
//	@Security		bearerAuth
//	@Router			/agents [get]
func (h *handlers) List(c *echo.Context) error {
	// Defense in depth: the listing is for authenticated users only, so
	// it stays 401 even if mounted without the bearer middleware.
	if _, ok := middleware.PrincipalFrom(c); !ok {
		return response.Fail(c, http.StatusUnauthorized, "unauthorized", "authentication required")
	}
	agents, degraded, err := h.svc.List(c.Request().Context())
	if err != nil {
		return managementError(c, err)
	}
	dtos := make([]agentSummaryDTO, 0, len(agents))
	for _, a := range agents {
		dtos = append(dtos, toSummary(a, degraded[a.ID]))
	}
	return response.OK(c, dtos)
}

// Get returns one agent's full detail (admin only).
//
//	@Summary		Get agent detail (admin)
//	@Description	Adds the system prompt, tool assignment and model overrides to the listing metadata.
//	@Tags			agents
//	@Param			id	path	string	true	"agent id (slug)"
//	@Success		200	{object}	response.Envelope
//	@Failure		403	{object}	response.Envelope
//	@Failure		404	{object}	response.Envelope
//	@Security		bearerAuth
//	@Router			/agents/{id} [get]
func (h *handlers) Get(c *echo.Context) error {
	a, degraded, err := h.svc.Get(c.Request().Context(), c.Param("id"))
	if err != nil {
		return managementError(c, err)
	}
	return response.OK(c, toDetail(a, degraded))
}

// Create inserts a new agent addressed by the slug id in the path.
//
//	@Summary		Create an agent (admin)
//	@Description	Validated create: the id is the path slug, the system prompt is required, every tool name must exist in the runtime catalog. Missing strings default empty, enabled defaults true, sort order defaults 0, nil overrides inherit the global llm default.
//	@Tags			agents
//	@Param			id		path	string				true	"agent id (lowercase slug)"
//	@Param			request	body	agentWriteRequest	true	"agent fields"
//	@Success		201		{object}	response.Envelope
//	@Failure		400		{object}	response.Envelope
//	@Failure		403		{object}	response.Envelope
//	@Failure		404		{object}	response.Envelope
//	@Security		bearerAuth
//	@Router			/agents/{id} [post]
func (h *handlers) Create(c *echo.Context) error {
	var req agentWriteRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "invalid_request", "invalid request body")
	}
	a, err := h.svc.Create(c.Request().Context(), c.Param("id"), req.createInput())
	if err != nil {
		return managementError(c, err)
	}
	return response.Created(c, toDetail(a, nil))
}

// Update applies a partial overlay onto the current row.
//
//	@Summary		Update an agent (admin)
//	@Description	Only the provided fields change; the rest keeps its current value. The id is immutable: the path is authoritative.
//	@Tags			agents
//	@Param			id		path	string				true	"agent id (slug)"
//	@Param			request	body	agentWriteRequest	true	"fields to change"
//	@Success		200		{object}	response.Envelope
//	@Failure		400		{object}	response.Envelope
//	@Failure		403		{object}	response.Envelope
//	@Failure		404		{object}	response.Envelope
//	@Security		bearerAuth
//	@Router			/agents/{id} [patch]
func (h *handlers) Update(c *echo.Context) error {
	id := c.Param("id")
	var req agentWriteRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, "invalid_request", "invalid request body")
	}
	cur, _, err := h.svc.Get(c.Request().Context(), id)
	if err != nil {
		return managementError(c, err)
	}
	a, err := h.svc.Update(c.Request().Context(), id, req.overlayInput(cur))
	if err != nil {
		return managementError(c, err)
	}
	return response.OK(c, toDetail(a, nil))
}

// Delete removes the agent — refused with 409 while conversations
// reference it.
//
//	@Summary		Delete an agent (admin)
//	@Description	Refused with 409 while conversations still reference the agent; disable it (enabled: false) instead.
//	@Tags			agents
//	@Param			id	path	string	true	"agent id (slug)"
//	@Success		200	{object}	response.Envelope
//	@Failure		403	{object}	response.Envelope
//	@Failure		404	{object}	response.Envelope
//	@Failure		409	{object}	response.Envelope
//	@Security		bearerAuth
//	@Router			/agents/{id} [delete]
func (h *handlers) Delete(c *echo.Context) error {
	if err := h.svc.Delete(c.Request().Context(), c.Param("id")); err != nil {
		return managementError(c, err)
	}
	return response.OK(c, nil)
}

// ToolCatalog returns the assignable tool names (admin only).
//
//	@Summary		List assignable tool names (admin)
//	@Description	Sorted names of the runtime tool catalog: the only tools an agent may be assigned (spec: Catalog-driven tool assignment). Feeds the admin page's tool checkboxes.
//	@Tags			agents
//	@Success		200	{object}	response.Envelope
//	@Failure		403	{object}	response.Envelope
//	@Security		bearerAuth
//	@Router			/agent-tools [get]
func (h *handlers) ToolCatalog(c *echo.Context) error {
	return response.OK(c, h.svc.ToolNames())
}

// managementError maps the use case's errors onto the standard envelope
// for every detail and write path. 404 semantics (unknown id) win over
// validation (400), then conflicts (409: taken slug, delete protection);
// anything else is an internal error whose reason is logged but never
// revealed.
func managementError(c *echo.Context, err error) error {
	var ve *agentadmin.ValidationError
	if errors.As(err, &ve) {
		return response.Fail(c, http.StatusBadRequest, "invalid_request", ve.Message)
	}
	if errors.Is(err, domain.ErrNotFound) {
		return response.Fail(c, http.StatusNotFound, "not_found", "agent not found")
	}
	if errors.Is(err, domain.ErrAgentExists) {
		return response.Fail(c, http.StatusConflict, "conflict", "agent id already exists")
	}
	if errors.Is(err, domain.ErrAgentInUse) {
		return response.Fail(c, http.StatusConflict, "conflict", "agent still has conversations; disable it with enabled: false instead of deleting")
	}
	slog.Warn("agent management operation failed", slog.String("error", err.Error()))
	return response.Fail(c, http.StatusInternalServerError, "internal", "internal error")
}

// createInput maps the POST body to a create input with the wire-level
// defaults applied; use-case-level validation stays in agentadmin.
func (r agentWriteRequest) createInput() agentadmin.Input {
	in := agentadmin.Input{Enabled: true, Tools: []string{}}
	r.overlay(&in)
	if r.Enabled != nil {
		in.Enabled = *r.Enabled
	}
	return in
}

// overlayInput starts from the current row so a PATCH changes only the
// fields the body provides.
func (r agentWriteRequest) overlayInput(cur domain.Agent) agentadmin.Input {
	in := agentadmin.Input{
		DisplayName:     cur.DisplayName,
		Description:     cur.Description,
		Icon:            cur.Icon,
		SystemPrompt:    cur.SystemPrompt,
		Tools:           cur.Tools,
		Model:           cur.Model,
		API:             cur.API,
		Temperature:     cur.Temperature,
		ReasoningEffort: cur.ReasoningEffort,
		Enabled:         cur.Enabled,
		SortOrder:       cur.SortOrder,
	}
	r.overlay(&in)
	return in
}

// overlay overwrites with the fields the body provides.
func (r agentWriteRequest) overlay(in *agentadmin.Input) {
	if r.DisplayName != nil {
		in.DisplayName = *r.DisplayName
	}
	if r.Description != nil {
		in.Description = *r.Description
	}
	if r.Icon != nil {
		in.Icon = *r.Icon
	}
	if r.SystemPrompt != nil {
		in.SystemPrompt = *r.SystemPrompt
	}
	if r.Tools != nil {
		tools := make([]string, len(*r.Tools))
		copy(tools, *r.Tools)
		in.Tools = tools
	}
	if r.Model != nil {
		in.Model = r.Model
	}
	if r.API != nil {
		in.API = r.API
	}
	if r.Temperature != nil {
		in.Temperature = r.Temperature
	}
	if r.ReasoningEffort != nil {
		in.ReasoningEffort = r.ReasoningEffort
	}
	if r.Enabled != nil {
		in.Enabled = *r.Enabled
	}
	if r.SortOrder != nil {
		in.SortOrder = *r.SortOrder
	}
}

func toSummary(a domain.Agent, degraded []string) agentSummaryDTO {
	return agentSummaryDTO{
		ID:            a.ID,
		DisplayName:   a.DisplayName,
		Description:   a.Description,
		Icon:          a.Icon,
		Enabled:       a.Enabled,
		SortOrder:     a.SortOrder,
		DegradedTools: nonNil(degraded),
	}
}

func toDetail(a domain.Agent, degraded []string) agentDetailDTO {
	return agentDetailDTO{
		agentSummaryDTO: toSummary(a, degraded),
		SystemPrompt:    a.SystemPrompt,
		Tools:           nonNil(a.Tools),
		Model:           a.Model,
		API:             a.API,
		Temperature:     a.Temperature,
		ReasoningEffort: a.ReasoningEffort,
	}
}

// nonNil normalizes nil slices to empty arrays so the wire shape is
// stable (absent degradation is [], not null).
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
