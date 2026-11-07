# Spec Delta

## ADDED Requirements

### Requirement: Conversations record their agent
Every conversation SHALL record the id of the agent it belongs to. The migration SHALL assign all existing conversations to the default agent. Conversation reads (get and list) SHALL expose the agent id, and listing SHALL support filtering by agent id.

#### Scenario: Existing conversations are backfilled
- **WHEN** the migration runs on a database with existing conversations
- **THEN** every conversation references the default agent

#### Scenario: Conversation reads expose the agent
- **WHEN** a caller reads or lists their conversations
- **THEN** each conversation carries the id of its agent

#### Scenario: Listing filters by agent
- **WHEN** a caller lists conversations filtered by an agent id
- **THEN** only conversations bound to that agent are returned
