package driven

import (
	"context"

	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// AgentStore persists chat agents. Agent IDs are slugs; timestamps are
// assigned by the database so all rows share one clock.
type AgentStore interface {
	// List returns all agents ordered by sort order.
	List(ctx context.Context) ([]domain.Agent, error)

	// Get returns the agent with the given id; domain.ErrNotFound when
	// absent.
	Get(ctx context.Context, id string) (*domain.Agent, error)

	// Create inserts a new agent row; a duplicate id wraps the database
	// error.
	Create(ctx context.Context, agent *domain.Agent) error

	// CreateIfAbsent inserts the agent unless its id already exists
	// (INSERT ... ON CONFLICT DO NOTHING); an existing row is left
	// untouched and no error is returned. It reports whether the row
	// was newly inserted (config seeding logs creations, skips the
	// rest).
	CreateIfAbsent(ctx context.Context, agent *domain.Agent) (inserted bool, err error)

	// Update replaces the full row (except the immutable id) for the
	// given agent; domain.ErrNotFound when absent.
	Update(ctx context.Context, agent *domain.Agent) error

	// SetEnabled toggles the agent's enabled flag; domain.ErrNotFound
	// when absent.
	SetEnabled(ctx context.Context, id string, enabled bool) error

	// Delete removes the agent; domain.ErrNotFound when absent.
	Delete(ctx context.Context, id string) error
}
