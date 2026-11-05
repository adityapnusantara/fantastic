// Package casdoor verifies Casdoor-issued bearer access tokens locally:
// RS256 signature via a cached JWKS, issuer, audience, expiry, token type
// and organization.
package casdoor

import (
	"net/http"
	"time"
)

// Config holds the Casdoor settings the verifier checks tokens against.
type Config struct {
	// Issuer must equal the token "iss" claim (Casdoor's configured origin).
	Issuer string
	// Audience is the Casdoor application client id expected in "aud".
	Audience string
	// Organization must equal the token "owner" claim.
	Organization string
	// JWKSURL is where Casdoor publishes its signing keys.
	JWKSURL string
}

const (
	defaultHTTPTimeout   = 5 * time.Second
	defaultMinRefetch    = 10 * time.Second
	defaultLeeway        = 5 * time.Second
	maxJWKSResponseBytes = 1 << 20
)

type options struct {
	client     *http.Client
	clock      func() time.Time
	minRefetch time.Duration
}

// Option customises a verifier; mainly useful in tests.
type Option func(*options)

// WithHTTPClient replaces the HTTP client used to fetch the JWKS.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) { o.client = c }
}

// WithClock replaces the time source used for expiry checks and refetch rate limiting.
func WithClock(now func() time.Time) Option {
	return func(o *options) { o.clock = now }
}

// WithMinRefetchInterval sets the minimum time between JWKS refetches.
func WithMinRefetchInterval(d time.Duration) Option {
	return func(o *options) { o.minRefetch = d }
}

func newOptions(opts []Option) options {
	o := options{
		client:     &http.Client{Timeout: defaultHTTPTimeout},
		clock:      time.Now,
		minRefetch: defaultMinRefetch,
	}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
