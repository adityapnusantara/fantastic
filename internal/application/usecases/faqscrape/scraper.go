package faqscrape

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/adityapratamanusantara/fantastic/internal/application/usecases/faqindex"
)

// Binding constants (docs/rag/inventory.md sections 3, 5, 6, 7). The UA,
// request delay and cache dir are config-driven (design D6 via the
// faq.scrape config section); the constants below are the parts of the
// contract that are not operator-tunable.
const (
	// baseURL is the only crawl host, canonicalized (spec 6.1).
	baseURL = "https://www.linkaja.id"

	// requestTimeout bounds one HTTP request (python REQUEST_TIMEOUT).
	requestTimeout = 30 * time.Second

	// maxPages is the safety cap for the pagination walk (python MAX_PAGES).
	maxPages = 15

	// challengeBackoff is the >= 5 s one-shot backoff on a suspected
	// Cloudflare challenge (politeness rule 4: never a retry loop).
	challengeBackoff = 5 * time.Second

	// cacheIndexFile is the cache sidecar name (spec 7.5).
	cacheIndexFile = "cache-index.json"

	// robotsPath is re-fetched at the start of every run (rule 3).
	robotsPath = "/robots.txt"
)

// expectedRobotsDenylist is the task 1.1 verdict (inventory section 3,
// quoted verbatim there): a single `User-agent: *` group disallowing
// exactly these two paths. checkRobots re-derives the denylist from the
// live robots.txt at the start of every run and refuses to crawl if it
// changed.
var expectedRobotsDenylist = []string{"/verify", "/apps_tcash"}

// panduanURLs are the three articles reachable ONLY via the /linkaja and
// /linkaja-syariah product-page "Ada panduannya" cards (inventory section
// 1.1, category "(panduan card)"). All are /news/* paths, robots-allowed;
// they are listed here (as the python spike did) instead of re-discovered
// from the product pages so every request stays inside the /news* crawl
// surface (rule 3). Schema 6.1: category "panduan" only when such a URL is
// NOT on the listing.
var panduanURLs = []string{
	"https://www.linkaja.id/news/cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant",
	"https://www.linkaja.id/news/cara-kirim-uang-pakai-linkaja-praktis-ke-sesama-linkaja-atau-rekening-bank",
	"https://www.linkaja.id/news/faq-mgames-linkaja",
}

// articleURLRE matches /news/<slug> paths (python ARTICLE_URL_RE).
var articleURLRE = regexp.MustCompile(`^/news/[^/?#]+$`)

// slugSanitizeRE replaces characters outside [a-z0-9-] in cache slugs
// (spec 7.5).
var slugSanitizeRE = regexp.MustCompile(`[^a-z0-9-]`)

// Options configures one scrape run; all fields come from the faq: config
// section (design D6) unless noted.
type Options struct {
	// UserAgent is the politeness UA (rule 1) — cfg.FAQ.Scrape.UserAgent.
	UserAgent string
	// RequestDelay is the minimum time between the START of consecutive
	// requests (rule 2) — cfg.FAQ.Scrape.RequestDelayMS.
	RequestDelay time.Duration
	// CacheDir is the raw HTML cache root (rule 5) — cfg.FAQ.Scrape.CacheDir.
	CacheDir string
	// AllowedHosts is the host allow-list — cfg.FAQ.Scrape.AllowedHosts.
	AllowedHosts []string
	// Now overrides the wall clock for timestamps (tests); nil = time.Now.
	Now func() time.Time
	// MaxPagesOverride overrides the 15-page safety cap (tests only).
	MaxPagesOverride int
}

// rfc3339Now formats the current time like python's
// datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ").
func rfc3339Now() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func (o *Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// canonicalURL normalizes an absolute URL per spec 6.1: https scheme,
// host normalized to www.linkaja.id (apex and company alias), no query,
// no fragment, no trailing slash.
func canonicalURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	host := strings.ToLower(u.Host)
	if host == "linkaja.id" || host == "company.linkaja.id" {
		host = "www.linkaja.id"
	}
	return "https://" + host + strings.TrimRight(u.Path, "/")
}

