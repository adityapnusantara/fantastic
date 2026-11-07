// Package faqscrape is the Go productionization of the task 1.3 python
// spike scraper (OpenSpec change add-faq-rag-search, task 4.1, design
// D5/D6/D8): crawl the linkaja.id /news FAQ surface politely, extract Q&A
// units, and emit the curated corpus JSON (inventory spec 6.1).
//
// The python script (scripts/spike/scrape_faq.py, deleted by this task) is
// the behavioral reference; the corpus schema, content_hash rule (spec 6.3)
// and the cache layout (spec 7.5) are cross-language contracts — the Go
// output must be byte-compatible with the python output modulo the
// corpus-level scraped_at. Intentional deviations are documented in
// comments where they occur (see checkRobots for the only one).
package faqscrape

import (
	"io"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
)

// eventKind mirrors the python EventParser's flat event stream
// ("open"/"close"/"void"/"text").
type eventKind int

const (
	eventOpen eventKind = iota
	eventClose
	eventVoid
	eventText
)

// event is one flat parse event. Attrs is the element's attribute map
// (last duplicate wins, matching python's dict(attrs)); Text carries the
// payload of an eventText.
type event struct {
	Kind  eventKind
	Tag   string
	Attrs map[string]string
	Text  string
}

func (k eventKind) String() string {
	switch k {
	case eventOpen:
		return "open"
	case eventClose:
		return "close"
	case eventVoid:
		return "void"
	case eventText:
		return "text"
	}
	return "?"
}

// voidTags is the python VOID_TAGS set: elements that never open a scope.
// A start tag of one of these, or ANY self-closed tag (`<div/>`), becomes
// an eventVoid — exactly what python's handle_starttag/handle_startendtag
// produced.
var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// parseEvents tokenizes HTML into the flat event stream, replicating the
// python EventParser:
//   - charrefs converted in text and attribute values (x/net/html's
//     Token() unescapes both; python used convert_charrefs=True);
//   - comments and doctypes dropped (python never overrode those handlers);
//   - text inside <script>/<style> suppressed (python's _suppress counter;
//     the tokenizer hands script/style content over as raw text tokens);
//   - void tags and self-closed tags never open a scope.
func parseEvents(r io.Reader) ([]event, error) {
	z := xhtml.NewTokenizer(r)
	events := make([]event, 0, 4096)
	suppress := 0
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			if z.Err() == io.EOF {
				return events, nil
			}
			return events, z.Err()
		}
		switch tt {
		case xhtml.TextToken:
			if suppress > 0 {
				continue
			}
			t := z.Token()
			events = append(events, event{Kind: eventText, Text: t.Data})
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			t := z.Token()
			if tt == xhtml.StartTagToken && (t.Data == "script" || t.Data == "style") {
				suppress++
			}
			if tt == xhtml.SelfClosingTagToken || voidTags[t.Data] {
				events = append(events, event{Kind: eventVoid, Tag: t.Data, Attrs: attrMap(t)})
				continue
			}
			events = append(events, event{Kind: eventOpen, Tag: t.Data, Attrs: attrMap(t)})
		case xhtml.EndTagToken:
			t := z.Token()
			if (t.Data == "script" || t.Data == "style") && suppress > 0 {
				suppress--
			}
			events = append(events, event{Kind: eventClose, Tag: t.Data})
		default:
			// CommentToken, DoctypeToken, etc.: dropped, like python.
		}
	}
}

// attrMap converts a token's attributes to the python dict(attrs) shape:
// plain map, last duplicate attribute wins.
func attrMap(t xhtml.Token) map[string]string {
	if len(t.Attr) == 0 {
		return nil
	}
	m := make(map[string]string, len(t.Attr))
	for _, a := range t.Attr {
		m[a.Key] = a.Val
	}
	return m
}

// zeroWidthRE strips zero-width characters at EXTRACTION time only
// (measured: empty decorative headings containing U+200D). The
// content_hash norm() itself stays exactly the spec 6.3 rule; see
// faqindex.Norm.
var zeroWidthRE = regexp.MustCompile("[\u200b\u200c\u200d\u2060\ufeff]")

// collapseWhitespace replicates python's
// " ".join(ZERO_WIDTH_RE.sub("", s).split()): strip zero-width chars, then
// collapse every whitespace run to one ASCII space.
func collapseWhitespace(parts []string) string {
	return strings.Join(strings.Fields(zeroWidthRE.ReplaceAllString(strings.Join(parts, ""), "")), " ")
}

// findClassSpans returns (open, close) event index spans of every element
// whose class attribute contains classToken as a whole token. Tolerates
// unclosed children, exactly like the python implementation: on a close
// event the innermost matching open is popped, and unclosed spans end at
// the end of the stream.
func findClassSpans(events []event, classToken string) [][2]int {
	type frame struct {
		tag      string
		openIdx  int
		isTarget bool
	}
	var spans [][2]int
	var stack []frame
	for i, ev := range events {
		switch ev.Kind {
		case eventOpen:
			cls := ev.Attrs["class"]
			stack = append(stack, frame{tag: ev.Tag, openIdx: i, isTarget: hasToken(cls, classToken)})
		case eventClose:
			hit := -1
			for j := len(stack) - 1; j >= 0; j-- {
				if stack[j].tag == ev.Tag {
					hit = j
					break
				}
			}
			if hit == -1 {
				continue
			}
			if stack[hit].isTarget {
				spans = append(spans, [2]int{stack[hit].openIdx, i})
			}
			stack = stack[:hit]
		}
	}
	tail := len(events)
	for _, f := range stack {
		if f.isTarget {
			spans = append(spans, [2]int{f.openIdx, tail})
		}
	}
	// python sorts the spans before returning; order only matters for
	// outermost() and for stable iteration of listing cards, both of
	// which we replicate.
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j][0] < spans[j-1][0]; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
	return spans
}

// hasToken reports whether a space-separated class list contains classToken.
func hasToken(classAttr, classToken string) bool {
	for _, t := range strings.Fields(classAttr) {
		if t == classToken {
			return true
		}
	}
	return false
}

// outermost filters spans to those not contained in an earlier span.
func outermost(spans [][2]int) [][2]int {
	var out [][2]int
	for _, s := range spans {
		if len(out) > 0 && s[0] >= out[len(out)-1][0] && s[1] <= out[len(out)-1][1] {
			continue
		}
		out = append(out, s)
	}
	return out
}

// firstTagSpan returns the (open, close) span of the first element with
// the given tag name, or nil.
func firstTagSpan(events []event, tag string) ([2]int, bool) {
	type frame struct {
		tag     string
		openIdx int
	}
	var stack []frame
	for i, ev := range events {
		switch ev.Kind {
		case eventOpen:
			stack = append(stack, frame{tag: ev.Tag, openIdx: i})
		case eventClose:
			hit := -1
			for j := len(stack) - 1; j >= 0; j-- {
				if stack[j].tag == ev.Tag {
					hit = j
					break
				}
			}
			if hit == -1 {
				continue
			}
			if stack[hit].tag == tag {
				return [2]int{stack[hit].openIdx, i}, true
			}
			stack = stack[:hit]
		}
	}
	return [2]int{}, false
}

// collectText joins the text payload of every text event inside the span
// (zero-width stripped, whitespace collapsed).
func collectText(events []event, span [2]int) string {
	var buf []string
	for _, ev := range events[span[0]+1 : span[1]] {
		if ev.Kind == eventText {
			buf = append(buf, ev.Text)
		}
	}
	return collapseWhitespace(buf)
}
