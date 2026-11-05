# mcp-tool-integration Specification

## Purpose

Connects the agent to external tools exclusively through MCP servers declared in configuration, so new tool capabilities are added by editing configuration — never by changing code.

## Requirements

### Requirement: Configuration-driven MCP servers
The system SHALL build the agent's tool set from MCP servers declared in configuration, where each server entry defines a URL using the streamable HTTP or SSE transport, optional HTTP headers, and optional allowed-tool filtering. Header values SHALL support environment-variable references so secrets are not written in configuration files.

#### Scenario: Tool from configured server is usable
- **WHEN** the config declares a reachable MCP server with tools and the agent runs a turn
- **THEN** the agent can invoke that server's tools

#### Scenario: Secret referenced from environment
- **WHEN** a header value references an environment variable
- **THEN** the resolved request carries the environment value and the raw configuration file contains no secret

### Requirement: Tool allowlist per server
When a server entry declares an allowed-tool list, the system SHALL expose only those tools from that server to the agent. When no list is declared, all tools of that server SHALL be exposed.

#### Scenario: Only allowed tools are exposed
- **WHEN** a server exposes write tools but its allowlist names only read tools
- **THEN** the agent can see and call only the allowlisted tools

#### Scenario: Unlisted tool is unavailable
- **WHEN** the agent attempts to determine whether a non-allowlisted tool exists
- **THEN** that tool is not offered to the agent

### Requirement: Server failure isolation
The system SHALL start and continue serving chat when a configured MCP server is unreachable: turns run with the tools of the reachable servers, and the failure SHALL be surfaced in the logs without failing the whole service.

#### Scenario: One server down does not break the agent
- **WHEN** one of several configured MCP servers is unreachable at startup
- **THEN** the service still starts and the agent runs with the remaining servers' tools

### Requirement: Tebar MCP as demo server
The default shipped configuration SHALL declare the Tebar MCP server with its Bearer token sourced from an environment variable, so tool integration can be exercised without running any local MCP server.

#### Scenario: Demo config connects without local servers
- **WHEN** the operator starts the service with the shipped configuration and a valid token environment variable
- **THEN** the agent exposes the Tebar server's allowlisted tools
