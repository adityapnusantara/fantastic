// Package agentregistry holds one constructed chat service per agent.
//
// The registry is the read side of the live-rebuild design (design D4):
// bootstrap builds the tool catalog once, then builds every agent through
// a Factory and Load( )s the results; management writes update the DB row
// and Rebuild just that agent, swapping the map entry under the write
// lock. Handlers grab the *chat.Service per request, so an in-flight
// streaming turn keeps its old service pointer and finishes on the
// definition it started with — no cancellation, no torn streams.
//
// Building an agent (tool resolution, model overrides against the global
// llm block, chat service construction) is deliberately NOT this
// package's job: it is the injected Factory seam, implemented by
// bootstrap.
package agentregistry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	chatusecase "github.com/adityapratamanusantara/fantastic/internal/application/usecases/chat"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// BuildResult is one agent's constructed pieces: the runnable chat
// service and the names of assigned tools that could not be resolved
// (design D5 — assigned-but-missing tools degrade the agent instead of
// failing it).
type BuildResult struct {
	Service       *chatusecase.Service
	DegradedTools []string
}

// Factory builds one agent's chat service from its row. Bootstrap
// implements it: resolve the agent's tool names against the catalog,
// resolve the model overrides (Model/API/Temperature/ReasoningEffort,
// nil = global llm default), and construct the service.
type Factory func(domain.Agent) (BuildResult, error)

// LoadError carries the per-agent failures of a Load. The batch is never
// aborted: Errors maps the failing agent's ID to its factory error, and
// every non-failing agent is registered normally. Unwrap yields the
// individual errors so errors.Is/As reach their causes.
type LoadError struct {
	Errors map[string]error
}

func (e *LoadError) Error() string {
	ids := make([]string, 0, len(e.Errors))
	for id := range e.Errors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return fmt.Sprintf("failed to build %d agent(s): %v", len(ids), ids)
}

// Unwrap exposes the individual per-agent errors to errors.Is/As.
func (e *LoadError) Unwrap() []error {
	out := make([]error, 0, len(e.Errors))
	for _, id := range sortedKeys(e.Errors) {
		out = append(out, fmt.Errorf("agent %q: %w", id, e.Errors[id]))
	}
	return out
}

func sortedKeys(m map[string]error) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Registry is the RWMutex-guarded map of agent ID to constructed chat
// service. Get/Degraded/IDs take the read lock; Load/Rebuild/Remove take
// the write lock. A rebuild replaces the map entry, never the *chat.Service
// already handed out, so streaming turns finish on their old definition.
type Registry struct {
	mu      sync.RWMutex
	factory Factory
	entries map[string]BuildResult
}

// New creates an empty registry over the given factory.
func New(factory Factory) *Registry {
	return &Registry{
		factory: factory,
		entries: make(map[string]BuildResult),
	}
}

// Load builds each agent through the factory and registers the results.
// A failing agent does not abort the batch: its error is collected into
// the returned LoadError and the agent is simply absent (the caller logs
// the per-agent errors). Successful agents are upserted; agents already
// registered but absent from this list are left untouched.
func (r *Registry) Load(_ context.Context, agents []domain.Agent) error {
	failures := make(map[string]error)
	built := make([]builtEntry, 0, len(agents))
	for _, a := range agents {
		res, err := r.build(a)
		if err != nil {
			failures[a.ID] = err
			continue
		}
		built = append(built, builtEntry{id: a.ID, res: res})
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range built {
		r.entries[b.id] = b.res
	}
	if len(failures) > 0 {
		return &LoadError{Errors: failures}
	}
	return nil
}

// Get returns the agent's current service.
func (r *Registry) Get(id string) (*chatusecase.Service, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	if !ok || e.Service == nil {
		return nil, false
	}
	return e.Service, true
}

// Degraded returns a copy of the agent's unresolved tool names (nil when
// the agent is unknown or its build degraded nothing). Mutating the
// result does not affect the registry.
func (r *Registry) Degraded(id string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[id]
	if !ok {
		return nil
	}
	if len(e.DegradedTools) == 0 {
		return nil
	}
	out := make([]string, len(e.DegradedTools))
	copy(out, e.DegradedTools)
	return out
}

// Rebuild builds the agent afresh through the factory and swaps the map
// entry under the write lock. On factory error the previous entry is
// left untouched (the agent keeps running on its old definition) and the
// error is returned. In-flight holders of the old *chat.Service are
// unaffected: the swap replaces the map entry, not the handed-out
// pointers (design D4).
func (r *Registry) Rebuild(a domain.Agent) (BuildResult, error) {
	res, err := r.build(a)
	if err != nil {
		return BuildResult{}, err
	}
	r.mu.Lock()
	r.entries[a.ID] = res
	r.mu.Unlock()
	return res, nil
}

// Remove drops the agent from the registry, including its degraded
// tool list.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	delete(r.entries, id)
	r.mu.Unlock()
}

// IDs returns a sorted snapshot of the registered agent IDs.
func (r *Registry) IDs() []string {
	r.mu.RLock()
	ids := make([]string, 0, len(r.entries))
	for id := range r.entries {
		ids = append(ids, id)
	}
	r.mu.RUnlock()
	sort.Strings(ids)
	return ids
}

func (r *Registry) build(a domain.Agent) (BuildResult, error) {
	if r.factory == nil {
		return BuildResult{}, errors.New("agentregistry: no factory configured")
	}
	return r.factory(a)
}

type builtEntry struct {
	id  string
	res BuildResult
}
