# Spec Delta

## Purpose

Persists conversations and messages in PostgreSQL so chat history survives restarts, keeps the model context within a configurable window using rolling-summary compaction, and exposes conversation management for chat UIs.

## ADDED Requirements

### Requirement: User-scoped conversations
Every conversation SHALL belong to the verified caller: the user id from the authenticated principal SHALL be stored on the conversation and all conversation queries (list, read, delete, chat) SHALL be restricted to conversations owned by that user.

#### Scenario: Caller lists own conversations only
- **WHEN** a caller lists conversations
- **THEN** only conversations owned by that caller's user id are returned

#### Scenario: Caller cannot read another user's conversation
- **WHEN** a caller requests a conversation owned by a different user id
- **THEN** the response is 404 and no message content is revealed

### Requirement: Message persistence model
The system SHALL store each message with: a stable unique id, owning conversation id, an ordering position unique within the conversation, a role (`user`, `assistant`, or `tool`), text content, optional tool-call data (as structured tool calls with ids, names, and arguments for assistant messages), a tool-call id for tool results, and a creation timestamp. Deleting a conversation SHALL delete its messages.

#### Scenario: History round-trips tool calls
- **WHEN** a turn uses tools and the conversation is later reloaded
- **THEN** the stored history contains the assistant tool calls with their ids and arguments and the corresponding tool results linked by tool-call id, in original order

#### Scenario: Deleting a conversation removes messages
- **WHEN** a caller deletes one of their conversations
- **THEN** the conversation and all its messages are removed

### Requirement: Idempotent message inserts
The system SHALL reject duplicate message inserts for the same message id instead of duplicating the row.

#### Scenario: Duplicate insert is ignored
- **WHEN** the same message id is inserted twice
- **THEN** the conversation contains exactly one copy of that message

### Requirement: History window sent to the model
For each turn, the system SHALL send the conversation's rolling summary plus at most the configured number of recent messages (default 20) to the model, in conversation order, regardless of how many messages are stored.

#### Scenario: Long conversation stays within the window
- **WHEN** a conversation has more messages than the configured window
- **THEN** the model context for the next turn contains the rolling summary plus only the most recent messages up to the window limit

### Requirement: Rolling-summary compaction
When messages exist outside the configured window that are not yet covered by the rolling summary, the system SHALL fold them into the rolling summary using the model, update the coverage marker, and keep the full raw messages stored.

#### Scenario: Overflow is folded into the summary
- **WHEN** a turn ends and messages beyond the window are not covered by the summary
- **THEN** the summary is regenerated to include those messages, the coverage marker advances, and the raw messages remain stored

#### Scenario: UI still sees full history
- **WHEN** a client loads a compacted conversation's messages
- **THEN** the complete raw message history is returned, including messages covered by the summary

### Requirement: Conversation management API
The system SHALL let an authenticated caller list their conversations (newest activity first), read a conversation's full flattened message history, and delete a conversation. A new conversation SHALL be titled from the first user message.

#### Scenario: List conversations newest first
- **WHEN** a caller lists conversations
- **THEN** conversations are ordered by most recent activity

#### Scenario: Read messages returns flattened history
- **WHEN** a caller reads a conversation's messages
- **THEN** the response contains user, assistant, and tool messages in order, including tool-call data

#### Scenario: New conversation is auto-titled
- **WHEN** the first user message is persisted on a conversation
- **THEN** the conversation title is derived from that message
