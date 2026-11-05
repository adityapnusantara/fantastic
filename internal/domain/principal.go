package domain

// Principal is the verified caller identity extracted from an access
// token. It is the only source of user identity allowed in business
// logic — never a client-supplied field.
type Principal struct {
	UserID       string
	Organization string
}

// IsZero reports whether no verified identity is present.
func (p Principal) IsZero() bool {
	return p.UserID == ""
}
