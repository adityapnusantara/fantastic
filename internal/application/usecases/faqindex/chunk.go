package faqindex

import (
	"fmt"
	"math"
	"strings"
)

// Chunk is one embeddable unit produced by ChunkUnit (design D4).
type Chunk struct {
	// Index is the 0-based ordinal of the chunk within its corpus unit
	// (manifest documents[].chunks[].chunk_index, spec 6.2).
	Index int
	// Text is the embedded text: question + "\n\n" + payload, where the
	// payload is the full answer (primary path) or one window of it
	// (long-form fallback). The question prefix rides on every window:
	// D4 credits the question text with materially higher hit rates.
	Text string
	// ContentHash is the chunk identity per spec 6.3: the unit's own
	// content_hash on the 1:1 path (chunk 0 == document hash, spec 6.2);
	// for windows, sha256 over Norm(window text) alone — no question
	// prefix, no join — which necessarily differs from the unit hash.
	// This deviation is documented for tasks 1.4/3.1 (they pin chunks via
	// the manifest, which carries the real per-chunk hashes).
	ContentHash string
	// Windowed reports whether this chunk came from the long-form
	// window fallback.
	Windowed bool
}

// Chunking knobs (design D4: 400-800 token windows with 10-15% overlap
// as the long-form fallback; one chunk per Q&A unit otherwise).
const (
	// longFormTokenLimit: a unit whose answer exceeds this estimated
	// token count is split into windows instead of embedded whole.
	longFormTokenLimit = 800
	// windowTokenTarget packs windows around 500 estimated tokens —
	// inside D4's 400-800 range with headroom for the question prefix
	// and greedy line-boundary slack.
	windowTokenTarget = 500
	// windowOverlapTokens is carried from one window into the next as
	// whole trailing lines (~14% of the 500-token target, inside D4's
	// 10-15% band).
	windowOverlapTokens = 70
	// minTailTokens merges an undersized trailing window into the
	// previous one when the merge stays within D4's 800-token ceiling.
	minTailTokens = 250
	// charsPerToken calibrates EstimateTokens for Indonesian text with
	// text-embedding-3-small (measured 2026-11-06: a 3211-char unit
	// embedded text = 927 prompt tokens -> 3.47 chars/token).
	charsPerToken = 3.5
	// maxEmbeddedTokens is the model's context limit for one embedding
	// input (text-embedding-3-small: 8191); a guard, never hit by this
	// corpus (worst window ~500 + question prefix).
	maxEmbeddedTokens = 8000
)

// EstimateTokens approximates text-embedding-3-small's tokenizer for
// Indonesian text. Calibrated against the real API (see charsPerToken);
// good enough for the D4 windowing thresholds, which are themselves
// approximate ("~800 tokens").
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return int(math.Ceil(float64(len([]rune(s))) / charsPerToken))
}

// ChunkUnit turns one corpus unit into 1..N chunks (design D4): one
// chunk per Q&A unit on the primary path, bounded overlapping windows
// when the answer is long-form.
func ChunkUnit(u *Unit) ([]Chunk, error) {
	if strings.TrimSpace(u.Answer) == "" {
		return nil, fmt.Errorf("unit %s: empty answer", u.ID)
	}
	if EstimateTokens(u.Answer) <= longFormTokenLimit {
		return []Chunk{{
			Index:       0,
			Text:        u.Question + "\n\n" + u.Answer,
			ContentHash: u.ContentHash,
		}}, nil
	}

	windows, err := splitWindows(u.Answer, windowTokenTarget, windowOverlapTokens, minTailTokens)
	if err != nil {
		return nil, fmt.Errorf("unit %s: split long-form answer: %w", u.ID, err)
	}
	chunks := make([]Chunk, 0, len(windows))
	for i, w := range windows {
		if tok := EstimateTokens(u.Question) + EstimateTokens(w); tok > maxEmbeddedTokens {
			return nil, fmt.Errorf("unit %s: window %d exceeds the embedding input limit (%d tokens)", u.ID, i, tok)
		}
		chunks = append(chunks, Chunk{
			Index:       i,
			Text:        u.Question + "\n\n" + w,
			ContentHash: ContentHash(Norm(w)),
			Windowed:    true,
		})
	}
	return chunks, nil
}

// splitWindows packs answer lines (block boundaries, per spec 6.1
// storage) into windows of at most targetTokens estimated tokens,
// carrying whole trailing lines worth about overlapTokens into the next
// window so no block is cut at a boundary.
func splitWindows(text string, targetTokens, overlapTokens, minTailTokens int) ([]string, error) {
	var windows []string
	var cur []string
	curTokens := 0

	flush := func() {
		if len(cur) == 0 {
			return
		}
		windows = append(windows, strings.Join(cur, "\n"))
		// Overlap: carry whole trailing lines totaling <= overlapTokens
		// into the next window. A single oversized line is not carried
		// (it would stall progress); boundary blocks reappear verbatim.
		var tail []string
		tailTokens := 0
		for i := len(cur) - 1; i >= 0; i-- {
			t := EstimateTokens(cur[i])
			if tailTokens+t > overlapTokens {
				break
			}
			tail = append([]string{cur[i]}, tail...)
			tailTokens += t
		}
		cur = tail
		curTokens = tailTokens
	}

	for _, line := range strings.Split(text, "\n") {
		for _, piece := range splitOversizedLine(line, targetTokens) {
			t := EstimateTokens(piece)
			if curTokens > 0 && curTokens+t > targetTokens {
				flush()
			}
			cur = append(cur, piece)
			curTokens += t
		}
	}
	if len(cur) > 0 {
		windows = append(windows, strings.Join(cur, "\n"))
	}

	// Merge an undersized trailing window into the previous one when the
	// combined window stays within D4's 800-token ceiling. The overlap
	// tail of the previous window is the head of the trailing one, so it
	// is stripped before joining (no duplicated lines inside a window).
	if len(windows) >= 2 {
		last := EstimateTokens(windows[len(windows)-1])
		prev := EstimateTokens(windows[len(windows)-2])
		if last < minTailTokens && prev+last <= longFormTokenLimit {
			prevLines := strings.Split(windows[len(windows)-2], "\n")
			lastLines := strings.Split(windows[len(windows)-1], "\n")
			shared := 0
			for shared < len(prevLines) && shared < len(lastLines) && prevLines[len(prevLines)-1-shared] == lastLines[shared] {
				shared++
			}
			windows[len(windows)-2] = windows[len(windows)-2] + "\n" + strings.Join(lastLines[shared:], "\n")
			windows = windows[:len(windows)-1]
		}
	}
	return windows, nil
}

// splitOversizedLine hard-splits a single line that exceeds the window
// target by runes (10% rune overlap) so window packing always makes
// progress. Not exercised by the current corpus (longest block line is
// far below the target); exists so a pathological page cannot wedge the
// indexer.
func splitOversizedLine(line string, targetTokens int) []string {
	if EstimateTokens(line) <= targetTokens {
		return []string{line}
	}
	runes := []rune(line)
	maxRunes := int(float64(targetTokens) * charsPerToken)
	overlapRunes := maxRunes / 10
	var pieces []string
	for start := 0; start < len(runes); {
		end := start + maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		pieces = append(pieces, string(runes[start:end]))
		if end == len(runes) {
			break
		}
		next := end - overlapRunes
		if next <= start { // guarantee forward progress
			next = start + 1
		}
		start = next
	}
	return pieces
}
