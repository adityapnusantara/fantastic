# Spec Delta

## MODIFIED Requirements

### Requirement: Citation-bearing FAQ answers in chat
Agents whose tool assignment includes the FAQ search tool SHALL have it available; the tool SHALL return the top matching FAQ chunks with their source URLs and scores. For questions answered from FAQ content, the agent's answer SHALL cite the source URL of the chunk it used, and the tool call SHALL be observable in the chat stream as a tool result event. Answers SHALL be in the user's language (Indonesian by default). An agent whose tool assignment excludes the FAQ search tool SHALL NOT offer the tool in its turns.

#### Scenario: FAQ question answered with a citation
- **WHEN** a user asks a question covered by the FAQ corpus in the chat of an agent that assigns the FAQ search tool
- **THEN** the agent invokes the FAQ search tool, the stream shows the tool result event, and the answer cites the matching source URL

#### Scenario: Answer cites only retrieved sources
- **WHEN** an agent answers from FAQ content
- **THEN** every cited URL is a source URL returned by the FAQ search tool, not an invented link

#### Scenario: Agent without the assignment lacks the tool
- **WHEN** an agent's tool assignment excludes the FAQ search tool
- **THEN** the agent cannot invoke it and its turns expose no FAQ tool result events
