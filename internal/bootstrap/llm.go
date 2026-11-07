// llm.go builds the chat model: the global llm config block plus the
// per-agent overrides of a stored agent row (design D3 — nullable
// override columns, nil = inherit the global value).
package bootstrap

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/components/model"
	"github.com/openai/openai-go/v3/responses"

	"github.com/adityapratamanusantara/fantastic/config"
	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

// modelOverrides carries one agent's nullable model overrides (the
// agents table's model/api/temperature/reasoning_effort columns): a nil
// field inherits the global llm block's value.
type modelOverrides struct {
	Model           *string
	API             *string
	Temperature     *float32
	ReasoningEffort *string
}

// overridesFromAgent reads the overrides off a stored agent row.
func overridesFromAgent(a domain.Agent) modelOverrides {
	return modelOverrides{
		Model:           a.Model,
		API:             a.API,
		Temperature:     a.Temperature,
		ReasoningEffort: a.ReasoningEffort,
	}
}

// effectiveLLM merges the per-agent overrides onto the global llm block.
// The pointer carries "unset": a nil field keeps the global value, and a
// pointer to an empty string inherits too (a blank model or api name is
// never a meaningful override). A pointer to temperature 0 IS an
// override — 0 is a valid temperature (config bounds: min 0), so the
// pointer, not the value, is what says "unset".
func effectiveLLM(base config.LLMConfig, o modelOverrides) config.LLMConfig {
	out := base
	if o.Model != nil && *o.Model != "" {
		out.Model = *o.Model
	}
	if o.API != nil && *o.API != "" {
		out.API = *o.API
	}
	if o.Temperature != nil {
		out.Temperature = *o.Temperature
	}
	if o.ReasoningEffort != nil && *o.ReasoningEffort != "" {
		out.ReasoningEffort = *o.ReasoningEffort
	}
	return out
}

// llmModel builds the agent model per the effective llm.api (the global
// llm.api overlaid with the agent's api override). "responses" (the
// default) targets OpenAI /v1/responses — required for reasoning models
// with function tools such as gpt-6.1-sol — while "chat" targets
// /v1/chat/completions for classic models on gateways without a
// responses endpoint. Both come from eino-ext agenticopenai and speak
// *schema.AgenticMessage, so the usecase layer is identical.
func llmModel(ctx context.Context, cfg *config.Config, apiKey string, o modelOverrides) (model.AgenticModel, error) {
	llm := effectiveLLM(cfg.LLM, o)
	switch llm.API {
	case "", "responses":
		mc := &agenticopenai.ResponsesConfig{
			APIKey:  apiKey,
			BaseURL: llm.BaseURL,
			Model:   llm.Model,
		}
		if llm.Temperature != 0 {
			t := llm.Temperature
			mc.Temperature = &t
		}
		if llm.MaxTokens > 0 {
			mt := llm.MaxTokens
			mc.MaxTokens = &mt
		}
		if llm.ReasoningEffort != "" {
			mc.Reasoning = &responses.ReasoningParam{
				Effort: responses.ReasoningEffort(llm.ReasoningEffort),
			}
		}
		return agenticopenai.NewResponsesModel(ctx, mc)
	case "chat":
		mc := &agenticopenai.ChatConfig{
			APIKey:  apiKey,
			BaseURL: llm.BaseURL,
			Model:   llm.Model,
		}
		if llm.Temperature != 0 {
			t := llm.Temperature
			mc.Temperature = &t
		}
		return agenticopenai.NewChatModel(ctx, mc)
	default:
		return nil, fmt.Errorf("llm.api must be \"responses\" or \"chat\", got %q", llm.API)
	}
}
