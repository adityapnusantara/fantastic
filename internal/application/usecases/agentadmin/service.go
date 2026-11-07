// Package agentadmin implements the admin-side use cases for managing
// chat agents: validated create/update (spec: Agent write validation),
// delete protection while conversations reference the agent
// (domain.ErrAgentInUse, HTTP 409), disable/enable, and a live registry
// rebuild/remove after every successful write (design D4/D5 — changes
// take effect without a restart).
//
// Agent ids are immutable slugs: they arrive as method arguments, never
// inside Input, and an update keeps the id it was addressed by. This
// package knows nothing about HTTP — the handler layer (task 5.2) maps
// ValidationError to 400, domain.ErrNotFound to 404 and
// domain.ErrAgentInUse to 409.
package agentadmin

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"

	"github.com/adityapratamanusantara/fantastic/internal/application/agentregistry"
	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// supportedAPIs are the api override values an agent row may carry
// (nil means "inherit the global llm default").
var supportedAPIs = map[string]bool{
	"responses": true,
	"chat":      true,
}

// minTemperature/maxTemperature bound the temperature override
// (nil means "inherit the global llm default").
const (
	minTemperature = float32(0)
	maxTemperature = float32(2)
)

// slugPattern is the id format: a lowercase slug. The id lives in the
// URL and is referenced by conversations.agent_id, so it is immutable
// after creation and restricted to URL-safe lowercase characters.
var slugPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// Input mirrors the management API's write body for one agent. The id is
// deliberately absent: it is a method argument, so the wire payload
// cannot rename an agent. Pointer overrides left nil inherit the global
// llm block.
type Input struct {
	DisplayName     string
	Description     string
	Icon            string
	SystemPrompt    string
	Tools           []string
	Model           *string
	API             *string
	Temperature     *float32
	ReasoningEffort *string
	Enabled         bool
	SortOrder       int
}

// ValidationError reports a rejected management write. Field names the
// offending request field (snake_case, matching the wire format) and
// Message describes the violation, naming the offending value where
// there is one (for tools, the unknown tool name). The HTTP layer maps
// it to 400 via errors.As — no string parsing.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// Service manages agent rows and keeps the running agent registry in
// step with the stored definitions.
type Service struct {
	store         driven.AgentStore
	registry      *agentregistry.Registry
	catalog       map[string]tool.BaseTool
	conversations driven.ConversationStore
}

// NewService builds the agent admin service. Every dependency must be
// non-nil: the store persists rows, the registry serves the runtime, the
// catalog decides which tool names are assignable (management writes
// assign catalog tools, never define new ones — design D5), and the
// conversation store backs the delete-protection count.
func NewService(store driven.AgentStore, registry *agentregistry.Registry, catalog map[string]tool.BaseTool, conversations driven.ConversationStore) (*Service, error) {
	switch {
	case store == nil:
		return nil, fmt.Errorf("agentadmin: nil agent store")
	case registry == nil:
		return nil, fmt.Errorf("agentadmin: nil agent registry")
	case catalog == nil:
		return nil, fmt.Errorf("agentadmin: nil tool catalog")
	case conversations == nil:
		return nil, fmt.Errorf("agentadmin: nil conversation store")
	}
	return &Service{
		store:         store,
		registry:      registry,
		catalog:       catalog,
		conversations: conversations,
	}, nil
}

// Create validates the input and inserts a new agent row, then moves it
// into the registry from the stored row — or leaves it out when created
// disabled: a disabled agent never serves chat. The id must be a
// lowercase slug and must not exist yet (the store rejects duplicates).
func (s *Service) Create(ctx context.Context, id string, in Input) (domain.Agent, error) {
	if err := validateID(id); err != nil {
		return domain.Agent{}, err
	}
	if err := s.validate(in); err != nil {
		return domain.Agent{}, err
	}

	agent := in.toAgent(id)
	if err := s.store.Create(ctx, &agent); err != nil {
		return domain.Agent{}, fmt.Errorf("create agent %q: %w", id, err)
	}
	// Return the stored row (database-assigned timestamps included).
	return s.syncRegistry(ctx, id)
}

