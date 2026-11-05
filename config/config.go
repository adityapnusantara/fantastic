// Package config loads fantastic's YAML configuration with ${VAR}
// environment interpolation for secret values.
package config

import (
	"fmt"
	"os"
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
)

// Config is the root configuration document.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	LLM      LLMConfig      `yaml:"llm"`
	Memory   MemoryConfig   `yaml:"memory"`
	Auth     AuthConfig     `yaml:"auth"`
	MCPSrv   MCPServers     `yaml:"mcp_servers"`
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
	API         string  `yaml:"api"`
	BaseURL     string  `yaml:"base_url"`
	Model       string  `yaml:"model"`
	APIKeyEnv   string  `yaml:"api_key_env"`
	SystemPrompt string `yaml:"system_prompt"`
	Temperature float32 `yaml:"temperature"`
	MaxTokens   int     `yaml:"max_tokens"`
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
	if strings.TrimSpace(c.LLM.SystemPrompt) == "" {
		c.LLM.SystemPrompt = DefaultSystemPrompt
	}
}

// DefaultSystemPrompt is used when the config omits llm.system_prompt.
const DefaultSystemPrompt = "You are a helpful personal assistant. Use the available tools when they help you answer accurately."
