package casdoor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/adityapratamanusantara/fantastic/internal/domain"
)

const accessTokenType = "access-token"

// claims are the Casdoor token claims the verifier checks beyond the
// registered ones.
type claims struct {
	jwt.RegisteredClaims

	TokenType string    `json:"tokenType"`
	Owner     string    `json:"owner"`
	Name      string    `json:"name"`
	Roles     roleNames `json:"roles"`
}

// roleNames tolerates both shapes of Casdoor's roles claim: an array of
// role-name strings (custom token fields) and — the default JWT mode —
// an array of role objects carrying a name field. Anything else per item
// is ignored rather than failing the whole token.
type roleNames []string

func (r *roleNames) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		var name string
		if err := json.Unmarshal(item, &name); err == nil {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, name)
			}
			continue
		}
		var role struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(item, &role); err == nil {
			if name := strings.TrimSpace(role.Name); name != "" {
				out = append(out, name)
			}
		}
	}
	*r = out
	return nil
}

// roles returns the token's roles claim normalized: absent claim yields
// an empty (never nil) slice, entries are trimmed, blanks dropped.
func (c claims) roles() []string {
	out := make([]string, 0, len(c.Roles))
	for _, r := range c.Roles {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// Verifier validates Casdoor access tokens into a domain.Principal.
type Verifier struct {
	cfg    Config
	keys   *jwksCache
	parser *jwt.Parser
}

// NewVerifier returns a Verifier. The JWKS is fetched lazily on the first
// Verify, so construction never touches the network.
func NewVerifier(cfg Config, opts ...Option) *Verifier {
	o := newOptions(opts)
	return &Verifier{
		cfg:  cfg,
		keys: newJWKSCache(cfg.JWKSURL, o),
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{"RS256"}),
			jwt.WithExpirationRequired(),
			jwt.WithIssuer(cfg.Issuer),
			jwt.WithAudience(cfg.Audience),
			jwt.WithLeeway(defaultLeeway),
			jwt.WithTimeFunc(o.clock),
		),
	}
}

// Verify turns a raw bearer token into a Principal. Every failure is an
// opaque error; callers must not reveal which check failed. The detailed
// cause is available via errors.Unwrap-style inspection for server logs.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (domain.Principal, error) {
	if strings.TrimSpace(rawToken) == "" {
		return domain.Principal{}, errors.New("empty token")
	}
	var c claims
	if _, err := v.parser.ParseWithClaims(rawToken, &c, v.keyFunc(ctx)); err != nil {
		return domain.Principal{}, err
	}
	if c.TokenType != accessTokenType {
		return domain.Principal{}, fmt.Errorf("token type %q is not an access token", c.TokenType)
	}
	if c.Owner != v.cfg.Organization {
		return domain.Principal{}, fmt.Errorf("organization %q does not match", c.Owner)
	}
	username := strings.TrimSpace(c.Name)
	if username == "" {
		return domain.Principal{}, errors.New("token has no username")
	}
	return domain.Principal{
		UserID:       username,
		Organization: strings.TrimSpace(c.Owner),
		Roles:        c.roles(),
	}, nil
}

func (v *Verifier) keyFunc(ctx context.Context) jwt.Keyfunc {
	return func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("token has no kid")
		}
		return v.keys.key(ctx, kid)
	}
}
