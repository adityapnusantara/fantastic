// Package config loads fantastic's YAML configuration with ${VAR}
// environment interpolation for secret values.
package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Default values applied when the config file omits the field.
const (
	DefaultHTTPPort      = 5580
	DefaultBaseURL       = "https://api.openai.com/v1"
	DefaultModel         = "gpt-6.1-sol"
	DefaultAPIKeyEnv     = "OPENAI_API_KEY"
	DefaultLLMAPI        = "responses"
	DefaultHistoryWindow = 20
	DefaultMCPTransport  = "http"

	// FAQ embedding defaults (design D2: single model, owner decision
	// 2026-11-06). The full faq: section is formalized by task 4.3.
	DefaultFaqEmbeddingModel = "text-embedding-3-small"
	DefaultFaqEmbeddingDims  = 1536

	// DefaultFaqSearchTopK is the retrieval width of the search_faq tool
	// (design D7: pure vector top-k, k=5 configurable).
	DefaultFaqSearchTopK = 5

	// FAQ scraping etiquette defaults (design D6: polite crawling of
	// public linkaja.id pages only, >=1 request/second, everything cached).
	DefaultFaqScrapeRequestDelayMS = 1000
	DefaultFaqScrapeUserAgent      = "fantastic-faq-spike/0.1 (personal project; contact via repo)"
	DefaultFaqScrapeCacheDir       = "data/raw"

	// DefaultFaqCorpusPath / DefaultFaqCorpusManifestPath are the design D5
	// first-class artifacts: the committed corpus JSON and the per-chunk hash
	// manifest emitted by the index step.
	DefaultFaqCorpusPath         = "data/corpus/linkaja-faq.json"
	DefaultFaqCorpusManifestPath = "data/corpus/index-manifest.json"
)

// DefaultFaqScrapeAllowedHosts is the host allow-list (design D6: public
// linkaja.id pages only). A var because slices cannot be Go constants;
// applyDefaults copies it so callers cannot mutate the shared default.
var DefaultFaqScrapeAllowedHosts = []string{"linkaja.id", "www.linkaja.id"}

// DefaultAdminRoles is the auth.admin_roles default (design D6): the
// Casdoor roles allowed to manage agents. A var because slices cannot be
// Go constants; applyDefaults copies it so callers cannot mutate the
// shared default.
var DefaultAdminRoles = []string{"fantastic-admin"}

// Bounds for faq.search.top_k (validated on load).
const (
	MinFaqSearchTopK = 1
	MaxFaqSearchTopK = 20
)

// Bounds for an agent temperature override (validated on load). Zero
// doubles as the "unset — inherit llm.temperature" sentinel, so it is the
// inclusive minimum.
const (
	MinAgentTemperature = float32(0)
	MaxAgentTemperature = float32(2)
)

// agentIDPattern constrains agent ids (the map keys of agents:) to
// URL-safe lowercase slugs; the id lands in the chat URL path and in
// conversations.agent_id.
var agentIDPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// agentAPIValues is the enum an agent's api override may take (same
// surfaces as llm.api).
var agentAPIValues = map[string]bool{"responses": true, "chat": true}

// Config is the root configuration document.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	LLM      LLMConfig      `yaml:"llm"`
	Memory   MemoryConfig   `yaml:"memory"`
	Auth     AuthConfig     `yaml:"auth"`
	MCPSrv   MCPServers     `yaml:"mcp_servers"`
	Agents   AgentSeeds     `yaml:"agents"`
	FAQ      FaqConfig      `yaml:"faq"`
}

type ServerConfig struct {
	HTTPPort int `yaml:"http_port"`
}

type DatabaseConfig struct {
	// URL is the postgres connection string (pgvector-capable instance).
	URL string `yaml:"url"`
}

type LLMConfig struct {
	// API selects the OpenAI surface: "responses" (/v1/responses, the
	// default — required for reasoning models with function tools such as
	// gpt-6.1-sol) or "chat" (/v1/chat/completions, for classic models on
	// gateways without a responses endpoint).
	API          string  `yaml:"api"`
	BaseURL      string  `yaml:"base_url"`
	Model        string  `yaml:"model"`
	APIKeyEnv    string  `yaml:"api_key_env"`
	SystemPrompt string  `yaml:"system_prompt"`
	Temperature  float32 `yaml:"temperature"`
	MaxTokens    int     `yaml:"max_tokens"`
	// ReasoningEffort is sent as reasoning.effort on /responses when
	// non-empty (low/medium/high/xhigh); empty means the model default.
	ReasoningEffort string `yaml:"reasoning_effort"`
}

