package faqscrape

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/faqindex"
)

// hash12 computes the spec 7.5 hash12 for a URL (first 12 hex of sha256).
func hash12(u string) string {
	sum := sha256.Sum256([]byte(u))
	return hex.EncodeToString(sum[:])[:12]
}

// The conformance vector of inventory spec 6.3 — the python 1.3 scraper
// and this Go scraper must agree bit for bit on the content_hash rule.
const conformanceHash = "69c5d36426d9f86dfc0d2dbd14b572f2b3f2400b91f51c1f6adcffcd8fbce3a3"

func TestUnitHashConformanceVector(t *testing.T) {
	// The exact spec 6.3 inputs.
	got := faqindex.UnitContentHash(
		"Berapa limit saldo dan transaksi LinkAja?",
		"Limit saldo akun terdaftar adalah Rp 2.000.000.\nLimit akun Full Service adalah Rp 20.000.000.",
	)
	if got != conformanceHash {
		t.Fatalf("UnitContentHash conformance vector mismatch: got %s, want %s", got, conformanceHash)
	}
}

// TestBuildUnitsHashPath pins the hash rule inside the unit assembly:
// buildUnits must derive content_hash from the raw (un-normalized)
// question + answer exactly as spec 6.3 says.
func TestBuildUnitsHashPath(t *testing.T) {
	parsed := articleBody{
		sections: []section{{
			question: "Berapa limit saldo dan transaksi LinkAja?",
			blocks: []string{
				"Limit saldo akun terdaftar adalah Rp 2.000.000.",
				"Limit akun Full Service adalah Rp 20.000.000.",
			},
		}},
	}
	units, notes := buildUnits("limit-saldo-transaksi-linkaja",
		"https://www.linkaja.id/news/limit-saldo-transaksi-linkaja", "FAQ LinkAja", parsed, "2026-11-06T00:00:00Z")
	if len(units) != 1 {
		t.Fatalf("units = %d, want 1 (notes: %v)", len(units), notes)
	}
	u := units[0]
	if u.ContentHash != conformanceHash {
		t.Fatalf("buildUnits content_hash = %s, want conformance vector %s", u.ContentHash, conformanceHash)
	}
	if u.ID != "limit-saldo-transaksi-linkaja_01" {
		t.Fatalf("unit id = %q, want limit-saldo-transaksi-linkaja_01 (zero-padded 2-digit ordinal)", u.ID)
	}
	if u.Answer != "Limit saldo akun terdaftar adalah Rp 2.000.000.\nLimit akun Full Service adalah Rp 20.000.000." {
		t.Fatalf("blocks must join with a single LF, got %q", u.Answer)
	}
	if u.Category != "FAQ LinkAja" || u.Language != "id" || u.SourceURL != "https://www.linkaja.id/news/limit-saldo-transaksi-linkaja" {
		t.Fatalf("unit metadata mismatch: %+v", u)
	}
}

// TestCacheFileNameCrossLanguage checks the spec 7.5 cache naming against
// file names actually produced by the python spike (they exist under
// data/raw/ on this repo, so the constants are real cross-language
// evidence, not invented expectations).
func TestCacheFileNameCrossLanguage(t *testing.T) {
	cases := map[string]string{
		"https://www.linkaja.id/news?04327379_page=2":            "news-6536cd2e9f16.html",
		"https://www.linkaja.id/news":                            "news-17ad1da6afeb.html",
		"https://www.linkaja.id/robots.txt":                      "robots-txt-d27b0f6a4f87.html",
		"https://www.linkaja.id/news/limit-saldo-transaksi-linkaja": "limit-saldo-transaksi-linkaja-3e1818c72c3d.html",
		// slug sanitization: uppercase and outside chars -> '-', truncate 64.
		"https://www.linkaja.id/news/Some_Slug.With.Caps": "some-slug-with-caps-" + hash12("https://www.linkaja.id/news/Some_Slug.With.Caps") + ".html",
	}
	for u, want := range cases {
		if got := cacheFileName(u); got != want {
			t.Errorf("cacheFileName(%s) = %s, want %s", u, got, want)
		}
	}
}

