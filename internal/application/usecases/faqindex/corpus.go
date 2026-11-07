package faqindex

import (
	"encoding/json"
	"fmt"
	"os"
)

// Corpus mirrors data/corpus/linkaja-faq.json (inventory spec 6.1).
type Corpus struct {
	SchemaVersion int    `json:"schema_version"`
	ScrapedAt     string `json:"scraped_at"`
	Source        string `json:"source"`
	Units         []Unit `json:"units"`
}

// Unit is one Q&A pair (design D4 primary chunking path) or one
// long-form fallback unit (question = page h1, answer = full body).
type Unit struct {
	ID          string `json:"id"`
	SourceURL   string `json:"source_url"`
	Question    string `json:"question"`
	Answer      string `json:"answer"`
	Category    string `json:"category"`
	Language    string `json:"language"`
	ContentHash string `json:"content_hash"`
	ScrapedAt   string `json:"scraped_at"`
}

// corpusSchemaVersion is the version this indexer accepts (spec 6: a
// consumer must reject a major version it does not know).
const corpusSchemaVersion = 1

// LoadCorpus reads and validates the curated corpus. Every unit's
// content_hash is recomputed per spec 6.3 and must match the stored
// value — the hashes are the upsert keys, so a mismatch means the corpus
// was tampered with (or the normalization rule drifted) and the run
// fails loudly instead of indexing under wrong identities.
func LoadCorpus(path string) (*Corpus, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read corpus: %w", err)
	}
	var c Corpus
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse corpus %s: %w", path, err)
	}
	if c.SchemaVersion != corpusSchemaVersion {
		return nil, fmt.Errorf("corpus %s: schema_version %d not supported (want %d)", path, c.SchemaVersion, corpusSchemaVersion)
	}
	if len(c.Units) == 0 {
		return nil, fmt.Errorf("corpus %s: no units", path)
	}
	seen := make(map[string]bool, len(c.Units))
	for i := range c.Units {
		u := &c.Units[i]
		if u.ID == "" || u.SourceURL == "" {
			return nil, fmt.Errorf("corpus %s: unit %d missing id or source_url", path, i)
		}
		if seen[u.ID] {
			return nil, fmt.Errorf("corpus %s: duplicate unit id %q", path, u.ID)
		}
		seen[u.ID] = true
		if want := UnitContentHash(u.Question, u.Answer); want != u.ContentHash {
			return nil, fmt.Errorf("corpus %s: unit %s content_hash mismatch: stored %s, computed per spec 6.3 %s", path, u.ID, u.ContentHash, want)
		}
	}
	return &c, nil
}
