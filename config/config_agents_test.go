package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Task 1.2: auth.admin_roles defaults to [fantastic-admin] (design D6).
func TestLoadAdminRolesDefaults(t *testing.T) {
	path := writeTemp(t, "server: {}\n")
	cfg, _, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"fantastic-admin"}, cfg.Auth.AdminRoles)
}

func TestLoadAdminRolesExplicitHonored(t *testing.T) {
	path := writeTemp(t, "auth:\n  admin_roles: [ops, fantastic-admin]\n")
	cfg, _, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"ops", "fantastic-admin"}, cfg.Auth.AdminRoles)
}

// Task 1.2: an omitted enabled defaults to true and sort_order to 0.
func TestLoadAgentsDefaults(t *testing.T) {
	path := writeTemp(t, "agents:\n  general:\n    system_prompt: You are a helpful assistant.\n")
	cfg, _, err := Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Agents, 1)

	a := cfg.Agents["general"]
	require.NotNil(t, a.Enabled)
	assert.True(t, *a.Enabled)
	assert.Equal(t, 0, a.SortOrder)
	assert.Empty(t, a.Tools)
	assert.Empty(t, a.Model, "unset model inherits the global llm block")
	assert.Empty(t, a.API, "unset api inherits the global llm block")
	assert.Equal(t, float32(0), a.Temperature, "unset temperature inherits the global llm block")
}

// Task 1.2: a fully specified seed round-trips every field.
func TestLoadAgentsFullBlockHonored(t *testing.T) {
	path := writeTemp(t, `
agents:
  support:
    display_name: Support Agent
    description: Handles support questions
    icon: support
    system_prompt: You handle support.
    tools:
      - search_faq
      - tebar.list_posts
    model: gpt-6.1-mini
    api: chat
    temperature: 0.7
    reasoning_effort: low
    enabled: false
    sort_order: 3
`)
	cfg, _, err := Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Agents, 1)

	a := cfg.Agents["support"]
	assert.Equal(t, "Support Agent", a.DisplayName)
	assert.Equal(t, "Handles support questions", a.Description)
	assert.Equal(t, "support", a.Icon)
	assert.Equal(t, "You handle support.", a.SystemPrompt)
	assert.Equal(t, []string{"search_faq", "tebar.list_posts"}, a.Tools)
	assert.Equal(t, "gpt-6.1-mini", a.Model)
	assert.Equal(t, "chat", a.API)
	assert.Equal(t, float32(0.7), a.Temperature)
	assert.Equal(t, "low", a.ReasoningEffort)
	require.NotNil(t, a.Enabled)
	assert.False(t, *a.Enabled)
	assert.Equal(t, 3, a.SortOrder)
}

// Task 1.2: each agents: validation rule rejects with an error naming the
// offending agent and field.
func TestLoadAgentsValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "uppercase id rejected",
			yaml:    "agents:\n  General:\n    system_prompt: hi\n",
			wantErr: "agents.General: invalid agent id",
		},
		{
			name:    "underscore id rejected",
			yaml:    "agents:\n  bad_id:\n    system_prompt: hi\n",
			wantErr: "invalid agent id (must match ^[a-z0-9-]+$)",
		},
		{
			name:    "empty id rejected",
			yaml:    "agents:\n  \"\":\n    system_prompt: hi\n",
			wantErr: "agents.: invalid agent id",
		},
		{
			name:    "empty system_prompt rejected",
			yaml:    "agents:\n  general:\n    system_prompt: \"\"\n",
			wantErr: "agents.general.system_prompt must not be empty",
		},
		{
			name:    "api outside enum rejected",
			yaml:    "agents:\n  general:\n    system_prompt: hi\n    api: completions\n",
			wantErr: `agents.general.api must be "responses" or "chat", got "completions"`,
		},
		{
			name:    "negative temperature rejected",
			yaml:    "agents:\n  general:\n    system_prompt: hi\n    temperature: -0.1\n",
			wantErr: "agents.general.temperature must be between 0 and 2, got -0.1",
		},
		{
			name:    "temperature above max rejected",
			yaml:    "agents:\n  general:\n    system_prompt: hi\n    temperature: 2.1\n",
			wantErr: "agents.general.temperature must be between 0 and 2, got 2.1",
		},
		{
			name:    "empty tool name rejected",
			yaml:    "agents:\n  general:\n    system_prompt: hi\n    tools:\n      - \"\"\n",
			wantErr: "agents.general.tools[0] must be a non-empty tool name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTemp(t, tt.yaml)
			_, _, err := Load(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// Task 1.2: temperature 0 means "inherit the llm block" and 2 is the
// accepted maximum; ids may carry lowercase letters, digits and hyphens.
func TestLoadAgentsBoundariesAccepted(t *testing.T) {
	path := writeTemp(t, `
agents:
  faq-helper-2:
    system_prompt: hi
    temperature: 2
`)
	cfg, _, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, float32(2), cfg.Agents["faq-helper-2"].Temperature)
}

// Validate also guards programmatically constructed values (zero struct).
func TestAgentSeedsValidateZeroStructRejected(t *testing.T) {
	err := AgentSeeds{"general": {}}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agents.general.system_prompt")
}

// Task 1.2 contract: the shipped config.yaml carries a valid agents: seed
// block — the general agent with the FAQ answering rules and the
// search_faq tool — plus auth.admin_roles.
func TestShippedConfigAgentsBlock(t *testing.T) {
	raw, err := os.ReadFile("../config.yaml")
	require.NoError(t, err)

	var cfg Config
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	applyDefaults(&cfg)
	require.NoError(t, cfg.FAQ.Validate())
	require.NoError(t, cfg.Agents.Validate())

	general := cfg.Agents["general"]
	assert.Equal(t, "General Assistant", general.DisplayName)
	assert.Contains(t, general.Tools, "search_faq")
	assert.Contains(t, general.SystemPrompt, "FAQ answering rules (search_faq tool)")
	assert.Contains(t, general.SystemPrompt, "cite the linkaja.id source_url")
	require.NotNil(t, general.Enabled)
	assert.True(t, *general.Enabled)
	assert.Equal(t, []string{"fantastic-admin"}, cfg.Auth.AdminRoles)
}