func TestGuardURL(t *testing.T) {
	allowed := []string{"linkaja.id", "www.linkaja.id"}
	ok := []string{
		"https://www.linkaja.id/news",
		"https://www.linkaja.id/news?04327379_page=2",
		"https://www.linkaja.id/news/limit-saldo-transaksi-linkaja",
		"https://www.linkaja.id/robots.txt",
	}
	for _, u := range ok {
		if err := guardURL(u, allowed); err != nil {
			t.Errorf("guardURL(%s) refused: %v", u, err)
		}
	}
	bad := []string{
		"http://www.linkaja.id/news",                  // not https
		"https://evil.example.com/news",               // foreign host
		"https://www.linkaja.id/verify",               // robots-disallowed
		"https://www.linkaja.id/apps_tcash",           // robots-disallowed
		"https://www.linkaja.id/verify/deep",          // robots-disallowed subtree
		"https://www.linkaja.id/",                     // outside /news* surface
		"https://www.linkaja.id/linkaja",              // outside /news* surface
		"https://www.linkaja.id/newsg",                // /news* prefix trick refused
		"https://www.linkaja.id/syarat-ketentuan",     // outside /news* surface
	}
	for _, u := range bad {
		if err := guardURL(u, allowed); err == nil {
			t.Errorf("guardURL(%s) allowed, want refusal", u)
		}
	}
}

