# agent-chat-streaming Specification

## Purpose

Serves the agent chat API over HTTP with Server-Sent Events streaming, so a client (or the built-in demo page) can submit a user message and receive the agent's streaming response, including tool activity, as observable events.

## Requirements

### Requirement: Streaming chat endpoint
The system SHALL expose `POST /api/v1/agents/{agent}/chat` that accepts a conversation id and a user message, runs the named agent for one turn, and streams agent events to the client as Server-Sent Events. The event format SHALL match eino's `http-sse-service` example (event name plus JSON data payload per event), carrying message deltas, tool activity with the executing agent's name, and turn-completion events. A request naming an unknown or disabled agent SHALL be rejected with 404.

#### Scenario: Successful streaming turn
- **WHEN** a client posts a chat request with a valid token and message to a known enabled agent
- **THEN** the response is `text/event-stream` and delivers agent events incrementally until a terminal event completes the turn

#### Scenario: Unknown agent is rejected
- **WHEN** a client posts a chat request naming an agent id that does not exist or is disabled
- **THEN** the response is 404 and no turn starts

#### Scenario: Full turn includes tool activity
- **WHEN** the agent decides to call a tool during the turn
- **THEN** tool activity events are streamed to the client before the terminal assistant output

### Requirement: Streaming deltas are live-only
The system SHALL stream message deltas to the client as they are produced, and SHALL persist only the assembled output of a turn (assistant message and tool results), never individual deltas.

#### Scenario: Persisted history contains assembled messages
- **WHEN** a turn completes after streaming many deltas
- **THEN** the stored conversation history contains one assembled assistant message and tool records, and no per-delta rows

### Requirement: Persist user message before provider work
The system SHALL persist the user message before invoking the model, so a failed or interrupted turn still records what was asked.

#### Scenario: Model failure preserves the question
- **WHEN** the model call fails after the user message was accepted
- **THEN** the user message remains persisted in the conversation and the stream closes with an error event

### Requirement: Persist turn output on completion or interruption
The system SHALL persist the assembled assistant output (including partial output when the client disconnects or the run is cancelled) after the turn ends, whatever the cause.

#### Scenario: Client disconnects mid-stream
- **WHEN** the client closes the connection while the agent is still producing output
- **THEN** the agent run is cancelled and the partial assistant output produced so far is persisted

### Requirement: Durability contract on persistence failure
The system SHALL treat failure to persist a turn's output as a failed turn: the stream SHALL be closed with an error event rather than completing a turn whose output is not stored.

#### Scenario: Output persistence fails
- **WHEN** writing the assembled assistant output fails after streaming
- **THEN** the stream ends with an error event indicating the turn was not saved

### Requirement: Static demo page
The system SHALL serve a static HTML demo page from the same server that lets an operator log in with username and password, obtain a token through the dev token endpoint, choose an agent from the agent listing, send chat messages to the selected agent, and observe the streamed response without any frontend build step.

#### Scenario: Operator chats from the demo page
- **WHEN** the operator opens the demo page, logs in, selects an agent, and sends a message
- **THEN** the page displays the streamed response of that agent as events arrive

### Requirement: Conversation is bound to the requested agent
Chatting into an existing conversation SHALL be allowed only when the conversation's agent matches the requested agent; a mismatch SHALL be rejected with 409 and no turn SHALL start. A chat request without a conversation id SHALL start a conversation bound to the requested agent.

#### Scenario: Mismatched agent is refused
- **WHEN** a client chats into a conversation owned by a different agent than the one in the path
- **THEN** the response is 409 and no turn starts

#### Scenario: New conversation inherits the requested agent
- **WHEN** a client chats to an agent without a conversation id
- **THEN** the created conversation is bound to that agent and its id is returned in the Conversation-Id header
