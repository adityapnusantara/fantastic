package domain

import "time"

// Agent is a selectable chat agent: identity, prompt, optional model
// overrides, and its tool assignment. ID is an immutable slug (it lives
// in the URL and is referenced by conversations).
type Agent struct {
	ID              string
	DisplayName     string
	Description     string
	Icon            string
	SystemPrompt    string
	Model           *string  // nil = global llm default
	API             *string  // "responses" or "chat"; nil = global default
	Temperature     *float32 // nil = global default
	ReasoningEffort *string  // nil = global default
	Tools           []string
	Enabled         bool
	SortOrder       int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
