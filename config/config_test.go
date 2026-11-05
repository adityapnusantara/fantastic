package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestLoadDefaults(t *testing.T) {
	path := writeTemp(t, "server: {}\n")
	cfg, warnings, err := Load(path)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	assert.Equal(t, DefaultHTTPPort, cfg.Server.HTTPPort)
	assert.Equal(t, DefaultBaseURL, cfg.LLM.BaseURL)
	assert.Equal(t, DefaultModel, cfg.LLM.Model)
	assert.Equal(t, DefaultAPIKeyEnv, cfg.LLM.APIKeyEnv)
	assert.Equal(t, DefaultHistoryWindow, cfg.Memory.HistoryWindow)
	assert.Equal(t, DefaultSystemPrompt, cfg.LLM.SystemPrompt)
}

func TestLoadEnvInterpolation(t *testing.T) {
	t.Setenv("TEBAR_MCP_TOKEN", "tok-123")
	t.Setenv("CASDOOR_SECRET", "sec-456")

	path := writeTemp(t, `
mcp_servers:
  tebar:
    url: https://api.tebar.app/mcp
    headers:
      Authorization: "Bearer ${TEBAR_MCP_TOKEN}"
    allow_tools: [list_accounts]
auth:
  client_secret_env: CASDOOR_SECRET
`)
	cfg, warnings, err := Load(path)
	require.NoError(t, err)
	assert.Empty(t, warnings)

	assert.Equal(t, "Bearer tok-123", cfg.MCPSrv["tebar"].Headers["Authorization"])
	assert.Equal(t, []string{"list_accounts"}, cfg.MCPSrv["tebar"].AllowTools)

	secret, err := cfg.ClientSecret()
	require.NoError(t, err)
	assert.Equal(t, "sec-456", secret)
}

func TestLoadMissingEnvWarns(t *testing.T) {
	path := writeTemp(t, `
mcp_servers:
  tebar:
    url: https://api.tebar.app/mcp
    headers:
      Authorization: "Bearer ${NOT_SET_ANYWHERE}"
`)
	cfg, warnings, err := Load(path)
	require.NoError(t, err)
	assert.Contains(t, warnings, "NOT_SET_ANYWHERE")
	assert.Equal(t, "Bearer ", cfg.MCPSrv["tebar"].Headers["Authorization"])
}

func TestTransportDefaultsToHTTP(t *testing.T) {
	path := writeTemp(t, `
mcp_servers:
  demo:
    url: http://127.0.0.1:9999/mcp
`)
	cfg, _, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, DefaultMCPTransport, cfg.MCPSrv["demo"].Transport)
}

func TestAPIKeyFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	path := writeTemp(t, "server: {}\n")
	cfg, _, err := Load(path)
	require.NoError(t, err)

	key, err := cfg.APIKey()
	require.NoError(t, err)
	assert.Equal(t, "sk-test", key)
}

func TestAPIKeyMissing(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	path := writeTemp(t, "server: {}\n")
	cfg, _, err := Load(path)
	require.NoError(t, err)

	_, err = cfg.APIKey()
	assert.Error(t, err)
}
