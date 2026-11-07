package domain

import "strings"

// Principal is the verified caller identity extracted from an access
// token. It is the only source of user identity allowed in business
// logic — never a client-supplied field. Roles ride along from the
// token for authorization decisions; they are claims, not identity.
type Principal struct {
	UserID       string
	Organization string
	Roles        []string
}

// IsZero reports whether no verified identity is present.
func (p Principal) IsZero() bool {
	return p.UserID == ""
}

// HasAnyRole reports whether the principal carries at least one of the
// allowed roles. Comparison is case-insensitive; stored casing is
// preserved. A principal with no roles never matches.
func (p Principal) HasAnyRole(allowed ...string) bool {
	for _, role := range p.Roles {
		for _, a := range allowed {
			if strings.EqualFold(role, a) {
				return true
			}
		}
	}
	return false
}
