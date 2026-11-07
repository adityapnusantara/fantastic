// Package bootstrap wires configuration, infrastructure, and HTTP routes
// into a running server.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/adityapratamanusantara/fantastic/docs"

	"github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/cloudwego/eino/components/tool"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/auth/casdoor"
	adminhttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/admin"
	agenthttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/agent"
	authhttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/auth"
	chathttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/chat"
	convhttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/conversation"
	demohttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/demo"
	healthhttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/health"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/middleware"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/response"
	mcpadapter "github.com/adityapratamanusantara/fantastic/internal/adapters/mcp"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/repositories/postgres"
	faqtool "github.com/adityapratamanusantara/fantastic/internal/adapters/tools/faq"
	"github.com/adityapratamanusantara/fantastic/internal/application/agentregistry"
	"github.com/adityapratamanusantara/fantastic/internal/application/ports/driven"
	agentadmin "github.com/adityapratamanusantara/fantastic/internal/application/usecases/agentadmin"
	chatusecase "github.com/adityapratamanusantara/fantastic/internal/application/usecases/chat"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/conversation"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// Server owns the HTTP server and its dependencies.
type Server struct {
	cfg  *config.Config
	log  *slog.Logger
	pool *pgxpool.Pool
	http *http.Server
}

// New builds the server: opens the database pool when a URL is configured
// and registers routes. Call Start to run it.
func New(cfg *config.Config, log *slog.Logger) (*Server, error) {
	s := &Server{cfg: cfg, log: log}

	if cfg.Database.URL != "" {
		poolCfg, err := pgxpool.ParseConfig(cfg.Database.URL)
		if err != nil {
			return nil, fmt.Errorf("parse database url: %w", err)
		}
		pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
		if err != nil {
			return nil, fmt.Errorf("open database pool: %w", err)
		}
		s.pool = pool
	}

	e := echo.New()
	e.GET("/health", healthhttp.New())

	// Generated OpenAPI document (see docs/ and main.go annotations).
	e.GET("/swagger/doc.json", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, docs.SwaggerInfo.ReadDoc())
	})

	// Every /api/v1 route requires a verified Casdoor bearer token.
	if cfg.Auth.Issuer == "" || cfg.Auth.ClientID == "" || cfg.Auth.Organization == "" || cfg.Auth.JWKSURL == "" {
		return nil, errors.New("auth must be configured: auth.issuer, auth.client_id, auth.organization, auth.jwks_url")
	}
	verifier := casdoor.NewVerifier(casdoor.Config{
		Issuer:       cfg.Auth.Issuer,
		Audience:     cfg.Auth.ClientID,
		Organization: cfg.Auth.Organization,
		JWKSURL:      cfg.Auth.JWKSURL,
	})

	// Dev token endpoint sits outside the protected group (it issues tokens).
	if err := authhttp.Register(e, cfg); err != nil {
		return nil, err
	}

	// Static demo page (login + SSE chat + conversation list).
	demohttp.Register(e)

	// Static agent-admin page (login + agent CRUD UI); authorization is
	// enforced by the management API behind it, not by the page.
	adminhttp.Register(e)

	api := e.Group("/api/v1")
	api.Use(middleware.BearerAuth(verifier, log))
	api.GET("/whoami", authhttp.Whoami)

	// Conversation store + services.
	if s.pool == nil {
		return nil, errors.New("database.url is required (chat memory is postgres-backed)")
	}
	store := postgres.NewStore(s.pool)
	convService := conversation.NewService(store)
	convhttp.Register(api, convService)

	// Declarative agent seeding (design D8): insert the config-declared
	// agents with ON CONFLICT (id) DO NOTHING — missing rows are created,
	// existing rows untouched (DB edits always win). Runs before anything
	// reads the agents table and whether or not chat is enabled.
	// Degrade-not-fail: seeding trouble only warns; the agents table and
	// the management API keep whatever rows already exist.
	agentStore := postgres.NewAgentStore(s.pool)
	if _, err := agentStore.SeedAgents(context.Background(), cfg.Agents, log); err != nil {
		log.Warn("agent seeding degraded; continuing with existing rows", slog.String("error", err.Error()))
	}

	// Agent runtime (design D4): the tool catalog is built exactly once
	// per boot — MCP tools under "<server>.<tool>" names, the local FAQ
	// search tool (design D10: the first local tool in the repo; the
	// registry stays MCP-only) under its own name "search_faq", absent
	// with only a warning when the capability cannot come up — and every
	// enabled agent row is built into the registry through the factory
	// below. The management API (listing open to all authenticated users,
	// detail and CRUD behind RequireAdmin) is mounted whether or not chat
	// is configured: agents still list and manage without an LLM key;
	// only chat turns are refused.
	mcpRegistry := mcpadapter.NewRegistry(context.Background(), cfg.MCPSrv, log)
	catalog := buildToolCatalog(context.Background(), mcpRegistry, cfg, s.pool, log)
	apiKey, keyErr := cfg.APIKey()
	agentRegistry := agentregistry.New(newAgentFactory(store, catalog, cfg, apiKey, log))
	loadAgents(context.Background(), agentRegistry, agentStore, log)

	adminSvc, err := agentadmin.NewService(agentStore, agentRegistry, catalog, store)
	if err != nil {
		return nil, fmt.Errorf("create agent admin service: %w", err)
	}
	agenthttp.Register(api, adminSvc, cfg.Auth.AdminRoles)

	// Chat: without an API key the route still registers but reports the
	// missing configuration (agents keep listing above). With one, each
	// request resolves the path agent through the registry (design D4) —
	// unknown or disabled agents are left unresolved and answer 404.
	if keyErr != nil {
		log.Warn("chat disabled: llm api key not set", slog.String("env", cfg.LLM.APIKeyEnv))
		api.POST("/agents/:agent/chat", func(c *echo.Context) error {
			return response.Fail(c, http.StatusServiceUnavailable, "not_configured", "chat requires "+cfg.LLM.APIKeyEnv)
		})
	} else {
		resolver := chathttp.ResolverFunc(func(agentID string) (chathttp.Streamer, bool) {
			return agentRegistry.Get(agentID)
		})
		api.POST("/agents/:agent/chat", chathttp.NewHandler(resolver, convService, log))
	}

	s.http = &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler:           e,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s, nil
}

