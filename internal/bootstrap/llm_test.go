package bootstrap

import (
	"context"
	"testing"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// The per-agent model override tests (design D3: nullable override
// columns; nil = the global llm block). The merge into an effective llm
// config is a pure function, so value-level override semantics are
// asserted on it; llmModel tests assert the api override really switches
// the provider branch and that a bogus override fails.

func testLLMConfig() config.LLMConfig {
	return config.LLMConfig{
		API:             "responses",
		BaseURL:         "http://127.0.0.1:9",
		Model:           "global-model",
		Temperature:     0.7,
		MaxTokens:       512,
		ReasoningEffort: "medium",
	}
}

func strPtr(s string) *string { return &s }

func f32Ptr(f float32) *float32 { return &f }

func TestOverridesFromAgentCarriesPointers(t *testing.T) {
	model := "agent-model"
	api := "chat"
	temp := float32(0.3)
	effort := "low"
	full := domain.Agent{Model: &model, API: &api, Temperature: &temp, ReasoningEffort: &effort}

	o := overridesFromAgent(full)
	require.NotNil(t, o.Model)
	assert.Equal(t, "agent-model", *o.Model)
	require.NotNil(t, o.API)
	assert.Equal(t, "chat", *o.API)
	require.NotNil(t, o.Temperature)
	assert.InDelta(t, 0.3, *o.Temperature, 0.0001)
	require.NotNil(t, o.ReasoningEffort)
	assert.Equal(t, "low", *o.ReasoningEffort)

	empty := overridesFromAgent(domain.Agent{})
	assert.Nil(t, empty.Model, "nil override columns must stay nil")
	assert.Nil(t, empty.API)
	assert.Nil(t, empty.Temperature)
	assert.Nil(t, empty.ReasoningEffort)
}

func TestEffectiveLLMNilOverridesInheritGlobal(t *testing.T) {
	base := testLLMConfig()

	got := effectiveLLM(base, modelOverrides{})

	assert.Equal(t, base, got, "no overrides means the effective config IS the global llm block")
}

func TestEffectiveLLMOverridesWin(t *testing.T) {
	base := testLLMConfig()

	got := effectiveLLM(base, modelOverrides{
		Model:           strPtr("agent-model"),
		API:             strPtr("chat"),
		Temperature:     f32Ptr(1.5),
		ReasoningEffort: strPtr("low"),
	})

	assert.Equal(t, "agent-model", got.Model)
	assert.Equal(t, "chat", got.API)
	assert.InDelta(t, 1.5, got.Temperature, 0.0001)
	assert.Equal(t, "low", got.ReasoningEffort)
	// untouched fields keep the global value
	assert.Equal(t, base.BaseURL, got.BaseURL)
	assert.Equal(t, base.MaxTokens, got.MaxTokens)
}

func TestEffectiveLLMTemperatureZeroIsARealOverride(t *testing.T) {
	// 0.0 is a valid temperature (config bounds: min 0) — the pointer,
	// not the value, carries "unset". A pointer to 0 must win over the
	// global 0.7.
	got := effectiveLLM(testLLMConfig(), modelOverrides{Temperature: f32Ptr(0)})
	assert.InDelta(t, 0, got.Temperature, 0.0001, "a pointer to 0 overrides the global temperature")
}

func TestEffectiveLLMEmptyStringOverridesInherit(t *testing.T) {
	// a pointer to an empty string is treated as inherit too: a blank
	// model or api name is never a meaningful override
	got := effectiveLLM(testLLMConfig(), modelOverrides{Model: strPtr(""), API: strPtr(""), ReasoningEffort: strPtr("")})
	assert.Equal(t, "global-model", got.Model)
	assert.Equal(t, "responses", got.API)
	assert.Equal(t, "medium", got.ReasoningEffort)
}

func TestLLMModelGlobalResponsesBranch(t *testing.T) {
	cfg := &config.Config{LLM: testLLMConfig()}

	m, err := llmModel(context.Background(), cfg, "test-key", modelOverrides{})
	require.NoError(t, err)
	assert.IsType(t, &agenticopenai.ResponsesModel{}, m, "the default api builds the responses model")
}

func TestLLMModelGlobalChatBranch(t *testing.T) {
	llm := testLLMConfig()
	llm.API = "chat"
	cfg := &config.Config{LLM: llm}

	m, err := llmModel(context.Background(), cfg, "test-key", modelOverrides{})
	require.NoError(t, err)
	assert.IsType(t, &agenticopenai.ChatModel{}, m, "llm.api chat builds the chat model")
}

func TestLLMModelAPIOverrideSwitchesBranch(t *testing.T) {
	// the agent row's api override must switch the provider branch even
	// though the global llm.api selects the other one
	cfg := &config.Config{LLM: testLLMConfig()} // global: responses

	m, err := llmModel(context.Background(), cfg, "test-key", modelOverrides{API: strPtr("chat")})
	require.NoError(t, err)
	assert.IsType(t, &agenticopenai.ChatModel{}, m, "the per-agent api override switches to the chat branch")

	llm := testLLMConfig()
	llm.API = "chat"
	cfgChat := &config.Config{LLM: llm}
	m, err = llmModel(context.Background(), cfgChat, "test-key", modelOverrides{API: strPtr("responses")})
	require.NoError(t, err)
	assert.IsType(t, &agenticopenai.ResponsesModel{}, m, "the per-agent api override switches back to responses")
}

func TestLLMModelRejectsUnsupportedAPIOverride(t *testing.T) {
	cfg := &config.Config{LLM: testLLMConfig()}

	_, err := llmModel(context.Background(), cfg, "test-key", modelOverrides{API: strPtr("bogus")})
	require.Error(t, err, "an api override outside the supported enum must fail the build")
	assert.Contains(t, err.Error(), "responses")
	assert.Contains(t, err.Error(), "chat")
}
