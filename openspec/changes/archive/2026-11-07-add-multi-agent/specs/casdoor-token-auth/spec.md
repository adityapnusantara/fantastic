# Spec Delta

## ADDED Requirements

### Requirement: Roles reach the verified principal
The system SHALL extract the roles claim of a verified access token into the principal alongside the user identity. A token without a roles claim SHALL yield a principal with no roles, and authorization decisions SHALL treat it as holding no role.

#### Scenario: Admin role is visible to authorization
- **WHEN** a caller presents a valid token whose roles claim contains the admin role and performs an admin-gated operation
- **THEN** the operation sees the principal as holding the admin role

#### Scenario: Token without roles holds none
- **WHEN** a caller presents a valid token without a roles claim and performs an admin-gated operation
- **THEN** the principal holds no role and the operation is rejected with 403
