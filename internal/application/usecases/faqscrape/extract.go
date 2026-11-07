package faqscrape

import (
	"regexp"
	"net/url"
	"strings"
)

// questionTags / blockTags are the python QUESTION_TAGS / BLOCK_TAGS sets.
// Measured structure (probed 2026-11-06, python scraper header): question
// headings inside the article rich text mix h1/h2/h3 levels, so all three
// are question boundaries scoped to the w-richtext container; h4-h6 are
// plain text blocks.
var questionTags = map[string]bool{"h1": true, "h2": true, "h3": true}

var blockTags = map[string]bool{
	"p": true, "li": true, "blockquote": true, "h4": true, "h5": true,
	"h6": true, "pre": true, "figure": true, "figcaption": true,
	"table": true, "dl": true,
}

// ctaRE is the contact-CTA tail trim (inventory section 5: "'Masih Ada
// Kendala?' contact-CTA tails trimmed"). Kept deliberately narrow —
// measured false-positive: broader patterns like "butuh bantuan" also open
// legitimate intro copy.
var ctaRE = regexp.MustCompile(`(?i)^masih (ada|punya) kendala\b`)

// knownCategories is the python KNOWN_CATEGORIES fallback for card
// eyebrows that are standalone text nodes (never substring matching —
// titles contain words like "Syariah").
var knownCategories = map[string]bool{
	"FAQ LinkAja": true, "Soft News": true, "Syariah": true, "Berita LinkAja": true,
}

// catClassRE matches class names that look like a card eyebrow/category
// (python CAT_CLASS_RE).
var catClassRE = regexp.MustCompile(`(?i)eyebrow|categor|tag|label|badge|chip`)

// section is one {question, blocks[]} split at a question heading.
type section struct {
	question string
	blocks   []string
}

// articleBody is the result of extractArticle for one article page.
type articleBody struct {
	title         string
	sections      []section
	prefaceBlocks []string
	prefaceChars  int
	images        int
	strayChars    int
	scoped        bool
}

// blockExtractor is a faithful port of the python BlockExtractor state
// machine: walk the (scoped) event stream, split at h1/h2/h3 question
// headings, and accumulate answer blocks per inventory spec 6.1 storage
// rules (block text trimmed with intra-line whitespace collapsed, blocks
// joined with LF later, list items rendered as "- item" lines).
type blockExtractor struct {
	sections []section
	preface  []string
	curQ     string
	hasQ     bool // python used cur_q is not None; "" (blank heading) is tracked via hasQ
	curBlks  []string
	h3buf    []string
	inH3     bool
	blockTag string
	lines    []string
	cur      []string
	inList   bool
	items    []string
	liParts  []string
	images   int
	stray    int
}

func (b *blockExtractor) finishLine() {
	text := collapseWhitespace(b.cur)
	b.cur = nil
	if text != "" {
		b.lines = append(b.lines, text)
	}
}

func (b *blockExtractor) finishItem() {
	text := collapseWhitespace(b.liParts)
	b.liParts = nil
	if text != "" {
		b.items = append(b.items, text)
	}
}

func (b *blockExtractor) finishBlock() {
	var target *[]string
	if b.hasQ {
		target = &b.curBlks
	} else {
		target = &b.preface
	}
	var blk string
	if b.inList {
		b.finishItem()
		var sb strings.Builder
		for _, it := range b.items {
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString("- ")
			sb.WriteString(it)
		}
		blk = sb.String()
	} else {
		b.finishLine()
		blk = strings.Join(b.lines, "\n")
	}
	blk = strings.TrimSpace(blk)
	if blk != "" {
		*target = append(*target, blk)
	}
	b.blockTag = ""
	b.lines = nil
	b.items = nil
	b.inList = false
}

func (b *blockExtractor) flushSection() {
	if b.hasQ {
		b.sections = append(b.sections, section{question: b.curQ, blocks: b.curBlks})
	}
	b.curQ = ""
	b.hasQ = false
	b.curBlks = nil
}