// buildToolCatalog assembles the named tool catalog: the MCP registry's
// "<server>.<tool>" entries plus the local search_faq tool when it can
// come up (degrade-not-fail: missing credentials just leave the key out).
// The catalog is built exactly once per boot (design D4): agent builds
// resolve their assigned tool names against this map (design D5) and the
// management API validates assignments against it. Agent prompts live in
// the agents-table rows — the FAQ answering rules travel with the seeded
// row prompts (migration 0005 / the config agents block), not with the
// catalog.
func buildToolCatalog(ctx context.Context, registry *mcpadapter.Registry, cfg *config.Config, pool *pgxpool.Pool, log *slog.Logger) map[string]tool.BaseTool {
	catalog := registry.Catalog()
	if faqTool := newFaqSearchTool(ctx, cfg, pool, log); faqTool != nil {
		catalog[faqtool.Name] = faqTool
	}
	return catalog
}

// newFaqSearchTool builds the local search_faq tool over the shared
// database pool and the faq.embedding config (design D10: appended at
// bootstrap beside the MCP tools). Mirroring the MCP registry's
// degrade-not-fail rule, it returns nil — chat continues without FAQ
// grounding — when the FAQ capability cannot come up; a failure at call
// time reaches the agent as a tool error instead.
func newFaqSearchTool(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, log *slog.Logger) tool.BaseTool {
	apiKey, err := cfg.FAQEmbeddingAPIKey()
	if err != nil {
		log.Warn("faq search disabled: embedding credential unavailable", slog.String("error", err.Error()))
		return nil
	}
	embedder, err := openai.NewEmbedder(ctx, &openai.EmbeddingConfig{
		APIKey:  apiKey,
		BaseURL: cfg.FAQ.Embedding.BaseURL,
		Model:   cfg.FAQ.Embedding.Model,
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		log.Warn("faq search disabled: embedder unavailable", slog.String("error", err.Error()))
		return nil
	}
	faqTool, err := faqtool.NewSearchTool(postgres.NewFaqSearchStore(pool), embedder, cfg.FAQ.Search.TopK)
	if err != nil {
		log.Warn("faq search disabled", slog.String("error", err.Error()))
		return nil
	}
	return faqTool
}

// newAgentFactory returns the agentregistry.Factory that builds one
// agent's chat service from its stored row (design D4). The agent's tool
// names resolve against the boot-wide catalog: names missing from the
// catalog become BuildResult.DegradedTools and the agent still builds,
// running on the resolvable subset (design D5: startup degrade — an
// unreachable MCP server is an infra condition, not an input bug). The
// model is built from the agent's overrides over the global llm block,
// and the service carries the agent's identity and prompt. An error
// fails only that agent's build: the registry's Load collects it and the
// agent stays out of the runtime (chat to it answers 404).
func newAgentFactory(store driven.ConversationStore, catalog map[string]tool.BaseTool, cfg *config.Config, apiKey string, log *slog.Logger) agentregistry.Factory {
	return func(a domain.Agent) (agentregistry.BuildResult, error) {
		tools := make([]tool.BaseTool, 0, len(a.Tools))
		var degraded []string
		for _, name := range a.Tools {
			if t, ok := catalog[name]; ok {
				tools = append(tools, t)
			} else {
				degraded = append(degraded, name)
			}
		}

		agenticModel, err := llmModel(context.Background(), cfg, apiKey, overridesFromAgent(a))
		if err != nil {
			return agentregistry.BuildResult{}, fmt.Errorf("build model for agent %q: %w", a.ID, err)
		}
		svc, err := chatusecase.NewService(store, agenticModel, tools, chatusecase.Config{
			AgentName:        a.ID,
			AgentDescription: a.Description,
			SystemPrompt:     a.SystemPrompt,
			HistoryWindow:    cfg.Memory.HistoryWindow,
		}, log)
		if err != nil {
			return agentregistry.BuildResult{}, fmt.Errorf("build chat service for agent %q: %w", a.ID, err)
		}
		return agentregistry.BuildResult{Service: svc, DegradedTools: degraded}, nil
	}
}

// loadAgents loads every enabled agent row from the store into the
// registry (design D4). It never fails the boot: a store error or a
// per-agent factory error is only warned and that agent stays out of the
// registry (chat to it answers 404), and agents whose tools cannot all
// be resolved are logged with the missing names (design D5 / task 3.4).
func loadAgents(ctx context.Context, reg *agentregistry.Registry, store driven.AgentStore, log *slog.Logger) {
	agents, err := store.List(ctx)
	if err != nil {
		log.Warn("agent registry load degraded: listing agents failed", slog.String("error", err.Error()))
		return
	}
	enabled := make([]domain.Agent, 0, len(agents))
	for _, a := range agents {
		if a.Enabled {
			enabled = append(enabled, a)
		}
	}
	if err := reg.Load(ctx, enabled); err != nil {
		var le *agentregistry.LoadError
		if errors.As(err, &le) {
			for _, id := range slices.Sorted(maps.Keys(le.Errors)) {
				log.Warn("agent build failed; chat to it will 404 until restart",
					slog.String("agent_id", id), slog.String("error", le.Errors[id].Error()))
			}
		} else {
			log.Warn("agent registry load degraded", slog.String("error", err.Error()))
		}
	}
	for _, a := range enabled {
		if missing := reg.Degraded(a.ID); len(missing) > 0 {
			log.Warn("agent running without tools",
				slog.String("agent_id", a.ID), slog.String("tools", strings.Join(missing, ", ")))
		}
	}
}

// Start runs the HTTP server and blocks until the process receives
// SIGINT/SIGTERM, then drains connections.
func (s *Server) Start() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http server listening", slog.Int("port", s.cfg.Server.HTTPPort))
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		s.log.Info("shutdown signal received")
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.http.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("shutdown http server: %w", err)
	}
	if s.pool != nil {
		s.pool.Close()
	}
	s.log.Info("server stopped")
	return nil
}
