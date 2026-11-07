package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// AgentStore is the pgx-backed driven.AgentStore.
type AgentStore struct {
	pool *pgxpool.Pool
}

// NewAgentStore builds an AgentStore over the given pool.
func NewAgentStore(pool *pgxpool.Pool) *AgentStore {
	return &AgentStore{pool: pool}
}

// compile-time port conformance check.
var _ driven.AgentStore = (*AgentStore)(nil)

// Create inserts a new agent row; timestamps are assigned by the
// database so all rows share one clock. A duplicate id surfaces as
// domain.ErrAgentExists so the HTTP layer can answer 409 instead of
// mistaking a taken slug for an internal error.
func (s *AgentStore) Create(ctx context.Context, agent *domain.Agent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agents (id, display_name, description, icon, system_prompt,
		                   model, api, temperature, reasoning_effort, tools, enabled, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		agent.ID, agent.DisplayName, agent.Description, agent.Icon, agent.SystemPrompt,
		agent.Model, agent.API, agent.Temperature, agent.ReasoningEffort, toolsOrEmpty(agent.Tools), agent.Enabled, agent.SortOrder,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcodeUniqueViolation {
			// both the domain sentinel and the driver error stay in the
			// chain (multi-%w): callers match on ErrAgentExists,
			// diagnostics on PgError
			return fmt.Errorf("insert agent %s: %w: %w", agent.ID, domain.ErrAgentExists, err)
		}
		return fmt.Errorf("insert agent: %w", err)
	}
	return nil
}

// pgerrcodeUniqueViolation is the SQLSTATE for a constraint violation of
// type unique (agents_pkey on the slug id).
const pgerrcodeUniqueViolation = "23505"

// CreateIfAbsent inserts the agent unless its id already exists; an
// existing row is left untouched (config seeding: DB-edited values win).
// It reports whether the row was newly inserted.
func (s *AgentStore) CreateIfAbsent(ctx context.Context, agent *domain.Agent) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO agents (id, display_name, description, icon, system_prompt,
		                   model, api, temperature, reasoning_effort, tools, enabled, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO NOTHING`,
		agent.ID, agent.DisplayName, agent.Description, agent.Icon, agent.SystemPrompt,
		agent.Model, agent.API, agent.Temperature, agent.ReasoningEffort, toolsOrEmpty(agent.Tools), agent.Enabled, agent.SortOrder,
	)
	if err != nil {
		return false, fmt.Errorf("insert agent if absent: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SeedAgents inserts the agents declared in the config `agents:` block
// (design D8: declarative seeding) with INSERT ... ON CONFLICT (id) DO
// NOTHING: missing rows are created, existing rows are left untouched so
// DB-edited values always win, and a hand-emptied table reseeds on the
// next boot. It returns how many rows were newly created and logs each
// creation at Info (skips are Debug). Seeds are applied in id order so
// the log is deterministic.
func (s *AgentStore) SeedAgents(ctx context.Context, seeds config.AgentSeeds, log *slog.Logger) (int, error) {
	seeded := 0
	for _, id := range slices.Sorted(maps.Keys(seeds)) {
		inserted, err := s.CreateIfAbsent(ctx, agentFromSeed(id, seeds[id]))
		if err != nil {
			return seeded, fmt.Errorf("seed agent: %w", err)
		}
		if inserted {
			seeded++
			log.Info("agent seeded from config", slog.String("agent_id", id))
		} else {
			log.Debug("agent seed skipped: row already exists", slog.String("agent_id", id))
		}
	}
	return seeded, nil
}

// agentFromSeed maps one config `agents:` entry to a domain.Agent; the
// map key is the id. Empty override values become nil columns so the row
// inherits the global llm defaults (mirroring the config's "leave 0 to
// omit" rule). An omitted enabled key defaults to true (applyDefaults
// normally sets the pointer; programmatic seeds get the same behavior).
func agentFromSeed(id string, a config.AgentConfig) *domain.Agent {
	enabled := true
	if a.Enabled != nil {
		enabled = *a.Enabled
	}
	return &domain.Agent{
		ID:              id,
		DisplayName:     a.DisplayName,
		Description:     a.Description,
		Icon:            a.Icon,
		SystemPrompt:    a.SystemPrompt,
		Model:           optionalString(a.Model),
		API:             optionalString(a.API),
		Temperature:     optionalFloat32(a.Temperature),
		ReasoningEffort: optionalString(a.ReasoningEffort),
		Tools:           a.Tools,
		Enabled:         enabled,
		SortOrder:       a.SortOrder,
	}
}

// optionalString maps an empty override to nil (inherit the default).
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// optionalFloat32 maps a zero override to nil (inherit the default).
func optionalFloat32(f float32) *float32 {
	if f == 0 {
		return nil
	}
	return &f
}

// Get returns the agent with the given id.
func (s *AgentStore) Get(ctx context.Context, id string) (*domain.Agent, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, display_name, description, icon, system_prompt,
		       model, api, temperature, reasoning_effort, tools, enabled, sort_order,
		       created_at, updated_at
		FROM agents WHERE id = $1`, id)
	return scanAgent(row)
}

// List returns all agents ordered by sort order, then id for stability.
func (s *AgentStore) List(ctx context.Context) ([]domain.Agent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, display_name, description, icon, system_prompt,
		       model, api, temperature, reasoning_effort, tools, enabled, sort_order,
		       created_at, updated_at
		FROM agents
		ORDER BY sort_order ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var out []domain.Agent
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *agent)
	}
	return out, rows.Err()
}

// Update replaces the full row (except the immutable id) and refreshes
// updated_at server-side.
func (s *AgentStore) Update(ctx context.Context, agent *domain.Agent) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agents SET
			display_name = $2, description = $3, icon = $4, system_prompt = $5,
			model = $6, api = $7, temperature = $8, reasoning_effort = $9,
			tools = $10, enabled = $11, sort_order = $12, updated_at = now()
		WHERE id = $1`,
		agent.ID, agent.DisplayName, agent.Description, agent.Icon, agent.SystemPrompt,
		agent.Model, agent.API, agent.Temperature, agent.ReasoningEffort, toolsOrEmpty(agent.Tools), agent.Enabled, agent.SortOrder,
	)
	if err != nil {
		return fmt.Errorf("update agent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SetEnabled toggles the agent's enabled flag in place.
func (s *AgentStore) SetEnabled(ctx context.Context, id string, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agents SET enabled = $2, updated_at = now()
		WHERE id = $1`, id, enabled)
	if err != nil {
		return fmt.Errorf("set agent enabled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Delete removes the agent row; conversations referencing it keep it
// alive at the schema level (ON DELETE RESTRICT).
func (s *AgentStore) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// toolsOrEmpty keeps the NOT NULL tools column happy: a nil Go slice
// must not become SQL NULL.
func toolsOrEmpty(tools []string) []string {
	if tools == nil {
		return []string{}
	}
	return tools
}

// scanAgent scans one agent row.
func scanAgent(row pgx.Row) (*domain.Agent, error) {
	var a domain.Agent
	err := row.Scan(&a.ID, &a.DisplayName, &a.Description, &a.Icon, &a.SystemPrompt,
		&a.Model, &a.API, &a.Temperature, &a.ReasoningEffort, &a.Tools, &a.Enabled, &a.SortOrder,
		&a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan agent: %w", err)
	}
	return &a, nil
}
