package casdoor

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

var errUnknownKey = errors.New("signing key not found")

// jwksCache holds RSA public keys by kid. It fetches lazily, refetches at most
// once per minInterval when an unknown kid is requested, and keeps serving
// cached keys when the endpoint fails.
type jwksCache struct {
	url         string
	client      *http.Client
	now         func() time.Time
	minInterval time.Duration

	fetchMu     sync.Mutex // serialises refetches so concurrent callers coalesce
	lastFetch   time.Time  // guarded by fetchMu
	fetchedOnce bool       // guarded by fetchMu

	mu   sync.RWMutex // guards keys; never held across network I/O
	keys map[string]*rsa.PublicKey
}

func newJWKSCache(url string, o options) *jwksCache {
	return &jwksCache{url: url, client: o.client, now: o.clock, minInterval: o.minRefetch}
}

func (c *jwksCache) cached(kid string) (*rsa.PublicKey, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	k, ok := c.keys[kid]
	return k, ok
}

// key returns the public key for kid, refetching the set when kid is unknown
// and the rate limit allows it.
func (c *jwksCache) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if k, ok := c.cached(kid); ok {
		return k, nil
	}

	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	// Another caller may have refreshed the set while we waited for the lock.
	if k, ok := c.cached(kid); ok {
		return k, nil
	}
	if c.fetchedOnce && c.now().Sub(c.lastFetch) < c.minInterval {
		return nil, errUnknownKey
	}
	c.lastFetch = c.now()
	c.fetchedOnce = true

	keys, err := c.fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("refresh jwks: %w", err)
	}
	c.mu.Lock()
	c.keys = keys
	c.mu.Unlock()

	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, errUnknownKey
}

func (c *jwksCache) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks endpoint returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSResponseBytes))
	if err != nil {
		return nil, err
	}
	return parseJWKS(body)
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func parseJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		pub, err := k.publicKey()
		if err != nil {
			continue // skip a malformed key; others may still be usable
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return nil, errors.New("jwks contains no usable RSA keys")
	}
	return keys, nil
}

func (k jwk) publicKey() (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eb, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	e := new(big.Int).SetBytes(eb)
	if !e.IsInt64() || e.Int64() <= 1 || e.Int64() > 1<<31-1 {
		return nil, errors.New("invalid exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: int(e.Int64())}, nil
}