// Update replaces the definition of the agent addressed by id and moves
// it in or out of the registry per the stored enabled flag: disabling
// through PATCH stops chat exactly like SetEnabled does. The id is
// immutable: the method argument is authoritative and Input carries no
// id, so an update can never rename an agent or move its conversations'
// references (spec: Agent id is immutable). Unknown ids pass through as
// domain.ErrNotFound. After the stored row is replaced the registry
// serves the new definition immediately; a turn already in flight
// finishes on the old one (design D4).
func (s *Service) Update(ctx context.Context, id string, in Input) (domain.Agent, error) {
	// Fail on an unknown id before validating: 404 semantics win over 400.
	if _, err := s.store.Get(ctx, id); err != nil {
		return domain.Agent{}, err
	}
	// No slug check here — the id already exists and cannot change.
	if err := s.validate(in); err != nil {
		return domain.Agent{}, err
	}

	agent := in.toAgent(id)
	if err := s.store.Update(ctx, &agent); err != nil {
		return domain.Agent{}, fmt.Errorf("update agent %q: %w", id, err)
	}
	// Return the stored row (store-side updated_at refresh included).
	return s.syncRegistry(ctx, id)
}

// syncRegistry moves the freshly stored agent in or out of the running
// registry per its enabled flag: enabled rebuilds (chat serves the new
// definition immediately, design D4), disabled removes (chat stops
// resolving it). It returns the stored row for callers that echo it.
func (s *Service) syncRegistry(ctx context.Context, id string) (domain.Agent, error) {
	stored, err := s.store.Get(ctx, id)
	if err != nil {
		return domain.Agent{}, fmt.Errorf("reload agent %q: %w", id, err)
	}
	if !stored.Enabled {
		s.registry.Remove(id)
		return *stored, nil
	}
	if _, err := s.registry.Rebuild(*stored); err != nil {
		return domain.Agent{}, fmt.Errorf("rebuild agent %q: %w", id, err)
	}
	return *stored, nil
}

// Delete removes the agent — unless conversations still reference it,
// in which case it returns an error wrapping domain.ErrAgentInUse (HTTP
// 409) and changes nothing. A clean delete also drops the agent from
// the registry. Unknown ids pass through as domain.ErrNotFound.
func (s *Service) Delete(ctx context.Context, id string) error {
	count, err := s.conversations.CountByAgent(ctx, id)
	if err != nil {
		return fmt.Errorf("count conversations for agent %q: %w", id, err)
	}
	if count > 0 {
		return fmt.Errorf("%w: agent %q still has %d conversation(s)", domain.ErrAgentInUse, id, count)
	}
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	s.registry.Remove(id)
	return nil
}

// SetEnabled toggles an agent's availability. Disabling removes it from
// the registry (chat requests stop resolving it while its row and
// conversations remain); enabling rebuilds it from the fresh row.
// Unknown ids pass through as domain.ErrNotFound.
func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool) error {
	if err := s.store.SetEnabled(ctx, id, enabled); err != nil {
		return err
	}
	if !enabled {
		s.registry.Remove(id)
		return nil
	}
	if _, err := s.rebuildStored(ctx, id); err != nil {
		return err
	}
	return nil
}