// isArticleURL reports whether u is a canonical /news/<slug> article URL.
func isArticleURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	return strings.ToLower(parsed.Host) == "www.linkaja.id" && articleURLRE.MatchString(parsed.Path)
}

// guardURL hard-enforces politeness rules 3+4: https on an allowed host
// only; /verify and /apps_tcash refused outright (robots verdict,
// inventory section 3); /robots.txt allowed (rule 3 mandates re-fetching
// it every run); everything else must stay inside the /news* crawl
// surface. It guards cache reads too, like the python scraper.
func guardURL(raw string, allowedHosts []string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("refusing unparseable URL %q: %w", raw, err)
	}
	host := strings.ToLower(parsed.Hostname())
	allowed := false
	for _, h := range allowedHosts {
		if host == strings.ToLower(h) {
			allowed = true
			break
		}
	}
	if parsed.Scheme != "https" || !allowed {
		return fmt.Errorf("refusing non-canonical URL: %s", raw)
	}
	path := parsed.Path
	if path == "" {
		path = "/"
	}
	if path == robotsPath {
		return nil
	}
	for _, bad := range expectedRobotsDenylist {
		if path == bad || strings.HasPrefix(path, bad+"/") {
			return fmt.Errorf("robots-disallowed path requested: %s", raw)
		}
	}
	if path != "/news" && !strings.HasPrefix(path, "/news/") {
		return fmt.Errorf("URL outside the /news* crawl surface: %s", raw)
	}
	return nil
}

// cacheEntry is one cache-index.json entry (spec 7.5 sidecar).
type cacheEntry struct {
	FetchedAt string `json:"fetched_at"`
	File      string `json:"file"`
	Status    int    `json:"status"`
}

// fetcher implements inventory section 7 exactly: UA from config, minimum
// RequestDelay between request starts, data/raw/<slug>-<hash12>.html cache
// with the cache-index.json sidecar, one-shot backoff on suspected
// challenges. Cached URLs are never re-fetched — including cached non-200
// responses, so a dead/blocked URL is not hammered on re-runs (delete its
// cache file to force a retry). The one deliberate exception: robots.txt
// is re-fetched on every run (rule 3) via fetch(forceNetwork=true).
type fetcher struct {
	opts    Options
	client  *http.Client
	rawDir  string
	entries map[string]cacheEntry

	networkFetches int
	cacheHits      int
	lastStart      time.Time
	failures       []string // "url -> status-or-error"
	challenges     []string // urls that got a 403/503 (incl. recovered)
}

func newFetcher(opts Options) *fetcher {
	return &fetcher{
		opts: opts,
		client: &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				// Politeness rule 4: GET only, no compression
				// negotiation surprises — we send Accept-Encoding:
				// identity and still handle gzip/deflate if forced.
				DisableCompression: true,
			},
		},
		rawDir:  opts.CacheDir,
		entries: map[string]cacheEntry{},
	}
}