func TestCanonicalURL(t *testing.T) {
	cases := map[string]string{
		"https://linkaja.id/news/foo":                          "https://www.linkaja.id/news/foo",
		"https://company.linkaja.id/news/foo/":                 "https://www.linkaja.id/news/foo",
		"https://www.linkaja.id/news/foo?x=1#frag":             "https://www.linkaja.id/news/foo",
		"//www.linkaja.id/news/foo":                            "https://www.linkaja.id/news/foo",
		"https://www.linkaja.id/news/foo":                      "https://www.linkaja.id/news/foo",
	}
	for in, want := range cases {
		if got := canonicalURL(in); got != want {
			t.Errorf("canonicalURL(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestParseEvents(t *testing.T) {
	html := `<!DOCTYPE html><!-- c --><div class="w-richtext"><h2>A &amp; B</h2><p>x<br/>y</p>` +
		`<script>var a = "<h3>not a heading</h3>";</script><style>.x{}</style></div>`
	events, err := parseEvents(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	var tags []string
	for _, ev := range events {
		switch ev.Kind {
		case eventOpen, eventClose, eventVoid:
			tags = append(tags, ev.Kind.String()+":"+ev.Tag)
		case eventText:
			texts = append(texts, ev.Text)
		}
	}
	joined := strings.Join(texts, "|")
	for _, want := range []string{"A & B", "x", "y"} {
		if !strings.Contains(joined, want) {
			t.Errorf("text %q missing from events: %q", want, joined)
		}
	}
	if strings.Contains(joined, "not a heading") {
		t.Errorf("script content leaked into text events: %q", joined)
	}
	if strings.Contains(joined, ".x") {
		t.Errorf("style content leaked into text events: %q", joined)
	}
	wantTags := strings.Join([]string{
		"open:div", "open:h2", "close:h2", "open:p", "void:br", "close:p",
		"open:script", "close:script", "open:style", "close:style", "close:div",
	}, ",")
	if strings.Join(tags, ",") != wantTags {
		t.Errorf("tag event sequence = %s, want %s", strings.Join(tags, ","), wantTags)
	}
	// charrefs in attribute values are unescaped (python HTMLParser does
	// this unconditionally).
	events, err = parseEvents(strings.NewReader(`<a href="/news/x?a=1&amp;b=2">l</a>`))
	if err != nil {
		t.Fatal(err)
	}
	if got := events[0].Attrs["href"]; got != "/news/x?a=1&b=2" {
		t.Errorf("attr href = %q, want unescaped", got)
	}
	// self-closing non-void tags become void events (python
	// handle_startendtag).
	events, err = parseEvents(strings.NewReader(`<span/>text`))
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Kind != eventVoid || events[0].Tag != "span" {
		t.Errorf("self-closed span = %v %s, want void", events[0].Kind, events[0].Tag)
	}
}

func TestBlockExtractorRules(t *testing.T) {
	// Measured Webflow article shape: preface dropped, h2/h3 questions,
	// list answers as "- item" lines, CTA tail trimmed, question-only
	// heading dropped.
	html := `<div class="w-richtext">
		<p>True preface before the first heading: dropped per spec.</p>
		<h1>Cara Top Up</h1>
		<p>Intro paragraph riding the page-title heading.</p>
		<h2>Bagaimana cara top up?</h2>
		<p>Buka aplikasi.</p>
		<ul><li>BCA</li><li>BRI</li></ul>
		<h3>Sub pertanyaan?</h3>
		<p>Jawaban sub.</p>
		<h2>Masih Ada Kendala?</h2>
		<p>Hubungi CS 1500650.</p>
	</div>`
	events, err := parseEvents(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	body := extractArticle(events)
	if !body.scoped {
		t.Fatal("w-richtext container not detected")
	}
	if body.title != "Cara Top Up" {
		t.Errorf("title = %q, want Cara Top Up", body.title)
	}
	// 4 question headings (measured quirk: the page title repeats as an
	// h1 INSIDE the rich text, kept as a real unit): h1 + h2 + h3 + CTA
	// h2.
	if len(body.sections) != 4 {
		t.Fatalf("sections = %d, want 4 (h1 + h2 + h3 + CTA heading)", len(body.sections))
	}
	if body.sections[0].question != "Cara Top Up" {
		t.Errorf("q0 (page-title-as-heading) = %q", body.sections[0].question)
	}
	if got := strings.Join(body.sections[0].blocks, "\n"); got != "Intro paragraph riding the page-title heading." {
		t.Errorf("h1 section blocks = %q (intro paragraphs after the h1 ride its section, python quirk)", got)
	}
	if body.sections[1].question != "Bagaimana cara top up?" {
		t.Errorf("q1 = %q", body.sections[1].question)
	}
	wantAnswer := "Buka aplikasi.\n- BCA\n- BRI"
	if got := strings.Join(body.sections[1].blocks, "\n"); got != wantAnswer {
		t.Errorf("answer1 = %q, want %q", got, wantAnswer)
	}
	if body.sections[3].question != "Masih Ada Kendala?" {
		t.Errorf("q3 = %q (CTA heading must survive extraction here; dropping happens in buildUnits)", body.sections[3].question)
	}
	if body.prefaceChars == 0 {
		t.Error("preface not captured for reporting")
	}

	// buildUnits drops the CTA section, keeps the others, assigns
	// document-order ordinals over survivors.
	units, _ := buildUnits("cara-top-up", "https://www.linkaja.id/news/cara-top-up", "FAQ LinkAja", body, "2026-11-06T00:00:00Z")
	if len(units) != 3 {
		t.Fatalf("units = %d, want 3 (CTA section dropped)", len(units))
	}
	if units[0].ID != "cara-top-up_01" || units[2].ID != "cara-top-up_03" {
		t.Errorf("ids = %s..%s; ordinals must run over surviving units", units[0].ID, units[2].ID)
	}
	if units[0].Answer != "Intro paragraph riding the page-title heading." {
		t.Errorf("h1 unit answer = %q", units[0].Answer)
	}
	if strings.Contains(units[2].Answer, "1500650") {
		t.Errorf("CTA tail leaked into answer: %q", units[2].Answer)
	}
}

func TestBlockExtractorCTATailInsideAnswer(t *testing.T) {
	html := `<div class="w-richtext"><h3>Q?</h3><p>Jawaban utama.</p><p>Masih punya kendala? Hubungi kami.</p><p>Footer contact.</p></div>`
	events, err := parseEvents(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	body := extractArticle(events)
	units, notes := buildUnits("q", "https://www.linkaja.id/news/q", "FAQ LinkAja", body, "2026-11-06T00:00:00Z")
	if len(units) != 1 || units[0].Answer != "Jawaban utama." {
		t.Fatalf("CTA tail not trimmed: %+v %v", units, notes)
	}
}

func TestLongFormFallback(t *testing.T) {
	// No usable Q&A structure -> one long-form unit (question = h1,
	// answer = full body, CTA text-trimmed).
	html := `<div class="w-richtext"><h1>Pemberitahuan</h1><p>Paragraf satu.</p><p>Paragraf dua.</p><p>Masih Ada Kendala? CS.</p></div>`
	events, err := parseEvents(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	body := extractArticle(events)
	units, notes := buildUnits("pemberitahuan", "https://www.linkaja.id/news/pemberitahuan", "Berita LinkAja", body, "2026-11-06T00:00:00Z")
	if len(units) != 1 {
		t.Fatalf("units = %d, want 1 long-form unit (%v)", len(units), notes)
	}
	if units[0].Question != "Pemberitahuan" || units[0].ID != "pemberitahuan_01" {
		t.Errorf("long-form unit = %+v", units[0])
	}
	if units[0].Answer != "Paragraf satu.\nParagraf dua." {
		t.Errorf("long-form answer = %q (CTA text tail must be trimmed)", units[0].Answer)
	}

	// No h1 and no body -> 0 units.
	empty := articleBody{}
	units, _ = buildUnits("x", "https://www.linkaja.id/news/x", "", empty, "2026-11-06T00:00:00Z")
	if len(units) != 0 {
		t.Fatalf("empty body must yield 0 units, got %d", len(units))
	}
}

func TestParseListingAndNextPage(t *testing.T) {
	html := `<div class="w-collection-list">
		<div class="w-dyn-item"><a href="/news/alpha" class="w-inline-block"><div class="eyebrow">FAQ LinkAja</div><h3>Alpha</h3></a></div>
		<div class="w-dyn-item"><a href="https://linkaja.id/news/beta/"><div class="card-tag">Soft News</div><h3>Beta</h3></a></div>
		<div class="w-dyn-item"><a href="/promo/x"><h3>Promo</h3></a></div>
		<div class="w-pagination-next"><a href="/news?04327379_page=2#f">Next</a></div>
	</div>`
	events, err := parseEvents(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	cards, structured := parseListing(events, "https://www.linkaja.id/news")
	if !structured {
		t.Fatal("w-dyn-item structure not detected")
	}
	if len(cards) != 2 {
		t.Fatalf("cards = %d, want 2 (non-article href filtered)", len(cards))
	}
	if cards[0].url != "https://www.linkaja.id/news/alpha" || cards[0].category != "FAQ LinkAja" {
		t.Errorf("card0 = %+v", cards[0])
	}
	if cards[1].url != "https://www.linkaja.id/news/beta" || cards[1].category != "Soft News" {
		t.Errorf("card1 = %+v (host normalized, trailing slash dropped, eyebrow via card-tag class)", cards[1])
	}
	if got := nextPageURL(events, "https://www.linkaja.id/news"); got != "https://www.linkaja.id/news?04327379_page=2#f" {
		t.Errorf("next page = %q", got)
	}
}

func TestParseRobotsDenylist(t *testing.T) {
	// The recorded verdict (inventory section 3), verbatim.
	verdict := "User-agent: *\nDisallow: /verify\nDisallow: /apps_tcash\n\n" +
		"Sitemap: https://linkaja.id/sitemap.xml\nSitemap: https://linkaja.id/sitemap-pages.xml"
	got := parseRobotsDenylist(verdict, "*")
	if len(got) != 2 || !got["/verify"] || !got["/apps_tcash"] {
		t.Fatalf("denylist = %v, want [/verify /apps_tcash]", got)
	}

	// A second agent's rules must not merge into the * group.
	mixed := "User-agent: *\nDisallow: /verify\nUser-agent: BadBot\nDisallow: /private"
	got = parseRobotsDenylist(mixed, "*")
	if len(got) != 1 || !got["/verify"] {
		t.Fatalf("denylist = %v, want only /verify for *", got)
	}

	// Any drift from the verdict must be visible (this is what stops the
	// run in checkRobots).
	changed := "User-agent: *\nDisallow: /verify\nDisallow: /apps_tcash\nDisallow: /news"
	if got := parseRobotsDenylist(changed, "*"); len(got) != 3 {
		t.Fatalf("denylist = %v, want 3 entries after drift", got)
	}
}

// TestWriteCorpusByteCompat pins the exact on-disk shape against the
// python dump format: json.dumps(corpus, ensure_ascii=False, indent=2) +
// "\n" — non-ASCII raw (no \uXXXX), no HTML escaping, 2-space indent,
// spec 6.1 field order, trailing newline.
func TestWriteCorpusByteCompat(t *testing.T) {
	corpus := &faqindex.Corpus{
		SchemaVersion: 1,
		ScrapedAt:     "2026-11-06T00:00:00Z",
		Source:        "linkaja.id",
		Units: []faqindex.Unit{{
			ID:          "u_01",
			SourceURL:   "https://www.linkaja.id/news/u",
			Question:    "Apa itu “LinkAja” & P2P?",
			Answer:      "Baris satu.\nBaris dua.",
			Category:    "FAQ LinkAja",
			Language:    "id",
			ContentHash: conformanceHash,
			ScrapedAt:   "2026-11-05T18:12:14Z",
		}},
	}
	path := filepath.Join(t.TempDir(), "corpus.json")
	if err := WriteCorpus(path, corpus); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "schema_version": 1,
  "scraped_at": "2026-11-06T00:00:00Z",
  "source": "linkaja.id",
  "units": [
    {
      "id": "u_01",
      "source_url": "https://www.linkaja.id/news/u",
      "question": "Apa itu “LinkAja” & P2P?",
      "answer": "Baris satu.\nBaris dua.",
      "category": "FAQ LinkAja",
      "language": "id",
      "content_hash": "69c5d36426d9f86dfc0d2dbd14b572f2b3f2400b91f51c1f6adcffcd8fbce3a3",
      "scraped_at": "2026-11-05T18:12:14Z"
    }
  ]
}
`
	if string(raw) != want {
		t.Fatalf("corpus bytes differ from python dump format.\n--- got ---\n%s\n--- want ---\n%s", raw, want)
	}
}

// TestFetcherCacheHit proves the 1.2 cache contract offline: a URL present
// in the sidecar + cache file is served from disk with its recorded
// fetched_at, and no network request happens.
func TestFetcherCacheHit(t *testing.T) {
	dir := t.TempDir()
	body := []byte("<html>cached</html>")
	if err := os.WriteFile(filepath.Join(dir, "news-abc123def456.html"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFetcher(Options{
		UserAgent:    "test-agent",
		CacheDir:     dir,
		AllowedHosts: []string{"www.linkaja.id"},
	})
	f.entries["https://www.linkaja.id/news"] = cacheEntry{
		FetchedAt: "2026-11-05T18:08:53Z", File: "news-abc123def456.html", Status: 200,
	}
	got, fetchedAt, status, cached, err := f.fetch(context.Background(), "https://www.linkaja.id/news", false)
	if err != nil {
		t.Fatal(err)
	}
	if !cached || status != 200 || string(got) != string(body) || fetchedAt != "2026-11-05T18:08:53Z" {
		t.Fatalf("cache hit mismatch: cached=%v status=%d fetchedAt=%s", cached, status, fetchedAt)
	}
	if f.networkFetches != 0 {
		t.Fatalf("network fetches = %d, want 0 (cache must prevent re-crawl)", f.networkFetches)
	}
	// Cached non-200 is reused too (documented behavior: a dead URL is
	// not hammered on re-runs).
	f.entries["https://www.linkaja.id/news/dead"] = cacheEntry{
		FetchedAt: "2026-11-05T18:08:53Z", File: "news-abc123def456.html", Status: 404,
	}
	_, _, status, cached, err = f.fetch(context.Background(), "https://www.linkaja.id/news/dead", false)
	if err != nil || !cached || status != 404 {
		t.Fatalf("cached non-200 must be reused, got cached=%v status=%d err=%v", cached, status, err)
	}
}

func TestFetcherSaveLoadCacheIndexRoundTrip(t *testing.T) {
	dir := t.TempDir()
	f := newFetcher(Options{UserAgent: "t", CacheDir: dir, AllowedHosts: []string{"www.linkaja.id"}})
	f.entries["https://www.linkaja.id/robots.txt"] = cacheEntry{FetchedAt: "2026-11-06T00:00:00Z", File: "robots-txt-d27b0f6a4f87.html", Status: 200}
	if err := f.saveCacheIndex(); err != nil {
		t.Fatal(err)
	}
	f2 := newFetcher(Options{UserAgent: "t", CacheDir: dir, AllowedHosts: []string{"www.linkaja.id"}})
	if err := f2.loadCacheIndex(); err != nil {
		t.Fatal(err)
	}
	if e := f2.entries["https://www.linkaja.id/robots.txt"]; e.Status != 200 || e.File != "robots-txt-d27b0f6a4f87.html" {
		t.Fatalf("round trip mismatch: %+v", e)
	}
	// Sidecar shape: sorted keys, 2-space indent, alphabetical entry
	// fields — byte-compatible with the python sort_keys dump.
	raw, err := os.ReadFile(filepath.Join(dir, cacheIndexFile))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"entries\": {\n    \"https://www.linkaja.id/robots.txt\": {\n      \"fetched_at\": \"2026-11-06T00:00:00Z\",\n      \"file\": \"robots-txt-d27b0f6a4f87.html\",\n      \"status\": 200\n    }\n  },\n  \"schema_version\": 1\n}\n"
	if string(raw) != want {
		t.Fatalf("sidecar bytes differ from python dump format:\n%s", raw)
	}
}
