package agentregistry_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/adityapratamanusantara/fantastic/internal/application/agentregistry"
	chatusecase "github.com/adityapratamanusantara/fantastic/internal/application/usecases/chat"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// fakeFactory scripts per-agent build outcomes: which agents produce a
// degraded tool list and which fail. Every call is recorded.
type fakeFactory struct {
	mu       sync.Mutex
	calls    []string
	degraded map[string][]string
	errs     map[string]error
}

func (f *fakeFactory) Build(a domain.Agent) (agentregistry.BuildResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, a.ID)
	f.mu.Unlock()
	if err := f.errs[a.ID]; err != nil {
		return agentregistry.BuildResult{}, err
	}
	return agentregistry.BuildResult{
		Service:       newTestService(a.ID),
		DegradedTools: f.degraded[a.ID],
	}, nil
}

func (f *fakeFactory) callCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == id {
			n++
		}
	}
	return n
}

func agent(id string) domain.Agent {
	return domain.Agent{ID: id, DisplayName: id, SystemPrompt: "prompt for " + id, Enabled: true}
}

// stubModel satisfies model.AgenticModel for service construction only;
// registry tests never run a turn.
type stubModel struct{}

func (stubModel) Generate(context.Context, []*schema.AgenticMessage, ...model.Option) (*schema.AgenticMessage, error) {
	return nil, errors.New("Generate not scripted in registry tests")
}

func (stubModel) Stream(context.Context, []*schema.AgenticMessage, ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return nil, errors.New("Stream not scripted in registry tests")
}

// newTestService builds a distinct real *chatusecase.Service per call —
// pointer identity is what the rebuild assertions rely on. The store is
// never touched at construction time.
func newTestService(agentID string) *chatusecase.Service {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := chatusecase.NewService(nil, stubModel{}, nil, chatusecase.Config{
		AgentName:        agentID,
		AgentDescription: "test agent " + agentID,
		SystemPrompt:     "prompt for " + agentID,
	}, log)
	if err != nil {
		panic("newTestService: " + err.Error())
	}
	return svc
}

func TestLoadBuildsAllAgents(t *testing.T) {
	f := &fakeFactory{}
	reg := agentregistry.New(f.Build)

	agents := []domain.Agent{agent("alpha"), agent("beta"), agent("gamma")}
	if err := reg.Load(context.Background(), agents); err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}

	for _, id := range []string{"alpha", "beta", "gamma"} {
		svc, ok := reg.Get(id)
		if !ok || svc == nil {
			t.Fatalf("Get(%q): want service, got none", id)
		}
		if f.callCount(id) != 1 {
			t.Errorf("factory called %d times for %q, want 1", f.callCount(id), id)
		}
	}
	if got, want := reg.IDs(), []string{"alpha", "beta", "gamma"}; len(got) != len(want) {
		t.Fatalf("IDs: got %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("IDs: got %v, want %v", got, want)
			}
		}
	}
}

func TestLoadCollectsPerAgentErrors(t *testing.T) {
	sentinel := errors.New("tool catalog unreachable")
	f := &fakeFactory{errs: map[string]error{"broken": sentinel}}
	reg := agentregistry.New(f.Build)

	err := reg.Load(context.Background(), []domain.Agent{agent("alpha"), agent("broken"), agent("beta")})
	if err == nil {
		t.Fatal("Load: want error for the failing agent, got nil")
	}
	var le *agentregistry.LoadError
	if !errors.As(err, &le) {
		t.Fatalf("Load: want *LoadError, got %T", err)
	}
	if got := le.Errors["broken"]; !errors.Is(got, sentinel) {
		t.Fatalf("LoadError.Errors[broken] = %v, want sentinel %v", got, sentinel)
	}
	if len(le.Errors) != 1 {
		t.Fatalf("LoadError.Errors has %d entries, want 1", len(le.Errors))
	}

	// The batch is never aborted: the healthy agents are registered.
	for _, id := range []string{"alpha", "beta"} {
		if _, ok := reg.Get(id); !ok {
			t.Errorf("Get(%q): want service after partial Load failure", id)
		}
	}
	if _, ok := reg.Get("broken"); ok {
		t.Error("Get(broken): want miss for the failing agent")
	}
	if got := reg.IDs(); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("IDs = %v, want [alpha beta]", got)
	}
}