// loadCacheIndex reads the sidecar; an unreadable sidecar starts fresh
// (the slug-hash convention is deterministic, so the sidecar is a
// convenience index, not the source of truth).
func (f *fetcher) loadCacheIndex() error {
	raw, err := os.ReadFile(filepath.Join(f.rawDir, cacheIndexFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var payload struct {
		SchemaVersion int                   `json:"schema_version"`
		Entries       map[string]cacheEntry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	if payload.Entries != nil {
		f.entries = payload.Entries
	}
	return nil
}

// saveCacheIndex writes the sidecar byte-compatibly with the python
// scraper: json.dumps(..., ensure_ascii=False, indent=2, sort_keys=True)
// + "\n" — Go maps marshal with sorted keys, the entry struct keeps
// fetched_at/file/status in alphabetical (python sort_keys) order, and
// SetEscapeHTML(false) matches python's lack of HTML escaping.
func (f *fetcher) saveCacheIndex() error {
	payload := map[string]any{
		"schema_version": 1,
		"entries":        f.entries,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(f.rawDir, cacheIndexFile), buf.Bytes(), 0o644)
}

// cacheFileName implements spec 7.5: slug = last path segment, lowercased,
// characters outside [a-z0-9-] -> '-', truncated to 64, empty -> "index";
// hash12 = first 12 hex of sha256(full request URL incl. query).
func cacheFileName(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		panic(err) // guarded URLs parse by construction
	}
	seg := strings.TrimRight(parsed.Path, "/")
	if i := strings.LastIndexByte(seg, '/'); i >= 0 {
		seg = seg[i+1:]
	}
	seg = strings.ToLower(seg)
	seg = slugSanitizeRE.ReplaceAllString(seg, "-")
	if len(seg) > 64 {
		seg = seg[:64]
	}
	if seg == "" {
		seg = "index"
	}
	sum := sha256.Sum256([]byte(rawURL))
	return seg + "-" + hex.EncodeToString(sum[:])[:12] + ".html"
}

// politeWait enforces rule 2: RequestDelay between the START of
// consecutive requests.
func (f *fetcher) politeWait() {
	if f.opts.RequestDelay <= 0 || f.lastStart.IsZero() {
		return
	}
	if wait := f.opts.RequestDelay - time.Since(f.lastStart); wait > 0 {
		time.Sleep(wait)
	}
}

// getOnce performs one GET with the python headers.
func (f *fetcher) getOnce(ctx context.Context, rawURL string) (int, []byte, error) {
	f.politeWait()
	f.lastStart = time.Now()
	f.networkFetches++
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", f.opts.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.7")
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := f.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	switch strings.TrimSpace(resp.Header.Get("Content-Encoding")) {
	case "gzip", "x-gzip":
		if zr, zerr := gzip.NewReader(bytes.NewReader(body)); zerr == nil {
			if out, rerr := io.ReadAll(zr); rerr == nil {
				body = out
			}
		}
	case "deflate":
		// python used zlib.decompress (RFC 1950); keep the same narrow
		// fallback (no raw-deflate guessing).
		if zr, zerr := zlib.NewReader(bytes.NewReader(body)); zerr == nil {
			if out, rerr := io.ReadAll(zr); rerr == nil {
				body = out
			}
		}
	}
	return resp.StatusCode, body, nil
}

// networkGet implements the one-shot challenge handling: 403/503 ->
// back off >= 5 s -> ONE retry -> otherwise record + continue. Never a
// retry loop (politeness rule 4).
func (f *fetcher) networkGet(ctx context.Context, rawURL string) (int, []byte) {
	status, body, err := f.getOnce(ctx, rawURL)
	if err != nil {
		if status == 0 {
			f.failures = append(f.failures, fmt.Sprintf("%s -> error: %v", rawURL, err))
			return 0, nil
		}
		// body read failed mid-response; treat as the status with no body
		f.failures = append(f.failures, fmt.Sprintf("%s -> error after HTTP %d: %v", rawURL, status, err))
		return status, nil
	}
	if status == 403 || status == 503 {
		f.challenges = append(f.challenges, rawURL)
		fmt.Printf("[warn] HTTP %d on %s (possible Cloudflare challenge); backing off %.0fs, retrying ONCE\n",
			status, rawURL, challengeBackoff.Seconds())
		time.Sleep(challengeBackoff)
		st2, body2, err2 := f.getOnce(ctx, rawURL)
		if err2 == nil && st2 == 200 {
			return st2, body2
		}
		if err2 != nil {
			f.failures = append(f.failures, fmt.Sprintf("%s -> error after backoff: %v", rawURL, err2))
			return status, body
		}
		f.failures = append(f.failures, fmt.Sprintf("%s -> %d", rawURL, st2))
		return st2, body2
	}
	if status != 200 {
		f.failures = append(f.failures, fmt.Sprintf("%s -> %d", rawURL, status))
	}
	return status, body
}

// fetch returns (body, fetchedAt, status, fromCache). Status 0 means a
// transport error, which is not cached.
func (f *fetcher) fetch(ctx context.Context, rawURL string, forceNetwork bool) ([]byte, string, int, bool, error) {
	if err := guardURL(rawURL, f.opts.AllowedHosts); err != nil {
		return nil, "", 0, false, err
	}
	fname := cacheFileName(rawURL)
	if !forceNetwork {
		if entry, ok := f.entries[rawURL]; ok {
			file := entry.File
			if file == "" {
				file = fname
			}
			body, err := os.ReadFile(filepath.Join(f.rawDir, file))
			if err == nil {
				f.cacheHits++
				return body, entry.FetchedAt, entry.Status, true, nil
			}
		}
	}
	status, body := f.networkGet(ctx, rawURL)
	fetchedAt := rfc3339Now()
	if status != 0 { // transport errors are not cached
		if err := os.WriteFile(filepath.Join(f.rawDir, fname), body, 0o644); err != nil {
			return nil, "", status, false, fmt.Errorf("write cache %s: %w", fname, err)
		}
		f.entries[rawURL] = cacheEntry{File: fname, FetchedAt: fetchedAt, Status: status}
		if err := f.saveCacheIndex(); err != nil {
			return nil, "", status, false, fmt.Errorf("write cache index: %w", err)
		}
	}
	return body, fetchedAt, status, false, nil
}

// checkRobots implements politeness rule 3: re-fetch robots.txt at the
// start of EVERY scrape run and stop if the measured denylist no longer
// matches the task 1.1 verdict.
//
// DOCUMENTED DEVIATION from the python spike: python compared the whole
// file byte-verbatim (whitespace-normalized, including its Sitemap
// lines); task 4.1 specifies "robots.txt fetch + path check against the
// /verify + /apps_tcash denylist", so this implementation parses the
// `User-agent: *` group minimally and requires its Disallow set to equal
// the recorded verdict exactly. Any added or removed Disallow stops the
// run before a single page is fetched. A non-200 robots fetch also stops
// the run (compliance cannot be verified).
func (f *fetcher) checkRobots(ctx context.Context) error {
	rawURL := baseURL + robotsPath
	fmt.Printf("[robots] re-fetching %s (contract rule 3: every run; not served from cache)\n", rawURL)
	body, _, status, _, err := f.fetch(ctx, rawURL, true)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("robots.txt fetch returned HTTP %d; cannot verify compliance -- aborting", status)
	}
	got := parseRobotsDenylist(string(body), "*")
	if len(got) != len(expectedRobotsDenylist) {
		return fmt.Errorf("robots.txt changed since the task 1.1 verdict: measured disallow %v, recorded %v. "+
			"Per politeness contract rule 3: stop, re-record the verdict in docs/rag/inventory.md before crawling. No pages were fetched.",
			got, expectedRobotsDenylist)
	}
	for _, want := range expectedRobotsDenylist {
		if got[want] != true {
			return fmt.Errorf("robots.txt changed since the task 1.1 verdict: measured disallow %v, recorded %v. "+
				"Per politeness contract rule 3: stop, re-record the verdict in docs/rag/inventory.md before crawling. No pages were fetched.",
				got, expectedRobotsDenylist)
		}
	}
	fmt.Printf("[robots] OK: denylist matches inventory section 3 verdict (%d bytes; "+
		"allowed = everything except /verify and /apps_tcash; crawl surface /news*)\n", len(body))
	return nil
}

