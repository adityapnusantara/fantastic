# casdoor-token-auth Specification

## Purpose

Authenticates callers by verifying Casdoor-issued bearer access tokens locally, so every API route carries a verified identity that scopes and stamps all conversation data, with a configuration-gated dev token endpoint for the demo page.

## Requirements

### Requirement: Bearer access token required on API routes
Every `/api/v1` route SHALL require an `Authorization: Bearer <token>` header carrying a valid Casdoor access token. A request with no header, a non-Bearer credential, or an empty token SHALL be rejected before any business logic runs.

#### Scenario: Missing credentials
- **WHEN** a client calls an `/api/v1` route without an Authorization header
- **THEN** the response has HTTP status 401 with an unauthorized error code and a `WWW-Authenticate: Bearer` header

#### Scenario: Non-Bearer credentials
- **WHEN** a client sends `Authorization: Basic abc`
- **THEN** the response has HTTP status 401 with an unauthorized error code

### Requirement: Token validation
The system SHALL accept a token only when all of the following hold: its signature verifies against a key published at the Casdoor JWKS endpoint using RS256; its issuer equals the configured issuer; its audience contains the configured client id; it has not expired; it is an access token rather than a refresh or id token; and its organization equals the configured organization. Any failed check SHALL produce HTTP status 401 with an unauthorized error code, without revealing which check failed.

#### Scenario: Expired token
- **WHEN** a client presents a token whose expiry is in the past
- **THEN** the response has HTTP status 401

#### Scenario: Wrong signature
- **WHEN** a client presents a token signed by a key that is not in the JWKS
- **THEN** the response has HTTP status 401

#### Scenario: Wrong audience
- **WHEN** a client presents a token issued for a different Casdoor application
- **THEN** the response has HTTP status 401

#### Scenario: Refresh token used as access token
- **WHEN** a client presents a Casdoor refresh token or id token as the Bearer credential
- **THEN** the response has HTTP status 401

### Requirement: Signing key rotation
The system SHALL keep fetched signing keys cached between requests. When a token names a key id that is not cached, the system SHALL refetch the key set once before rejecting the token. A failure to reach the JWKS endpoint SHALL NOT cause a token signed by a cached key to be rejected.

#### Scenario: New signing key published
- **WHEN** Casdoor rotates its signing key and a client presents a token signed with the new key
- **THEN** the system refetches the key set and accepts the token

#### Scenario: JWKS endpoint temporarily unreachable
- **WHEN** the JWKS endpoint cannot be reached but the token is signed by a cached key
- **THEN** the token is accepted

### Requirement: Verified identity reaches business logic
On a valid token, the system SHALL derive the caller identity (user id) from the token and make it available to business logic. Conversation data access SHALL use this verified identity, never a client-supplied user id.

#### Scenario: Identity comes from the token
- **WHEN** a caller performs any conversation operation
- **THEN** the owning user id used for that operation is the one inside the verified token

### Requirement: Dev token endpoint
The system SHALL expose a token endpoint for the demo page that accepts a username and password, exchanges them for an access token using the configured Casdoor application credentials held server-side, and returns the access token. This endpoint SHALL be disabled unless explicitly enabled in configuration, and SHALL return 404 when disabled.

#### Scenario: Enabled endpoint issues a token
- **WHEN** the endpoint is enabled in configuration and the operator submits valid demo credentials
- **THEN** the response contains an access token usable on `/api/v1` routes

#### Scenario: Disabled by default
- **WHEN** configuration does not enable the endpoint
- **THEN** requests to it return 404

#### Scenario: Invalid credentials rejected
- **WHEN** the endpoint is enabled and Casdoor rejects the credentials
- **THEN** the response has HTTP status 401

### Requirement: Roles reach the verified principal
The system SHALL extract the roles claim of a verified access token into the principal alongside the user identity. A token without a roles claim SHALL yield a principal with no roles, and authorization decisions SHALL treat it as holding no role.

#### Scenario: Admin role is visible to authorization
- **WHEN** a caller presents a valid token whose roles claim contains the admin role and performs an admin-gated operation
- **THEN** the operation sees the principal as holding the admin role

#### Scenario: Token without roles holds none
- **WHEN** a caller presents a valid token without a roles claim and performs an admin-gated operation
- **THEN** the principal holds no role and the operation is rejected with 403
