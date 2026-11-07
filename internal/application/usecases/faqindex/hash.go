// Package faqindex implements the FAQ corpus embedding pipeline
// (OpenSpec change add-faq-rag-search, task 2.2, design D2/D4/D5):
// chunk the curated corpus, embed the chunks with the configured model,
// upsert them into pgvector idempotently by content_hash, and emit the
// index manifest (inventory spec 6.2) as the integrity reference.
//
// Task 4.1/4.2 absorb this package into the CLI and the formal
// FAQIndexer/FAQSearcher ports.
package faqindex

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Norm normalizes text per inventory spec 6.3: every run of Unicode
// whitespace collapses to a single ASCII space and leading/trailing
// whitespace is trimmed. strings.Fields is Unicode-aware, matching the
// python scraper's " ".join(s.split()) bit for bit.
func Norm(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// UnitContentHash hashes one corpus unit per spec 6.3:
// lowercase-hex sha256 over Norm(question) + "\n" + Norm(answer).
func UnitContentHash(question, answer string) string {
	return ContentHash(Norm(question) + "\n" + Norm(answer))
}

// ContentHash hashes a single piece of text per spec 6.3. Used for the
// D4 long-form window fallback, whose windows hash the window text
// alone (no question prefix, no join).
func ContentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// DocumentContentHash derives the faq_documents content_hash for one
// article (one row per source_url). Chosen identity (documented for
// 2.4/3.1/4.1): lowercase-hex sha256 over the article's unit
// content_hashes joined with "\n" in corpus order — content-faithful, so
// an edited article gets a new document row and the stale row is
// garbage-collected by the store instead of silently persisting.
func DocumentContentHash(unitHashes []string) string {
	return ContentHash(strings.Join(unitHashes, "\n"))
}