// ToolNames returns the assignable tool names: the sorted keys of the
// boot-wide runtime tool catalog the service validates assignments
// against (spec: Catalog-driven tool assignment — management operations
// assign catalog tools, never define new ones). The admin page's tool
// checkboxes are fed from this through GET /api/v1/agent-tools.
func (s *Service) ToolNames() []string {
	names := make([]string, 0, len(s.catalog))
	for name := range s.catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Get returns one agent with its degraded tool names (assigned tools the
// runtime could not resolve — design D5). Unknown ids pass through as
// domain.ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (domain.Agent, []string, error) {
	agent, err := s.store.Get(ctx, id)
	if err != nil {
		return domain.Agent{}, nil, err
	}
	return *agent, s.registry.Degraded(id), nil
}

// List returns every agent sorted by (sort_order, id) with a degraded
// tool map keyed by agent id (agents without degradation are absent
// from the map).
func (s *Service) List(ctx context.Context) ([]domain.Agent, map[string][]string, error) {
	agents, err := s.store.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list agents: %w", err)
	}

	sorted := make([]domain.Agent, len(agents))
	copy(sorted, agents)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].SortOrder != sorted[j].SortOrder {
			return sorted[i].SortOrder < sorted[j].SortOrder
		}
		return sorted[i].ID < sorted[j].ID
	})

	degraded := make(map[string][]string, len(sorted))
	for _, a := range sorted {
		if d := s.registry.Degraded(a.ID); len(d) > 0 {
			degraded[a.ID] = d
		}
	}
	return sorted, degraded, nil
}

// rebuildStored reloads the row just written, swaps it into the registry
// (so the running definition always matches the database — the database
// owns timestamps and any store-side normalization), and returns the
// stored row. On factory error the registry keeps the previous entry and
// the error is returned; the persisted row is unaffected and a retry
// heals the registry.
func (s *Service) rebuildStored(ctx context.Context, id string) (domain.Agent, error) {
	stored, err := s.store.Get(ctx, id)
	if err != nil {
		return domain.Agent{}, fmt.Errorf("reload agent %q: %w", id, err)
	}
	if _, err := s.registry.Rebuild(*stored); err != nil {
		return domain.Agent{}, fmt.Errorf("rebuild agent %q: %w", id, err)
	}
	return *stored, nil
}

// validateID enforces the slug format on creation.
func validateID(id string) error {
	if !slugPattern.MatchString(id) {
		return &ValidationError{Field: "id", Message: fmt.Sprintf("%q must be a lowercase slug (^[a-z0-9-]+$)", id)}
	}
	return nil
}

// validate enforces the shared write rules (spec: Agent write
// validation): a required system prompt, supported api override, bounded
// temperature override, and a tool assignment that only names catalog
// tools. Every rejection names the offending field or tool.
func (s *Service) validate(in Input) error {
	if strings.TrimSpace(in.SystemPrompt) == "" {
		return &ValidationError{Field: "system_prompt", Message: "is required"}
	}
	if in.API != nil && !supportedAPIs[*in.API] {
		return &ValidationError{Field: "api", Message: fmt.Sprintf("%q is not supported (use \"responses\" or \"chat\")", *in.API)}
	}
	if in.Temperature != nil && (*in.Temperature < minTemperature || *in.Temperature > maxTemperature) {
		return &ValidationError{Field: "temperature", Message: fmt.Sprintf("%v is outside the supported range 0..2", *in.Temperature)}
	}
	for _, name := range in.Tools {
		if strings.TrimSpace(name) == "" {
			return &ValidationError{Field: "tools", Message: "tool names must be non-empty"}
		}
		if _, ok := s.catalog[name]; !ok {
			return &ValidationError{Field: "tools", Message: fmt.Sprintf("unknown tool %q (assign tools from the runtime catalog)", name)}
		}
	}
	return nil
}

// toAgent builds the row for the store from validated input. A nil tool
// list is normalized to an empty slice (no assignment), and the tools
// slice is copied so later mutation of the caller's input cannot alias
// into the stored row.
func (in Input) toAgent(id string) domain.Agent {
	tools := make([]string, len(in.Tools))
	copy(tools, in.Tools)
	return domain.Agent{
		ID:              id,
		DisplayName:     in.DisplayName,
		Description:     in.Description,
		Icon:            in.Icon,
		SystemPrompt:    in.SystemPrompt,
		Model:           in.Model,
		API:             in.API,
		Temperature:     in.Temperature,
		ReasoningEffort: in.ReasoningEffort,
		Tools:           tools,
		Enabled:         in.Enabled,
		SortOrder:       in.SortOrder,
	}
}
