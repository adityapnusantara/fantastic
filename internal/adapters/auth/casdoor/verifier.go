package casdoor

import (
	"context"
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

	TokenType string `json:"tokenType"`
	Owner     string `json:"owner"`
	Name      string `json:"name"`
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
