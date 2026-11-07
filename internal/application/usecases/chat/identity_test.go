package chat

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// agentIdentity is the unexported resolver behind NewService; the ADK
// agent Description has no observable surface on emitted events, so the
// fallback for it is asserted here, against the resolver itself.
func TestAgentIdentityFallsBackWhenUnset(t *testing.T) {
	name, desc := agentIdentity(Config{SystemPrompt: "you are helpful"})
	assert.Equal(t, defaultAgentName, name, "empty AgentName falls back to the built-in name")
	assert.Equal(t, defaultAgentDescription, desc, "empty AgentDescription falls back to the built-in description")
}

func TestAgentIdentityUsesConfiguredValues(t *testing.T) {
	name, desc := agentIdentity(Config{
		AgentName:        "faq-bot",
		AgentDescription: "answers frequently asked questions",
	})
	assert.Equal(t, "faq-bot", name)
	assert.Equal(t, "answers frequently asked questions", desc)
}

func TestAgentIdentityFallsBackPerField(t *testing.T) {
	name, desc := agentIdentity(Config{AgentName: "faq-bot"})
	assert.Equal(t, "faq-bot", name)
	assert.Equal(t, defaultAgentDescription, desc, "unset description still falls back when only the name is set")

	name, desc = agentIdentity(Config{AgentDescription: "custom"})
	assert.Equal(t, defaultAgentName, name, "unset name still falls back when only the description is set")
	assert.Equal(t, "custom", desc)
}