// parseRobotsDenylist is the minimal robots.txt parsing required by rule
// 3: collect the Disallow paths of the named user-agent group. Comments
// are stripped; a group is the consecutive `User-agent:` lines plus the
// rules that follow them. No full robotstxt library — the contract only
// needs the `*` group's path set.
func parseRobotsDenylist(text, agent string) map[string]bool {
	out := map[string]bool{}
	currentAgents := map[string]bool{}
	inGroupHeader := false // consecutive user-agent lines share one group
	for _, line := range strings.Split(text, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		switch name {
		case "user-agent":
			if !inGroupHeader {
				// A rule line ended the previous group (RFC 9309
				// grouping); start a fresh one.
				currentAgents = map[string]bool{}
			}
			currentAgents[strings.ToLower(value)] = true
			inGroupHeader = true
		default:
			if name == "disallow" && currentAgents[agent] && value != "" {
				out[value] = true
			}
			inGroupHeader = false
		}
	}
	return out
}

// discoveredPage is one walked listing page, for the run report.
type discoveredPage struct {
	url   string
	cards int
}

// discover walks /news pagination while w-pagination-next exists (the
// declared child sitemaps 404, inventory 1.4/3 — they are never used).
func (f *fetcher) discover(ctx context.Context) (cards []listingCard, pages []discoveredPage, structuredAny bool, err error) {
	pageURL := baseURL + "/news"
	byURL := map[string]string{} // url -> category
	var order []string
	for pageURL != "" && len(pages) < f.maxPages() {
		if err := guardURL(pageURL, f.opts.AllowedHosts); err != nil {
			return nil, nil, false, err
		}
		body, _, status, _, ferr := f.fetch(ctx, pageURL, false)
		if ferr != nil {
			return nil, nil, false, ferr
		}
		if status != 200 {
			fmt.Printf("[warn] listing page %s -> HTTP %d; stopping pagination walk\n", pageURL, status)
			break
		}
		events, perr := parseEvents(bytes.NewReader(body))
		if perr != nil {
			return nil, nil, false, fmt.Errorf("parse listing %s: %w", pageURL, perr)
		}
		pageCards, structured := parseListing(events, pageURL)
		structuredAny = structuredAny || structured
		for _, c := range pageCards {
			if _, ok := byURL[c.url]; !ok {
				byURL[c.url] = c.category
				order = append(order, c.url)
			}
		}
		pages = append(pages, discoveredPage{url: pageURL, cards: len(pageCards)})
		nxt := nextPageURL(events, pageURL)
		if nxt != "" {
			if u, uerr := url.Parse(nxt); uerr == nil {
				u.Fragment = ""
				nxt = u.String()
			}
		}
		if nxt == "" || nxt == pageURL {
			break
		}
		pageURL = nxt
	}
	if len(pages) >= f.maxPages() {
		fmt.Printf("[warn] pagination cap %d reached; there may be more pages\n", f.maxPages())
	}
	if !structuredAny {
		fmt.Println("[anomaly] listing structure not recognized (no w-dyn-item cards); " +
			"categories could not be read from cards")
	}
	for _, u := range order {
		cards = append(cards, listingCard{url: u, category: byURL[u]})
	}
	return cards, pages, structuredAny, nil
}

