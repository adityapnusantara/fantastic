// Package bootstrap wires configuration, infrastructure, and HTTP routes
// into a running server.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/adityapratamanusantara/fantastic/docs"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/components/model"
	"github.com/openai/openai-go/v3/responses"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/auth/casdoor"
	authhttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/auth"
	chathttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/chat"
	convhttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/conversation"
	demohttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/demo"
	healthhttp "github.com/adityapratamanusantara/fantastic/internal/adapters/http/health"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/middleware"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/http/response"
	mcpadapter "github.com/adityapratamanusantara/fantastic/internal/adapters/mcp"
	"github.com/adityapratamanusantara/fantastic/internal/adapters/repositories/postgres"
	chatusecase "github.com/adityapratamanusantara/fantastic/internal/application/usecases/chat"
	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/conversation"
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

	// Chat: model + MCP tools + streaming usecase. Without an API key the
	// route still registers but reports the missing configuration.
	apiKey, keyErr := cfg.APIKey()
	if keyErr != nil {
		log.Warn("chat disabled: llm api key not set", slog.String("env", cfg.LLM.APIKeyEnv))
		api.POST("/chat", func(c *echo.Context) error {
			return response.Fail(c, http.StatusServiceUnavailable, "not_configured", "chat requires "+cfg.LLM.APIKeyEnv)
		})
	} else {
		agenticModel, err := llmModel(context.Background(), cfg, apiKey)
		if err != nil {
			return nil, fmt.Errorf("create chat model: %w", err)
		}
		registry := mcpadapter.NewRegistry(context.Background(), cfg.MCPSrv, log)
		chatSvc, err := chatusecase.NewService(store, agenticModel, registry.Tools(), chatusecase.Config{
			SystemPrompt:  cfg.LLM.SystemPrompt,
			HistoryWindow: cfg.Memory.HistoryWindow,
		}, log)
		if err != nil {
			return nil, fmt.Errorf("create chat service: %w", err)
		}
		api.POST("/chat", chathttp.NewHandler(chatSvc, convService, log))
	}

	s.http = &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler:           e,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s, nil
}

// llmModel builds the agent model per llm.api. "responses" (the default)
// targets OpenAI /v1/responses — required for reasoning models with
// function tools such as gpt-6.1-sol — while "chat" targets
// /v1/chat/completions for classic models on gateways without a responses
// endpoint. Both come from eino-ext agenticopenai and speak
// *schema.AgenticMessage, so the usecase layer is identical.
func llmModel(ctx context.Context, cfg *config.Config, apiKey string) (model.AgenticModel, error) {
	switch cfg.LLM.API {
	case "", "responses":
		mc := &agenticopenai.ResponsesConfig{
			APIKey:  apiKey,
			BaseURL: cfg.LLM.BaseURL,
			Model:   cfg.LLM.Model,
		}
		if cfg.LLM.Temperature != 0 {
			t := cfg.LLM.Temperature
			mc.Temperature = &t
		}
		if cfg.LLM.MaxTokens > 0 {
			mt := cfg.LLM.MaxTokens
			mc.MaxTokens = &mt
		}
		if cfg.LLM.ReasoningEffort != "" {
			mc.Reasoning = &responses.ReasoningParam{
				Effort: responses.ReasoningEffort(cfg.LLM.ReasoningEffort),
			}
		}
		return agenticopenai.NewResponsesModel(ctx, mc)
	case "chat":
		mc := &agenticopenai.ChatConfig{
			APIKey:  apiKey,
			BaseURL: cfg.LLM.BaseURL,
			Model:   cfg.LLM.Model,
		}
		if cfg.LLM.Temperature != 0 {
			t := cfg.LLM.Temperature
			mc.Temperature = &t
		}
		return agenticopenai.NewChatModel(ctx, mc)
	default:
		return nil, fmt.Errorf("llm.api must be \"responses\" or \"chat\", got %q", cfg.LLM.API)
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
