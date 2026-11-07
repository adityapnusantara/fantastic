package faqindex

import (
	"strings"
	"testing"
)

// TestUnitContentHashConformance reproduces the inventory spec 6.3
// conformance vector — the python 1.3 scraper and this Go indexer must
// hash bit-identically.
func TestUnitContentHashConformance(t *testing.T) {
	const question = "Berapa limit saldo dan transaksi LinkAja?"
	const answer = "Limit saldo akun terdaftar adalah Rp 2.000.000.\nLimit akun Full Service adalah Rp 20.000.000."
	const want = "69c5d36426d9f86dfc0d2dbd14b572f2b3f2400b91f51c1f6adcffcd8fbce3a3"

	if got := UnitContentHash(question, answer); got != want {
		t.Fatalf("UnitContentHash conformance: got %s, want %s", got, want)
	}
}

// TestNormMatchesPythonSplitJoin pins the norm() rule: whitespace runs
// collapse to one space, edges trimmed.
func TestNormMatchesPythonSplitJoin(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  hello   world  ", "hello world"},
		{"a\n\tb \t c", "a b c"},
		{"\u00a0nbsp\u2003wide", "nbsp wide"}, // unicode whitespace
		{"", ""},
	}
	for _, c := range cases {
		if got := Norm(c.in); got != c.want {
			t.Errorf("Norm(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDocumentContentHashIsDeterministic: article identity derives from
// its unit hashes, stable across runs, sensitive to content.
func TestDocumentContentHashIsDeterministic(t *testing.T) {
	a := []string{"h1", "h2", "h3"}
	if DocumentContentHash(a) != DocumentContentHash([]string{"h1", "h2", "h3"}) {
		t.Fatal("same unit hashes must give the same document hash")
	}
	if DocumentContentHash(a) == DocumentContentHash([]string{"h3", "h2", "h1"}) {
		t.Fatal("unit order must matter (corpus order is the canonical order)")
	}
	if DocumentContentHash(a) == DocumentContentHash([]string{"h1", "h2"}) {
		t.Fatal("content change must change the document hash")
	}
}

// TestChunkUnitPrimaryPath: a normal unit embeds whole; the chunk text
// is question + "\n\n" + answer and the chunk hash is the unit hash
// (spec 6.2: chunk 0's hash equals the document entry hash).
func TestChunkUnitPrimaryPath(t *testing.T) {
	u := &Unit{
		ID:          "some-article_01",
		Question:    "Berapa biaya top up?",
		Answer:      "Top up gratis.\nSemua channel.",
		ContentHash: UnitContentHash("Berapa biaya top up?", "Top up gratis.\nSemua channel."),
	}
	chunks, err := ChunkUnit(u)
	if err != nil {
		t.Fatalf("ChunkUnit: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("primary path must yield 1 chunk, got %d", len(chunks))
	}
	c := chunks[0]
	if c.Index != 0 {
		t.Errorf("chunk index = %d, want 0", c.Index)
	}
	if c.Windowed {
		t.Error("primary path chunk must not be flagged windowed")
	}
	if want := u.Question + "\n\n" + u.Answer; c.Text != want {
		t.Errorf("embedded text = %q, want %q", c.Text, want)
	}
	if c.ContentHash != u.ContentHash {
		t.Errorf("chunk hash = %s, want the unit hash %s", c.ContentHash, u.ContentHash)
	}
}

// longTestAnswer builds a multi-line answer of roughly n estimated
// tokens.
func longTestAnswer(n int) string {
	var sb strings.Builder
	line := strings.Repeat("kata ", 40) // ~200 chars, ~57 estimated tokens
	for sb.Len()/4 < n {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// TestChunkUnitWindowFallback: an >800-token answer is split into
// windows inside D4's bounds, with overlap between consecutive windows
// and hashes over the window text alone (deviation documented for
// 1.4/3.1).
func TestChunkUnitWindowFallback(t *testing.T) {
	answer := longTestAnswer(1000) // ~1000 estimated tokens > 800
	question := "Artikel panjang"
	u := &Unit{
		ID:          "long-article_01",
		Question:    question,
		Answer:      answer,
		ContentHash: UnitContentHash(question, answer),
	}
	chunks, err := ChunkUnit(u)
	if err != nil {
		t.Fatalf("ChunkUnit: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("long-form unit must split, got %d chunk(s)", len(chunks))
	}
	for i, c := range chunks {
		if c.Index != i {
			t.Errorf("chunk %d: index = %d (indices must be contiguous)", i, c.Index)
		}
		if !c.Windowed {
			t.Errorf("chunk %d: expected Windowed=true", i)
		}
		if !strings.HasPrefix(c.Text, question+"\n\n") {
			t.Errorf("chunk %d: embedded text must carry the question prefix (D4)", i)
		}
		if est := EstimateTokens(c.Text); est > 800 {
			t.Errorf("chunk %d: embedded text ~%d tokens exceeds the 800-token window bound", i, est)
		}
		if want := ContentHash(Norm(windowOf(c.Text, question))); c.ContentHash != want {
			t.Errorf("chunk %d: hash must be sha256 over Norm(window text) alone (spec 6.3)", i)
		}
		if c.ContentHash == u.ContentHash {
			t.Errorf("chunk %d: window hash must differ from the unit hash (deviation to document)", i)
		}
	}
	// Overlap: consecutive windows share the carried trailing lines —
	// the start of window i+1 appears inside window i.
	for i := 1; i < len(chunks); i++ {
		prev := windowOf(chunks[i-1].Text, question)
		cur := windowOf(chunks[i].Text, question)
		firstLine := strings.SplitN(cur, "\n", 2)[0]
		if !strings.Contains(prev, firstLine) {
			t.Errorf("windows %d->%d: no overlap (first line of window %d not found in window %d)", i-1, i, i, i-1)
		}
	}
}

// windowOf strips the question prefix from an embedded chunk text.
func windowOf(embedded, question string) string {
	return strings.TrimPrefix(embedded, question+"\n\n")
}

// TestSplitWindowsOversizedLine: a single line longer than the window
// target is hard-split with guaranteed progress (no wedge).
func TestSplitWindowsOversizedLine(t *testing.T) {
	line := strings.Repeat("x", 20000) // ~5700 estimated tokens in one line
	windows, err := splitWindows(line, windowTokenTarget, windowOverlapTokens, minTailTokens)
	if err != nil {
		t.Fatalf("splitWindows: %v", err)
	}
	if len(windows) < 3 {
		t.Fatalf("oversized line must be hard-split, got %d windows", len(windows))
	}
	for i, w := range windows {
		if est := EstimateTokens(w); est > windowTokenTarget {
			t.Errorf("window %d: ~%d tokens > target %d", i, est, windowTokenTarget)
		}
	}
}

// TestEstimateTokensCalibration: the estimator stays within ~10% of the
// measured API count for the longest corpus unit (3211 chars embedded
// text = 927 prompt tokens, measured 2026-11-06).
func TestEstimateTokensCalibration(t *testing.T) {
	text := strings.Repeat("Limit saldo akun terdaftar adalah Rp 2.000.000. ", 67) // 3216 chars
	got := EstimateTokens(text)
	const measured = 927
	if got < measured*9/10 || got > measured*11/10 {
		t.Fatalf("EstimateTokens(%d chars) = %d, want within 10%% of the measured %d", len(text), got, measured)
	}
}