func (b *blockExtractor) feed(events []event) {
	for _, ev := range events {
		switch ev.Kind {
		case eventText:
			switch {
			case b.inH3:
				b.h3buf = append(b.h3buf, ev.Text)
			case b.blockTag != "":
				if b.inList {
					b.liParts = append(b.liParts, ev.Text)
				} else {
					b.cur = append(b.cur, ev.Text)
				}
			default:
				if s := strings.TrimSpace(ev.Text); s != "" {
					b.stray += len(s)
				}
			}
		case eventVoid:
			if ev.Tag == "br" {
				switch {
				case b.inH3:
					b.h3buf = append(b.h3buf, " ")
				case b.blockTag != "":
					if b.inList {
						b.liParts = append(b.liParts, " ")
					} else {
						b.cur = append(b.cur, " ")
					}
				}
			} else if ev.Tag == "img" && b.blockTag != "" {
				b.images++
			}
		case eventOpen:
			if questionTags[ev.Tag] {
				if b.blockTag != "" {
					b.finishBlock()
				}
				b.flushSection()
				b.h3buf = nil
				b.inH3 = true
				continue
			}
			if b.inH3 {
				continue
			}
			if ev.Tag == "li" && b.inList {
				b.finishItem()
				continue
			}
			if (ev.Tag == "ul" || ev.Tag == "ol") && !b.inList {
				if b.blockTag != "" {
					b.finishBlock()
				}
				b.blockTag = ev.Tag
				b.inList = true
				b.items = nil
				continue
			}
			if (ev.Tag == "tr" || ev.Tag == "td" || ev.Tag == "th") && b.blockTag == "table" {
				b.finishLine() // row/cell -> new line within table block
				continue
			}
			if blockTags[ev.Tag] && !b.inList {
				if b.blockTag != "" {
					b.finishBlock() // implied close of unclosed block
				}
				b.blockTag = ev.Tag
				b.lines = nil
				continue
			}
		case eventClose:
			if questionTags[ev.Tag] && b.inH3 {
				b.curQ = collapseWhitespace(b.h3buf)
				b.hasQ = true // python: cur_q set (possibly to "") -> section exists
				b.h3buf = nil
				b.inH3 = false
				b.curBlks = nil
				continue
			}
			if ev.Tag == "li" && b.inList {
				b.finishItem()
				continue
			}
			if (ev.Tag == "ul" || ev.Tag == "ol") && b.inList && b.blockTag == ev.Tag {
				b.finishBlock()
				continue
			}
			if !b.inList && b.blockTag == ev.Tag {
				b.finishBlock()
				continue
			}
		}
	}
	if b.inH3 {
		b.curQ = collapseWhitespace(b.h3buf)
		b.hasQ = true
		b.h3buf = nil
		b.inH3 = false
	}
	if b.blockTag != "" {
		b.finishBlock()
	}
	b.flushSection()
}

// extractArticle ports python extract_article: page h1 title, then the
// article's Webflow rich-text container(s) (w-richtext) fed to the block
// extractor. Without a w-richtext container the whole page is used
// (scoped=false, flagged by the caller).
func extractArticle(events []event) articleBody {
	var body articleBody
	if span, ok := firstTagSpan(events, "h1"); ok {
		body.title = collectText(events, span)
	}
	spans := outermost(findClassSpans(events, "w-richtext"))
	body.scoped = len(spans) > 0
	var merged []event
	if body.scoped {
		for _, s := range spans {
			merged = append(merged, events[s[0]+1:s[1]]...)
		}
	} else {
		merged = events
	}
	ex := &blockExtractor{}
	ex.feed(merged)
	body.sections = ex.sections
	body.prefaceBlocks = ex.preface
	for _, b := range ex.preface {
		body.prefaceChars += len(b)
	}
	body.images = ex.images
	body.strayChars = ex.stray
	return body
}