func TestRebuildSwapsInstance(t *testing.T) {
	f := &fakeFactory{degraded: map[string][]string{"alpha": {"mcp.search"}}}
	reg := agentregistry.New(f.Build)

	a := agent("alpha")
	if err := reg.Load(context.Background(), []domain.Agent{a}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	before, ok := reg.Get("alpha")
	if !ok {
		t.Fatal("Get(alpha): want service after Load")
	}
	if got, want := reg.Degraded("alpha"), []string{"mcp.search"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("Degraded(alpha) = %v, want [mcp.search]", got)
	}

	res, err := reg.Rebuild(a)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	after, ok := reg.Get("alpha")
	if !ok {
		t.Fatal("Get(alpha): want service after Rebuild")
	}
	if after != res.Service {
		t.Error("Get(alpha) after Rebuild: want the rebuilt instance")
	}
	if before == after {
		t.Error("Rebuild: Get returned the same instance, want a distinct pointer")
	}
	if f.callCount("alpha") != 2 {
		t.Errorf("factory called %d times for alpha, want 2 (load + rebuild)", f.callCount("alpha"))
	}
	if got, want := reg.Degraded("alpha"), []string{"mcp.search"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("Degraded(alpha) after rebuild = %v, want [mcp.search]", got)
	}
}

func TestRebuildFailureKeepsPrevious(t *testing.T) {
	f := &fakeFactory{
		degraded: map[string][]string{"alpha": {"mcp.search"}},
		errs:     map[string]error{},
	}
	reg := agentregistry.New(f.Build)

	a := agent("alpha")
	if err := reg.Load(context.Background(), []domain.Agent{a}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	before, ok := reg.Get("alpha")
	if !ok {
		t.Fatal("Get(alpha): want service after Load")
	}

	boom := errors.New("model construction failed")
	f.mu.Lock()
	f.errs["alpha"] = boom
	f.mu.Unlock()

	if _, err := reg.Rebuild(a); !errors.Is(err, boom) {
		t.Fatalf("Rebuild: want boom, got %v", err)
	}
	after, ok := reg.Get("alpha")
	if !ok || after != before {
		t.Error("failed Rebuild must leave the previous service in place")
	}
	if got, want := reg.Degraded("alpha"), []string{"mcp.search"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("Degraded(alpha) after failed rebuild = %v, want [mcp.search]", got)
	}
}

func TestRebuildWithNoDegradedClearsList(t *testing.T) {
	f := &fakeFactory{degraded: map[string][]string{"alpha": {"mcp.search", "mcp.weather"}}}
	reg := agentregistry.New(f.Build)

	a := agent("alpha")
	if err := reg.Load(context.Background(), []domain.Agent{a}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := reg.Degraded("alpha"); len(got) != 2 {
		t.Fatalf("Degraded(alpha) = %v, want 2 entries", got)
	}

	f.mu.Lock()
	delete(f.degraded, "alpha")
	f.mu.Unlock()

	if _, err := reg.Rebuild(a); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if got := reg.Degraded("alpha"); got != nil {
		t.Errorf("Degraded(alpha) after clean rebuild = %v, want nil", got)
	}
}

func TestRemoveMakesGetMiss(t *testing.T) {
	f := &fakeFactory{degraded: map[string][]string{"alpha": {"mcp.search"}}}
	reg := agentregistry.New(f.Build)

	if err := reg.Load(context.Background(), []domain.Agent{agent("alpha"), agent("beta")}); err != nil {
		t.Fatalf("Load: %v", err)
	}

	reg.Remove("alpha")
	if _, ok := reg.Get("alpha"); ok {
		t.Error("Get(alpha): want miss after Remove")
	}
	if got := reg.Degraded("alpha"); got != nil {
		t.Errorf("Degraded(alpha) after Remove = %v, want nil", got)
	}
	if got, want := reg.IDs(), []string{"beta"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("IDs = %v, want [beta]", got)
	}

	// Removing an unknown id is a no-op.
	reg.Remove("nonexistent")
}

func TestDegradedReturnsCopy(t *testing.T) {
	f := &fakeFactory{degraded: map[string][]string{"alpha": {"mcp.search"}}}
	reg := agentregistry.New(f.Build)

	if err := reg.Load(context.Background(), []domain.Agent{agent("alpha")}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := reg.Degraded("alpha")
	got[0] = "mutated"
	if want := []string{"mcp.search"}; reg.Degraded("alpha")[0] != want[0] {
		t.Errorf("Degraded(alpha) is not a defensive copy: %v", reg.Degraded("alpha"))
	}
	if got := reg.Degraded("missing"); got != nil {
		t.Errorf("Degraded(missing) = %v, want nil", got)
	}
}

// TestConcurrentGetDuringRebuildAndRemove exercises the read/write split
// under the race detector: one writer rebuilds and removes agents while
// parallel readers grab services, degraded lists and ID snapshots.
func TestConcurrentGetDuringRebuildAndRemove(t *testing.T) {
	f := &fakeFactory{}
	reg := agentregistry.New(f.Build)

	agents := []domain.Agent{agent("alpha"), agent("beta"), agent("gamma")}
	if err := reg.Load(context.Background(), agents); err != nil {
		t.Fatalf("Load: %v", err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				for _, id := range []string{"alpha", "beta", "gamma", "removed"} {
					_, _ = reg.Get(id)
					_ = reg.Degraded(id)
				}
				_ = reg.IDs()
			}
		}()
	}

	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		for range 50 {
			if _, err := reg.Rebuild(agent("alpha")); err != nil {
				t.Errorf("Rebuild(alpha): %v", err)
			}
			if _, err := reg.Rebuild(agent("beta")); err != nil {
				t.Errorf("Rebuild(beta): %v", err)
			}
			reg.Remove("gamma")
			if _, err := reg.Rebuild(agent("gamma")); err != nil {
				t.Errorf("Rebuild(gamma): %v", err)
			}
			reg.Remove("removed")
			_ = reg.Load(context.Background(), agents)
		}
	}()

	// The writer runs a bounded number of rounds; once it is done, stop
	// the readers and wait for them to drain.
	writerWG.Wait()
	close(done)
	wg.Wait()
}