type MemoryConfig struct {
	// HistoryWindow is how many recent messages are sent to the model
	// alongside the rolling summary.
	HistoryWindow int `yaml:"history_window"`
}

type AuthConfig struct {
	Issuer          string `yaml:"issuer"`
	ClientID        string `yaml:"client_id"`
	Organization    string `yaml:"organization"`
	JWKSURL         string `yaml:"jwks_url"`
	DevTokenEnabled bool   `yaml:"dev_token_endpoint"`
	ClientSecretEnv string `yaml:"client_secret_env"`
	// AdminRoles are the Casdoor roles (from the token's `roles` claim)
	// allowed to manage agents via RequireAdmin (design D6). Defaults to
	// DefaultAdminRoles when the config omits it.
	AdminRoles []string `yaml:"admin_roles"`
}

// MCPServerConfig declares one external MCP server.
type MCPServerConfig struct {
	URL        string            `yaml:"url"`
	Transport  string            `yaml:"transport"` // "http" (streamable) or "sse"
	Headers    map[string]string `yaml:"headers"`
	AllowTools []string          `yaml:"allow_tools"`
}

// MCPServers maps a server name to its declaration.
type MCPServers map[string]MCPServerConfig

// AgentConfig declares one agent seed in the `agents:` block (design D8:
// config-declared agents are declarative — bootstrap inserts them at
// startup with ON CONFLICT (id) DO NOTHING, so DB edits always win). The
// map key is the agent id (slug): it is not a yaml field, appears in the
// chat URL and conversations.agent_id, and is immutable once created.
type AgentConfig struct {
	// DisplayName, Description and Icon are metadata shown in listings.
	DisplayName string `yaml:"display_name"`
	Description string `yaml:"description"`
	Icon        string `yaml:"icon"`
	// SystemPrompt is the agent's full system prompt (required, non-empty).
	SystemPrompt string `yaml:"system_prompt"`
	// Tools lists tool-catalog names (local tools like search_faq keep
	// their name; MCP tools are "<server>.<tool>", design D5).
	Tools []string `yaml:"tools"`
	// Model, API, Temperature and ReasoningEffort are optional per-agent
	// overrides: empty string / zero temperature inherits the global llm:
	// block (mirroring the nullable override columns of migration 0005 —
	// the same "leave 0 to omit" rule as llm.temperature).
	Model           string  `yaml:"model"`
	API             string  `yaml:"api"`
	Temperature     float32 `yaml:"temperature"`
	ReasoningEffort string  `yaml:"reasoning_effort"`
	// Enabled is a pointer so an omitted key defaults to true while an
	// explicit `enabled: false` is still honored (a plain bool cannot
	// tell "unset" from "false").
	Enabled *bool `yaml:"enabled"`
	// SortOrder orders agents in listings; default 0.
	SortOrder int `yaml:"sort_order"`
}

// AgentSeeds maps agent id -> seed declaration (the agents: block).
type AgentSeeds map[string]AgentConfig

