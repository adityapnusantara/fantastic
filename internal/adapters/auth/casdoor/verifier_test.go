package casdoor_test

import (
	"context"
	"crypto/rsa"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyAcceptsValidToken(t *testing.T) {
	clock := newFakeClock()
	key := newKey(t)
	server := newJWKSServer(t, map[string]*rsa.PrivateKey{"k1": key})
	v := newTestVerifier(t, server.URL, clock)

	token := signRS256(t, "k1", key, validClaims())
	p, err := v.Verify(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, "aditya", p.UserID)
	assert.Equal(t, testOrg, p.Organization)
}

func TestVerifyRejectsMissingAndEmpty(t *testing.T) {
	clock := newFakeClock()
	key := newKey(t)
	server := newJWKSServer(t, map[string]*rsa.PrivateKey{"k1": key})
	v := newTestVerifier(t, server.URL, clock)

	for _, raw := range []string{"", "   "} {
		_, err := v.Verify(context.Background(), raw)
		assert.Error(t, err)
	}
}

func TestVerifyRejectsNonBearerSchemeIndependent(t *testing.T) {
	// middleware handles the scheme; the verifier rejects malformed JWTs
	clock := newFakeClock()
	key := newKey(t)
	server := newJWKSServer(t, map[string]*rsa.PrivateKey{"k1": key})
	v := newTestVerifier(t, server.URL, clock)

	_, err := v.Verify(context.Background(), "not-a-jwt")
	assert.Error(t, err)
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	clock := newFakeClock()
	key := newKey(t)
	server := newJWKSServer(t, map[string]*rsa.PrivateKey{"k1": key})
	v := newTestVerifier(t, server.URL, clock)

	claims := validClaims()
	claims["exp"] = testNow.Add(-time.Hour).Unix()
	token := signRS256(t, "k1", key, claims)

	_, err := v.Verify(context.Background(), token)
	assert.Error(t, err)
}

func TestVerifyRejectsWrongSignature(t *testing.T) {
	clock := newFakeClock()
	key := newKey(t)
	other := newKey(t)
	server := newJWKSServer(t, map[string]*rsa.PrivateKey{"k1": key})
	v := newTestVerifier(t, server.URL, clock)

	token := signRS256(t, "k1", other, validClaims())
	_, err := v.Verify(context.Background(), token)
	assert.Error(t, err)
}

func TestVerifyRejectsWrongAudience(t *testing.T) {
	clock := newFakeClock()
	key := newKey(t)
	server := newJWKSServer(t, map[string]*rsa.PrivateKey{"k1": key})
	v := newTestVerifier(t, server.URL, clock)

	claims := validClaims()
	claims["aud"] = []string{"other-app"}
	token := signRS256(t, "k1", key, claims)

	_, err := v.Verify(context.Background(), token)
	assert.Error(t, err)
}

func TestVerifyRejectsWrongIssuer(t *testing.T) {
	clock := newFakeClock()
	key := newKey(t)
	server := newJWKSServer(t, map[string]*rsa.PrivateKey{"k1": key})
	v := newTestVerifier(t, server.URL, clock)

	claims := validClaims()
	claims["iss"] = "https://evil.example.com"
	token := signRS256(t, "k1", key, claims)

	_, err := v.Verify(context.Background(), token)
	assert.Error(t, err)
}

func TestVerifyRejectsRefreshAndIDTokens(t *testing.T) {
	clock := newFakeClock()
	key := newKey(t)
	server := newJWKSServer(t, map[string]*rsa.PrivateKey{"k1": key})
	v := newTestVerifier(t, server.URL, clock)

	for _, typ := range []string{"refresh-token", "id-token", ""} {
		claims := validClaims()
		if typ != "" {
			claims["tokenType"] = typ
		} else {
			delete(claims, "tokenType")
		}
		token := signRS256(t, "k1", key, claims)
		_, err := v.Verify(context.Background(), token)
		assert.Error(t, err, "tokenType %q must be rejected", typ)
	}
}

func TestVerifyRejectsWrongOrganization(t *testing.T) {
	clock := newFakeClock()
	key := newKey(t)
	server := newJWKSServer(t, map[string]*rsa.PrivateKey{"k1": key})
	v := newTestVerifier(t, server.URL, clock)

	claims := validClaims()
	claims["owner"] = "other-org"
	token := signRS256(t, "k1", key, claims)

	_, err := v.Verify(context.Background(), token)
	assert.Error(t, err)
}

func TestKeyRotationRefetchesOnce(t *testing.T) {
	clock := newFakeClock()
	k1 := newKey(t)
	k2 := newKey(t)
	server := newJWKSServer(t, map[string]*rsa.PrivateKey{"k1": k1})
	v := newTestVerifier(t, server.URL, clock)

	// token signed with the cached key works with no refetch
	first := signRS256(t, "k1", k1, validClaims())
	_, err := v.Verify(context.Background(), first)
	require.NoError(t, err)
	hitsAfterFirst := server.hitCount()

	// rotation: new key published, token signed with it
	server.addKey("k2", k2)
	clock.Advance(11 * time.Second) // past the refetch rate limit
	rotated := signRS256(t, "k2", k2, validClaims())
	p, err := v.Verify(context.Background(), rotated)
	require.NoError(t, err)
	assert.Equal(t, "aditya", p.UserID)
	assert.Greater(t, server.hitCount(), hitsAfterFirst, "unknown kid triggers a refetch")

	// subsequent unknown-kid attempts within the rate limit do not refetch again
	server.addKey("k3", newKey(t))
	unknown := signRS256(t, "k3", newKey(t), validClaims())
	_, err = v.Verify(context.Background(), unknown)
	assert.Error(t, err)
	assert.Equal(t, server.hitCount(), hitsAfterFirst+1, "refetch is rate-limited to one attempt")
}

func TestCachedKeysSurviveJWKSEndpointOutage(t *testing.T) {
	clock := newFakeClock()
	key := newKey(t)
	server := newJWKSServer(t, map[string]*rsa.PrivateKey{"k1": key})
	v := newTestVerifier(t, server.URL, clock)

	token := signRS256(t, "k1", key, validClaims())
	_, err := v.Verify(context.Background(), token)
	require.NoError(t, err)

	server.setStatus(http.StatusInternalServerError)
	_, err = v.Verify(context.Background(), token)
	assert.NoError(t, err, "cached key must keep working while the endpoint is down")
}
