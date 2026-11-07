# Spec Delta

## MODIFIED Requirements

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

### Requirement: Static demo page
The system SHALL serve a static HTML demo page from the same server that lets an operator log in with username and password, obtain a token through the dev token endpoint, choose an agent from the agent listing, send chat messages to the selected agent, and observe the streamed response without any frontend build step.

#### Scenario: Operator chats from the demo page
- **WHEN** the operator opens the demo page, logs in, selects an agent, and sends a message
- **THEN** the page displays the streamed response of that agent as events arrive

## ADDED Requirements

### Requirement: Conversation is bound to the requested agent
Chatting into an existing conversation SHALL be allowed only when the conversation's agent matches the requested agent; a mismatch SHALL be rejected with 409 and no turn SHALL start. A chat request without a conversation id SHALL start a conversation bound to the requested agent.

#### Scenario: Mismatched agent is refused
- **WHEN** a client chats into a conversation owned by a different agent than the one in the path
- **THEN** the response is 409 and no turn starts

#### Scenario: New conversation inherits the requested agent
- **WHEN** a client chats to an agent without a conversation id
- **THEN** the created conversation is bound to that agent and its id is returned in the Conversation-Id header