// Validate enforces the agents: seed invariants after defaults have been
// applied: slug-form ids, non-empty system prompts, the api override
// enum, the temperature override range and non-empty tool names. Errors
// name the offending agent and field. It is also usable on
// programmatically constructed AgentSeeds values.
func (s AgentSeeds) Validate() error {
	ids := make([]string, 0, len(s))
	for id := range s {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic error order
	for _, id := range ids {
		a := s[id]
		if !agentIDPattern.MatchString(id) {
			return fmt.Errorf("agents.%s: invalid agent id (must match %s)", id, agentIDPattern)
		}
		if strings.TrimSpace(a.SystemPrompt) == "" {
			return fmt.Errorf("agents.%s.system_prompt must not be empty", id)
		}
		if a.API != "" && !agentAPIValues[a.API] {
			return fmt.Errorf("agents.%s.api must be \"responses\" or \"chat\", got %q", id, a.API)
		}
		if a.Temperature < MinAgentTemperature || a.Temperature > MaxAgentTemperature {
			return fmt.Errorf("agents.%s.temperature must be between %g and %g, got %g",
				id, MinAgentTemperature, MaxAgentTemperature, a.Temperature)
		}
		for i, tool := range a.Tools {
			if strings.TrimSpace(tool) == "" {
				return fmt.Errorf("agents.%s.tools[%d] must be a non-empty tool name", id, i)
			}
		}
	}
	return nil
}

// FaqConfig holds the FAQ RAG settings (formalized by task 4.3): the
// embedding credential block, retrieval tuning, scrape politeness and the
// corpus artifact paths. See FaqConfig.Validate for the invariants.
type FaqConfig struct {
	Embedding FaqEmbeddingConfig `yaml:"embedding"`
	// Search tunes the retrieval the search_faq tool performs (task 2.4).
	Search FaqSearchConfig `yaml:"search"`
	// Scrape holds the crawler politeness policy (design D6).
	Scrape FaqScrapeConfig `yaml:"scrape"`
	// Corpus holds the design D5 artifact paths (committed corpus JSON +
	// index manifest).
	Corpus FaqCorpusConfig `yaml:"corpus"`
}

// Validate enforces the faq: invariants after defaults have been applied.
// It is also usable on programmatically constructed FaqConfig values.
func (c *FaqConfig) Validate() error {
	if c.Search.TopK < MinFaqSearchTopK || c.Search.TopK > MaxFaqSearchTopK {
		return fmt.Errorf("faq.search.top_k must be between %d and %d, got %d",
			MinFaqSearchTopK, MaxFaqSearchTopK, c.Search.TopK)
	}
	if c.Embedding.Dims <= 0 {
		return fmt.Errorf("faq.embedding.dims must be positive, got %d", c.Embedding.Dims)
	}
	if c.Scrape.RequestDelayMS < 0 {
		return fmt.Errorf("faq.scrape.request_delay_ms must be >= 0, got %d", c.Scrape.RequestDelayMS)
	}
	return nil
}

// FaqSearchConfig tunes FAQ retrieval (design D7: pure vector top-k).
type FaqSearchConfig struct {
	// TopK is how many FAQ chunks the search_faq tool returns at most.
	// Valid range 1..20 (validated); defaults to DefaultFaqSearchTopK.
	TopK int `yaml:"top_k"`
}

// FaqEmbeddingConfig mirrors the llm: credential pattern: the model id, its
// vector dimension, an optional OpenAI-compatible gateway base_url, and an
// api_key written as a ${ENV} reference (expanded from the environment at
// load — never a literal secret in the file).
type FaqEmbeddingConfig struct {
	Model string `yaml:"model"`
	// Dims is the embedding vector dimension (text-embedding-3-small = 1536,
	// design D2). A vector column is a (model, dimension) pair: changing the
	// model means a new column + migration, never a different Dims here.
	Dims int `yaml:"dims"`
	// BaseURL is an optional OpenAI-compatible gateway; empty = default.
	BaseURL string `yaml:"base_url"`
	// APIKey is a ${ENV} reference resolved at load; never a literal secret.
	APIKey string `yaml:"api_key"`
}

// FaqScrapeConfig is the crawler politeness policy (design D6): an explicit
// host allow-list, a minimum pause between requests, the contract User-Agent
// string, and the raw-HTML cache directory so re-embedding never re-crawls.
type FaqScrapeConfig struct {
	// AllowedHosts is the exhaustive list of hosts the scraper may fetch.
	AllowedHosts []string `yaml:"allowed_hosts"`
	// RequestDelayMS is the pause between consecutive requests. The default
	// (DefaultFaqScrapeRequestDelayMS) keeps cadence at >=1 req/s.
	RequestDelayMS int `yaml:"request_delay_ms"`
	// UserAgent is sent with every request; the default is the design D6
	// contract string identifying the project to linkaja.id operators.
	UserAgent string `yaml:"user_agent"`
	// CacheDir stores raw HTML between scrape and embed runs (gitignored).
	CacheDir string `yaml:"cache_dir"`
}

// FaqCorpusConfig points at the design D5 first-class artifacts.
type FaqCorpusConfig struct {
	// Path is the committed JSON corpus (data/corpus/linkaja-faq.json).
	Path string `yaml:"path"`
	// ManifestPath is the index step's per-chunk hash integrity manifest.
	ManifestPath string `yaml:"manifest_path"`
}

// FAQEmbeddingAPIKey resolves the embedding credential: the configured
// api_key value (${ENV}-expanded at load) or, when the config omits it,
// the OPENAI_API_KEY environment variable directly.
func (c *Config) FAQEmbeddingAPIKey() (string, error) {
	if v := c.FAQ.Embedding.APIKey; v != "" {
		return v, nil
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("faq.embedding.api_key is not set (reference ${OPENAI_API_KEY} in config.yaml or export OPENAI_API_KEY)")
}

// APIKey resolves the LLM key from the configured environment variable.
func (c *Config) APIKey() (string, error) {
	name := c.LLM.APIKeyEnv
	if name == "" {
		name = DefaultAPIKeyEnv
	}
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("environment variable %s is not set", name)
	}
	return v, nil
}

// ClientSecret resolves the Casdoor client secret from the configured
// environment variable.
func (c *Config) ClientSecret() (string, error) {
	if c.Auth.ClientSecretEnv == "" {
		return "", fmt.Errorf("auth.client_secret_env is not configured")
	}
	v := os.Getenv(c.Auth.ClientSecretEnv)
	if v == "" {
		return "", fmt.Errorf("environment variable %s is not set", c.Auth.ClientSecretEnv)
	}
	return v, nil
}

// Load reads the YAML file at path, expands ${VAR} references from the
// environment, and applies defaults. Missing variables expand to "" and
// are reported in the returned warnings slice.
func Load(path string) (*Config, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read config: %w", err)
	}

	expanded, warnings := expandEnv(string(raw))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, warnings, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(&cfg)
	if err := cfg.FAQ.Validate(); err != nil {
		return nil, warnings, err
	}
	if err := cfg.Agents.Validate(); err != nil {
		return nil, warnings, err
	}
	return &cfg, warnings, nil
}

