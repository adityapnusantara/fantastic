# Spec Delta

## Purpose

Stores agent definitions (prompt, tool assignment, model overrides, availability) in Postgres and manages them through an admin CRUD API with live rebuild, so adding an agent or changing its prompt and tools requires no code change or redeploy.

## ADDED Requirements

### Requirement: Durable agent definitions
The system SHALL store each agent as a row identified by a user-readable slug id that is immutable after creation, carrying a display name, description, system prompt, optional model overrides (model, api, temperature, reasoning effort), a tool assignment referencing the runtime tool catalog, an enabled flag, and a sort order.

#### Scenario: Agent survives restart
- **WHEN** an agent is created through the management API and the service restarts
- **THEN** the agent is still listed and usable with the same prompt, tools, and overrides

#### Scenario: Agent id is immutable
- **WHEN** a client attempts to change an agent's id through the management API
- **THEN** the request is rejected and the agent keeps its id

### Requirement: Agent management API
The system SHALL expose `GET /api/v1/agents` returning listing metadata (id, display name, description, icon, enabled, sort order, degraded tool names) to every authenticated user, and `GET`, `POST`, `PATCH`, `DELETE` on `/api/v1/agents/{id}` requiring the principal to hold the admin role. A principal without the admin role SHALL receive 403 on management operations while listing stays available.

#### Scenario: Listing open to all authenticated users
- **WHEN** an authenticated user without the admin role lists agents
- **THEN** the response contains the listing metadata of all agents

#### Scenario: Non-admin write is rejected
- **WHEN** a principal without the admin role creates, updates, or deletes an agent
- **THEN** the response is 403 and no change is applied

#### Scenario: Admin reads full agent detail
- **WHEN** a principal with the admin role requests one agent's detail
- **THEN** the response additionally contains the system prompt, tool assignment, and model overrides

### Requirement: Agent write validation
The system SHALL validate management writes: the id is a lowercase slug; the system prompt is required; the api override accepts only the supported API kinds; the temperature override is within the supported range; and every assigned tool name MUST exist in the runtime tool catalog. A violation SHALL be rejected with 400 naming the offending field or tool, and no partial change SHALL be applied.

#### Scenario: Unknown tool name rejected
- **WHEN** an agent is created or updated naming a tool that is not in the runtime catalog
- **THEN** the response is 400 and names the unknown tool, and the agent is unchanged

#### Scenario: Invalid slug rejected
- **WHEN** an agent is created with an id that is not a lowercase slug
- **THEN** the response is 400 and no agent row is created

### Requirement: Delete protection
The system SHALL refuse to delete an agent that is referenced by any conversation (409). Disabling an agent SHALL remove it from chat availability while its conversations and history remain readable.

#### Scenario: Delete with conversations is refused
- **WHEN** an admin deletes an agent that still has conversations
- **THEN** the response is 409 and the agent remains stored

#### Scenario: Disabled agent leaves chat but keeps history
- **WHEN** an agent is disabled
- **THEN** chat requests to it are rejected, its listing row is marked disabled, and its conversations' history remains readable

### Requirement: Live rebuild on management changes
The system SHALL apply create, update, delete, and disable operations to the running agent set without a service restart: subsequent requests use the updated definition, while turns already in flight complete on the definition they started with.

#### Scenario: Prompt change takes effect immediately
- **WHEN** an admin updates an agent's system prompt
- **THEN** the agent's next chat turn uses the new prompt without a service restart

#### Scenario: In-flight turn unaffected by update
- **WHEN** an agent definition is updated while one of its turns is streaming
- **THEN** that turn completes using the definition it started with

### Requirement: Catalog-driven tool assignment
The set of tools available for assignment SHALL come exclusively from the runtime tool catalog built from configuration: MCP tools exposed under server-prefixed catalog names and local tools under their own names. Management operations SHALL assign catalog tools, never define new ones.

#### Scenario: MCP tool assignable under prefixed name
- **WHEN** the configuration declares an MCP server with allowlisted tools
- **THEN** those tools are assignable to agents under `<server>.<tool>` catalog names

### Requirement: Missing tools degrade without failing
At startup, an agent whose assigned tools cannot all be resolved from the catalog SHALL still start, running with the resolvable subset; its listing SHALL mark the unresolved tool names as degraded and the degradation SHALL be logged.

#### Scenario: MCP server down at startup degrades referencing agents
- **WHEN** a configured MCP server is unreachable at startup and an agent references its tools
- **THEN** the service starts, the agent runs with its remaining tools, and its listing marks the missing tools as degraded

### Requirement: Configuration seeding on startup
On startup the system SHALL seed the agent table from the shipped configuration without overwriting existing rows, so a fresh deployment has working agents and operator edits survive restarts.

#### Scenario: First boot seeds configured agents
- **WHEN** the service starts against an empty agent table
- **THEN** the configured agents exist and a configured agent can be used for chat

#### Scenario: Operator edits survive restart
- **WHEN** an operator has edited an agent through the API and the service restarts
- **THEN** the edited values remain in effect

### Requirement: Static admin page
The system SHALL serve a static HTML admin page from the same server, without a frontend build step, that lets an admin log in, list agents, create and edit agents (prompt, tools, model overrides, enable/disable), and delete them. Authorization SHALL remain enforced by the API; the page only hides management controls it cannot use.

#### Scenario: Admin manages agents from the page
- **WHEN** an admin logs in on the admin page and edits an agent's system prompt
- **THEN** the agent's next chat turn uses the new prompt

#### Scenario: Non-admin cannot operate the page
- **WHEN** a user without the admin role submits a management action from the page
- **THEN** the API rejects the action with 403