// trimCTABlocks drops the trailing contact-CTA tail: from the first block
// whose first line matches ctaRE to the end. Returns (blocks, removedN).
func trimCTABlocks(blocks []string) ([]string, int) {
	for i, blk := range blocks {
		firstLine := blk
		if j := strings.IndexByte(blk, '\n'); j >= 0 {
			firstLine = blk[:j]
		}
		if ctaRE.MatchString(firstLine) {
			return blocks[:i], len(blocks) - i
		}
	}
	return blocks, 0
}

// trimCTAText does the same tail trim on a multi-line answer string.
func trimCTAText(text string) string {
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		if ctaRE.MatchString(ln) {
			return strings.TrimSpace(strings.Join(lines[:i], "\n"))
		}
	}
	return text
}

// firstAnchorHref returns the href of the first anchor inside the span.
func firstAnchorHref(events []event, s, e int) string {
	for i := s + 1; i < e; i++ {
		ev := events[i]
		if ev.Kind == eventOpen && ev.Tag == "a" {
			if href := strings.TrimSpace(ev.Attrs["href"]); href != "" {
				return href
			}
		}
	}
	return ""
}

// categoryInSpan ports python category_in_span: the first non-empty text
// of an element whose class looks like an eyebrow/category/tag; fallback:
// a standalone text node exactly equal to a known category name.
func categoryInSpan(events []event, s, e int) string {
	captureTag := ""
	var buf []string
	for i := s + 1; i < e; i++ {
		ev := events[i]
		switch ev.Kind {
		case eventOpen:
			if captureTag == "" && catClassRE.MatchString(ev.Attrs["class"]) {
				captureTag = ev.Tag
				buf = nil
			}
		case eventClose:
			if captureTag != "" && ev.Tag == captureTag {
				t := collapseWhitespace(buf)
				if t != "" {
					return t
				}
				captureTag = ""
			}
		case eventText:
			if captureTag != "" {
				buf = append(buf, ev.Text)
			} else if knownCategories[strings.TrimSpace(ev.Text)] {
				return strings.TrimSpace(ev.Text)
			}
		}
	}
	return ""
}

// listingCard is one discovered article: canonical URL + category.
type listingCard struct {
	url      string
	category string
}

// parseListing ports python parse_listing: article cards of one /news
// listing page in document order. Without w-dyn-item structure the whole
// page is one span and categories cannot be read (structured=false).
func parseListing(events []event, pageURL string) ([]listingCard, bool) {
	spans := findClassSpans(events, "w-dyn-item")
	structured := len(spans) > 0
	if !structured {
		spans = [][2]int{{0, len(events)}}
	}
	var cards []listingCard
	seen := map[string]bool{}
	for _, span := range spans {
		s, e := span[0], span[1]
		href := ""
		if events[s].Kind == eventOpen && events[s].Tag == "a" {
			href = strings.TrimSpace(events[s].Attrs["href"])
		}
		if href == "" {
			href = firstAnchorHref(events, s, e)
		}
		if href == "" {
			continue
		}
		u := canonicalURL(resolveReference(pageURL, href))
		if !isArticleURL(u) || seen[u] {
			continue
		}
		seen[u] = true
		cat := ""
		if structured {
			cat = categoryInSpan(events, s, e)
		}
		cards = append(cards, listingCard{url: u, category: cat})
	}
	return cards, structured
}

// nextPageURL ports python next_page_url: the href of the first
// w-pagination-next element, resolved against the page URL.
func nextPageURL(events []event, pageURL string) string {
	for _, span := range findClassSpans(events, "w-pagination-next") {
		s, e := span[0], span[1]
		href := ""
		if events[s].Kind == eventOpen && events[s].Tag == "a" {
			href = strings.TrimSpace(events[s].Attrs["href"])
		}
		if href == "" {
			href = firstAnchorHref(events, s, e)
		}
		if href != "" {
			return resolveReference(pageURL, href)
		}
	}
	return ""
}

// resolveReference is python urljoin(pageURL, href).
func resolveReference(baseURL, href string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return href
	}
	ref, err := url.Parse(href)
	if err != nil {
		return href
	}
	return base.ResolveReference(ref).String()
}