func (f *fetcher) maxPages() int {
	if f.opts.MaxPagesOverride > 0 {
		return f.opts.MaxPagesOverride
	}
	return maxPages
}

// buildUnits assembles the corpus units of one article per inventory 6.1
// + section 5, including the question-only/CTA dropping rules and the
// long-form fallback. Ordinals are assigned in document order over the
// units that survive extraction (spec 6.1 `<slug>_<NN>` rule).
func buildUnits(slug, rawURL, category string, parsed articleBody, fetchedAt string) ([]faqindex.Unit, []string) {
	var notes []string
	var units []faqindex.Unit

	makeUnit := func(ordinal int, question, answer string) faqindex.Unit {
		return faqindex.Unit{
			ID:          fmt.Sprintf("%s_%02d", slug, ordinal),
			SourceURL:   rawURL,
			Question:    faqindex.Norm(question),
			Answer:      answer,
			Category:    category,
			Language:    "id",
			ContentHash: faqindex.UnitContentHash(question, answer),
			ScrapedAt:   fetchedAt,
		}
	}

	droppedCTA, droppedEmpty := 0, 0
	for _, sec := range parsed.sections {
		q := sec.question
		if q == "" {
			notes = append(notes, "heading with empty text skipped")
			continue
		}
		if ctaRE.MatchString(q) {
			droppedCTA++
			notes = append(notes, fmt.Sprintf("dropped contact-CTA section (heading = %q)", q))
			continue
		}
		blocks, removed := trimCTABlocks(sec.blocks)
		if removed > 0 {
			notes = append(notes, fmt.Sprintf("trimmed contact-CTA tail: %d block(s) after %q", removed, q))
		}
		answer := strings.Join(blocks, "\n")
		if answer == "" {
			// Structural parent heading (immediately followed by
			// sub-headings) or content lost to trimming: a question-only
			// unit embeds no answerable content, so it is dropped and
			// flagged (python behavior, audited by task 1.4).
			droppedEmpty++
			notes = append(notes, fmt.Sprintf("dropped question-only heading (no answer blocks): %q", q))
			continue
		}
		units = append(units, makeUnit(len(units)+1, q, answer))
	}

	if len(units) > 0 && parsed.prefaceChars > 0 {
		// Deliberate spec-strict choice (documented in the python spike):
		// paragraph(s) BEFORE the first heading (article preface) are
		// dropped; the per-article char count is reported so task 1.4 can
		// audit whether any meaningful intro content was lost.
		notes = append(notes, fmt.Sprintf("preface dropped per spec (content before first heading): %d chars in %d block(s)",
			parsed.prefaceChars, len(parsed.prefaceBlocks)))
	}

	if len(units) == 0 {
		// Long-form fallback (inventory 6.1, "only for articles without
		// usable h3 Q&A structure"): question = h1, answer = the full body
		// blocks. Also covers articles whose only headings were blank/CTA
		// so no unit survived.
		if parsed.title == "" || len(parsed.prefaceBlocks) == 0 {
			notes = append(notes, "no usable Q&A headings AND no usable h1/body -- 0 units")
			return units, notes
		}
		answer := trimCTAText(strings.Join(parsed.prefaceBlocks, "\n"))
		notes = append(notes, "long-form fallback: no usable Q&A structure; 1 unit (question = h1, answer = full body)")
		units = append(units, makeUnit(1, parsed.title, answer))
	}

	if parsed.images > 0 {
		notes = append(notes, fmt.Sprintf("%d image(s) inside answer blocks skipped (no alt-text capture)", parsed.images))
	}
	if parsed.strayChars > 40 {
		notes = append(notes, fmt.Sprintf("%d chars of stray text outside recognized blocks", parsed.strayChars))
	}
	if droppedCTA > 0 {
		notes = append(notes, fmt.Sprintf("%d contact-CTA section(s) dropped in total", droppedCTA))
	}
	if droppedEmpty > 0 {
		notes = append(notes, fmt.Sprintf("%d question-only heading(s) dropped in total", droppedEmpty))
	}
	return units, notes
}

