package casdoor_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"

	"github.com/adityapratamanusantara/fantastic/internal/adapters/auth/casdoor"
)

const (
	testIssuer   = "https://casdoor.example.com"
	testAudience = "fantastic-app"
	testOrg      = "pribadi"
)

var testNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return k
}

// jwksServer serves a mutable key set and counts requests.
type jwksServer struct {
	*httptest.Server

	hits   atomic.Int64
	status int

	mu     sync.Mutex
	keys   map[string]*rsa.PublicKey
	called []string
}

func newJWKSServer(t *testing.T, keys map[string]*rsa.PrivateKey) *jwksServer {
	t.Helper()
	s := &jwksServer{keys: map[string]*rsa.PublicKey{}, status: http.StatusOK}
	for kid, k := range keys {
		s.keys[kid] = &k.PublicKey
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *jwksServer) serve(w http.ResponseWriter, r *http.Request) {
	s.hits.Add(1)
	s.mu.Lock()
	s.called = append(s.called, r.URL.Path)
	status := s.status
	keys := s.keys
	s.mu.Unlock()
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	set := make([]map[string]string, 0, len(keys))
	for kid, pub := range keys {
		set = append(set, map[string]string{
			"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": set})
}

func (s *jwksServer) addKey(kid string, k *rsa.PrivateKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[kid] = &k.PublicKey
}

func (s *jwksServer) setStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = code
}

func (s *jwksServer) hitCount() int64 { return s.hits.Load() }

// fakeClock is a race-safe, advanceable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: testNow} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// toServer sends every request to the test server, whatever host the verifier
// derived from the issuer, so tests can serve the JWKS locally while tokens
// carry the production-looking testIssuer.
type toServer struct{ target *url.URL }

func (t toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = t.target.Scheme, t.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func newTestVerifier(t *testing.T, serverURL string, clock *fakeClock, opts ...casdoor.Option) *casdoor.Verifier {
	t.Helper()
	target, err := url.Parse(serverURL)
	require.NoError(t, err)
	cfg := casdoor.Config{
		Issuer:       testIssuer,
		Audience:     testAudience,
		Organization: testOrg,
		JWKSURL:      testIssuer + "/.well-known/jwks",
	}
	opts = append([]casdoor.Option{
		casdoor.WithClock(clock.Now),
		casdoor.WithHTTPClient(&http.Client{Transport: toServer{target: target}}),
	}, opts...)
	return casdoor.NewVerifier(cfg, opts...)
}

// validClaims returns a claim set that passes every check.
func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss":       testIssuer,
		"aud":       []string{testAudience},
		"exp":       testNow.Add(time.Hour).Unix(),
		"iat":       testNow.Add(-time.Minute).Unix(),
		"tokenType": "access-token",
		"owner":     testOrg,
		"name":      "aditya",
	}
}

func signRS256(t *testing.T, kid string, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	require.NoError(t, err)
	return s
}
