package config

import (
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Task 2.4: faq.search.top_k defaults to 5 (design D7) and never stays at
// zero or negative.
func TestLoadFaqSearchTopKDefaults(t *testing.T) {
	path := writeTemp(t, "server: {}\nfaq: {}\n")
	cfg, warnings, err := Load(path)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, DefaultFaqSearchTopK, cfg.FAQ.Search.TopK)
}

func TestLoadFaqSearchTopKExplicitValueHonored(t *testing.T) {
	path := writeTemp(t, "faq:\n  search:\n    top_k: 3\n")
	cfg, warnings, err := Load(path)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	assert.Equal(t, 3, cfg.FAQ.Search.TopK)
}

// Task 4.3: the embedding block carries the vector dimension (design D2:
// a vector column is a (model, dimension) pair).
func TestLoadFaqEmbeddingDimsDefaults(t *testing.T) {
	path := writeTemp(t, "faq: {}\n")
	cfg, _, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, DefaultFaqEmbeddingDims, cfg.FAQ.Embedding.Dims)
}

func TestLoadFaqEmbeddingDimsExplicitValueHonored(t *testing.T) {
	path := writeTemp(t, "faq:\n  embedding:\n    dims: 512\n")
	cfg, _, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, 512, cfg.FAQ.Embedding.Dims)
}

// Task 4.3: faq.scrape defaults to the design D6 politeness policy.
func TestLoadFaqScrapeDefaults(t *testing.T) {
	path := writeTemp(t, "faq: {}\n")
	cfg, _, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"linkaja.id", "www.linkaja.id"}, cfg.FAQ.Scrape.AllowedHosts)
	assert.Equal(t, DefaultFaqScrapeRequestDelayMS, cfg.FAQ.Scrape.RequestDelayMS)
	assert.GreaterOrEqual(t, cfg.FAQ.Scrape.RequestDelayMS, 1000, "polite default is >=1 req/s (design D6)")
	assert.Equal(t, "fantastic-faq-spike/0.1 (personal project; contact via repo)", cfg.FAQ.Scrape.UserAgent)
	assert.Equal(t, "data/raw", cfg.FAQ.Scrape.CacheDir)
}

func TestLoadFaqScrapeExplicitValuesHonored(t *testing.T) {
	path := writeTemp(t, `
faq:
  scrape:
    allowed_hosts: [example.com]
    request_delay_ms: 2500
    user_agent: "custom-ua/1.0"
    cache_dir: /tmp/raw-cache
`)
	cfg, _, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"example.com"}, cfg.FAQ.Scrape.AllowedHosts)
	assert.Equal(t, 2500, cfg.FAQ.Scrape.RequestDelayMS)
	assert.Equal(t, "custom-ua/1.0", cfg.FAQ.Scrape.UserAgent)
	assert.Equal(t, "/tmp/raw-cache", cfg.FAQ.Scrape.CacheDir)
}

func TestLoadFaqScrapeNegativeDelayRejected(t *testing.T) {
	path := writeTemp(t, "faq:\n  scrape:\n    request_delay_ms: -5\n")
	_, _, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "faq.scrape.request_delay_ms")
}

// Task 4.3: faq.corpus defaults to the design D5 artifact paths.
func TestLoadFaqCorpusDefaults(t *testing.T) {
	path := writeTemp(t, "faq: {}\n")
	cfg, _, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "data/corpus/linkaja-faq.json", cfg.FAQ.Corpus.Path)
	assert.Equal(t, "data/corpus/index-manifest.json", cfg.FAQ.Corpus.ManifestPath)
}

func TestLoadFaqCorpusExplicitValuesHonored(t *testing.T) {
	path := writeTemp(t, `
faq:
  corpus:
    path: /tmp/other-corpus.json
    manifest_path: /tmp/other-manifest.json
`)
	cfg, _, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/other-corpus.json", cfg.FAQ.Corpus.Path)
	assert.Equal(t, "/tmp/other-manifest.json", cfg.FAQ.Corpus.ManifestPath)
}

// Task 4.3: top_k is validated to 1..20 on load.
func TestLoadFaqSearchTopKAboveMaxRejected(t *testing.T) {
	path := writeTemp(t, "faq:\n  search:\n    top_k: 21\n")
	_, _, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "faq.search.top_k")
}

func TestLoadFaqSearchTopKBoundariesAccepted(t *testing.T) {
	for _, k := range []int{MinFaqSearchTopK, MaxFaqSearchTopK} {
		path := writeTemp(t, "faq:\n  search:\n    top_k: "+strconv.Itoa(k)+"\n")
		cfg, _, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, k, cfg.FAQ.Search.TopK)
	}
}

// Validate also guards programmatically constructed values (zero struct).
func TestFaqConfigValidateZeroStructRejected(t *testing.T) {
	var c FaqConfig
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "faq.search.top_k")
}

// Task 4.3 contract: the shipped config.yaml references the credential via
// ${OPENAI_API_KEY} and never carries a plaintext secret.
func TestShippedConfigUsesEnvRefForFAQKey(t *testing.T) {
	raw, err := os.ReadFile("../config.yaml")
	require.NoError(t, err)
	s := string(raw)
	assert.Contains(t, s, "api_key: ${OPENAI_API_KEY}")
	assert.NotContains(t, s, "sk-")
}