// RunStats summarizes one scrape run for logging and verification.
type RunStats struct {
	ArticlesDiscovered int
	PanduanAdded       int
	ArticlesFetchedOK  int
	Units              int
	NetworkFetches     int
	CacheHits          int
	Failures           []string
	Challenges         []string
	Anomalies          []string
	Pages              int
}

// Run executes the full scrape: robots re-check -> discovery -> per-article
// extraction -> corpus assembly (units sorted by id ascending). It never
// writes the corpus file itself; the CLI decides (dry-run).
func Run(ctx context.Context, opts Options) (*faqindex.Corpus, *RunStats, error) {
	if opts.UserAgent == "" {
		return nil, nil, fmt.Errorf("scrape: faq.scrape.user_agent is not configured")
	}
	if opts.CacheDir == "" {
		return nil, nil, fmt.Errorf("scrape: faq.scrape.cache_dir is not configured")
	}
	if len(opts.AllowedHosts) == 0 {
		return nil, nil, fmt.Errorf("scrape: faq.scrape.allowed_hosts is not configured")
	}
	if opts.RequestDelay < 0 {
		return nil, nil, fmt.Errorf("scrape: negative request delay")
	}

	f := newFetcher(opts)
	if err := os.MkdirAll(f.rawDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("scrape: create cache dir %s: %w", f.rawDir, err)
	}
	if err := f.loadCacheIndex(); err != nil {
		fmt.Printf("[warn] cache-index.json unreadable (%v); starting fresh\n", err)
		f.entries = map[string]cacheEntry{}
	}
	if err := f.checkRobots(ctx); err != nil {
		return nil, nil, fmt.Errorf("scrape: %w", err)
	}

	stats := &RunStats{}

	cards, pages, _, err := f.discover(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("scrape: discovery: %w", err)
	}
	stats.Pages = len(pages)
	for _, p := range pages {
		fmt.Printf("  page %s -> %d cards\n", p.url, p.cards)
	}
	stats.ArticlesDiscovered = len(cards)

	byURL := make(map[string]string, len(cards)+len(panduanURLs))
	for _, c := range cards {
		byURL[c.url] = c.category
	}
	for _, u := range panduanURLs {
		if _, ok := byURL[u]; ok {
			fmt.Printf("  [note] panduan URL also on listing with category %q: %s\n", byURL[u], u)
			continue
		}
		byURL[u] = "panduan"
		stats.PanduanAdded++
	}
	articleURLs := make([]string, 0, len(byURL))
	for u := range byURL {
		articleURLs = append(articleURLs, u)
	}
	sort.Strings(articleURLs)
	fmt.Printf("  articles to fetch: %d\n", len(articleURLs))

	var units []faqindex.Unit
	idOwner := map[string]string{}
	for _, rawURL := range articleURLs {
		slug := rawURL[strings.LastIndexByte(rawURL, '/')+1:]
		category := byURL[rawURL]
		body, fetchedAt, status, cached, err := f.fetch(ctx, rawURL, false)
		if err != nil {
			return nil, nil, fmt.Errorf("scrape: fetch %s: %w", rawURL, err)
		}
		if status != 200 {
			src := "network"
			if cached {
				src = "cache"
			}
			stats.Anomalies = append(stats.Anomalies,
				fmt.Sprintf("%s: fetch status %d (%s) -- 0 units", slug, status, src))
			continue
		}
		stats.ArticlesFetchedOK++
		events, err := parseEvents(bytes.NewReader(body))
		if err != nil {
			return nil, nil, fmt.Errorf("scrape: parse %s: %w", rawURL, err)
		}
		parsed := extractArticle(events)
		if !parsed.scoped {
			stats.Anomalies = append(stats.Anomalies,
				fmt.Sprintf("%s: no w-richtext container; extracted over whole page (review)", slug))
		}
		aus, notes := buildUnits(slug, rawURL, category, parsed, fetchedAt)
		for _, n := range notes {
			stats.Anomalies = append(stats.Anomalies, slug+": "+n)
		}
		for _, u := range aus {
			if prev, dup := idOwner[u.ID]; dup {
				return nil, nil, fmt.Errorf("scrape: duplicate unit id %q (%s vs %s)", u.ID, rawURL, prev)
			}
			idOwner[u.ID] = rawURL
		}
		units = append(units, aus...)
	}

	sort.Slice(units, func(i, j int) bool { return units[i].ID < units[j].ID })

	stats.Units = len(units)
	stats.NetworkFetches = f.networkFetches
	stats.CacheHits = f.cacheHits
	stats.Failures = f.failures
	stats.Challenges = f.challenges

	corpus := &faqindex.Corpus{
		SchemaVersion: 1,
		// Corpus-level scraped_at = when the corpus file was generated
		// (spec 6.1). Unit-level scraped_at rides the raw cache, so a
		// cached re-run is byte-identical except this field.
		ScrapedAt: opts.now().UTC().Format("2006-01-02T15:04:05Z"),
		Source:    "linkaja.id",
		Units:     units,
	}
	return corpus, stats, nil
}

// WriteCorpus emits the curated corpus JSON byte-compatibly with the
// python scraper (inventory spec 6.1): json.dumps(corpus,
// ensure_ascii=False, indent=2) + "\n". The faqindex.Corpus/Unit struct
// tags carry the spec field order; SetEscapeHTML(false) matches python's
// lack of <>& escaping.
func WriteCorpus(path string, corpus *faqindex.Corpus) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(corpus); err != nil {
		return fmt.Errorf("encode corpus: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create corpus dir: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write corpus %s: %w", path, err)
	}
	return nil
}