// expandEnv replaces every ${NAME} with the environment value, recording
// the names that are unset.
func expandEnv(s string) (string, []string) {
	var missing []string
	out := os.Expand(s, func(key string) string {
		v, ok := os.LookupEnv(key)
		if !ok {
			missing = append(missing, key)
			return ""
		}
		return v
	})
	return out, missing
}

func applyDefaults(c *Config) {
	if c.Server.HTTPPort == 0 {
		c.Server.HTTPPort = DefaultHTTPPort
	}
	if c.LLM.BaseURL == "" {
		c.LLM.BaseURL = DefaultBaseURL
	}
	if c.LLM.Model == "" {
		c.LLM.Model = DefaultModel
	}
	if c.LLM.API == "" {
		c.LLM.API = DefaultLLMAPI
	}
	if c.LLM.APIKeyEnv == "" {
		c.LLM.APIKeyEnv = DefaultAPIKeyEnv
	}
	if c.Memory.HistoryWindow <= 0 {
		c.Memory.HistoryWindow = DefaultHistoryWindow
	}
	for name, srv := range c.MCPSrv {
		if srv.Transport == "" {
			srv.Transport = DefaultMCPTransport
			c.MCPSrv[name] = srv
		}
	}
	if len(c.Auth.AdminRoles) == 0 {
		c.Auth.AdminRoles = append([]string(nil), DefaultAdminRoles...)
	}
	for id, agent := range c.Agents {
		if agent.Enabled == nil {
			enabled := true
			agent.Enabled = &enabled
		}
		c.Agents[id] = agent
	}
	if c.FAQ.Embedding.Model == "" {
		c.FAQ.Embedding.Model = DefaultFaqEmbeddingModel
	}
	if c.FAQ.Embedding.Dims == 0 {
		c.FAQ.Embedding.Dims = DefaultFaqEmbeddingDims
	}
	if c.FAQ.Search.TopK <= 0 {
		c.FAQ.Search.TopK = DefaultFaqSearchTopK
	}
	if len(c.FAQ.Scrape.AllowedHosts) == 0 {
		c.FAQ.Scrape.AllowedHosts = append([]string(nil), DefaultFaqScrapeAllowedHosts...)
	}
	if c.FAQ.Scrape.RequestDelayMS == 0 {
		c.FAQ.Scrape.RequestDelayMS = DefaultFaqScrapeRequestDelayMS
	}
	if c.FAQ.Scrape.UserAgent == "" {
		c.FAQ.Scrape.UserAgent = DefaultFaqScrapeUserAgent
	}
	if c.FAQ.Scrape.CacheDir == "" {
		c.FAQ.Scrape.CacheDir = DefaultFaqScrapeCacheDir
	}
	if c.FAQ.Corpus.Path == "" {
		c.FAQ.Corpus.Path = DefaultFaqCorpusPath
	}
	if c.FAQ.Corpus.ManifestPath == "" {
		c.FAQ.Corpus.ManifestPath = DefaultFaqCorpusManifestPath
	}
	if strings.TrimSpace(c.LLM.SystemPrompt) == "" {
		c.LLM.SystemPrompt = DefaultSystemPrompt
	}
}

// DefaultSystemPrompt is used when the config omits llm.system_prompt.
const DefaultSystemPrompt = "You are a helpful personal assistant. Use the available tools when they help you answer accurately."
