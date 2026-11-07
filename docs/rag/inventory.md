# LinkAja FAQ Surface Inventory

Task 1.1 of the `add-faq-rag-search` OpenSpec change. Probing done 2026-11-06 with the
politeness contract from design D6: UA `fantastic-faq-spike/0.1 (personal project; contact via repo)`,
minimum 1.2 s between requests, GET/HEAD only, public pages only (~36 requests total).
This is an inventory, not the content scrape (that is task 1.3).

**TL;DR for the next tasks.** LinkAja's web presence is a Webflow corporate site
(`www.linkaja.id`, aliased by `linkaja.id` apex and `company.linkaja.id`, fronted by Cloudflare).
There is no `/faq` page and no help-center subdomain. The official FAQ/help content lives in the
Webflow CMS collection at `/news`, category tag **"FAQ LinkAja"**: ~30 server-rendered articles
(3 listing pages × 10) whose bodies are structured as question headings (`h3`) followed by answer
paragraphs — ideal Q&A units. robots.txt is permissive (only `/verify` and `/apps_tcash`
disallowed) but the four child sitemaps it declares all return 404, so discovery must walk the
`/news` pagination instead. The ToS (at `/syarat-ketentuan`) is scoped to the LinkAja *mobile app*
and contains an anti-automation clause; the public website is not explicitly addressed, so the ToS
verdict is "unclear, low risk for a polite private crawl" and must be re-raised at the gate review
(D11). See sections 3 and 4.

## 1. URL inventory

### 1.1 Primary FAQ surface — the `/news` CMS collection (category "FAQ LinkAja")

Listing entry points (server-rendered, 10 articles per page):

| URL | Status | Notes |
|---|---|---|
| `https://www.linkaja.id/news` | 200 | Webflow collection list, page 1, has `w-pagination-next` |
| `https://www.linkaja.id/news?04327379_page=2` | 200 | page 2, has next |
| `https://www.linkaja.id/news?04327379_page=3` | 200 | page 3, last (no `w-pagination-next`) |

The pagination parameter is literally `?04327379_page=N` (Webflow-generated, includes a hash).
Task 1.3 should walk pages while a `w-pagination-next` link exists rather than hardcoding `page=3`.

31 unique article URLs were enumerated from the three listing pages plus the "panduan" cards on
`/linkaja` and `/linkaja-syariah`. Categories below are the eyebrow tags rendered on the listing
cards; one-liners are derived from the slug (and, for the one fetched sample, its verified content).

| URL | Category | One-line description |
|---|---|---|
| `/news/aplikasi-linkaja-mode-maintenance` | FAQ LinkAja | What "app in maintenance mode" means and what to do |
| `/news/aplikasi-linkaja-tidak-bisa-dibuka` | FAQ LinkAja | Troubleshooting: app won't open |
| `/news/cara-aktivasi-linkaja-syariah` | FAQ LinkAja | How to activate LinkAja Syariah |
| `/news/cara-daftar-aktivasi-akun-linkaja` | FAQ LinkAja | How to register/activate a LinkAja account |
| `/news/cara-hapus-metode-pembayaran-linkaja` | FAQ LinkAja | How to remove a payment method |
| `/news/cara-kembali-ke-linkaja-reguler` | FAQ LinkAja | How to switch back from Syariah to regular |
| `/news/cara-pembayaran-pembelian-linkaja` | FAQ LinkAja | How purchase payments work |
| `/news/cara-tambah-metode-pembayaran-linkaja` | FAQ LinkAja | How to add a payment method |
| `/news/cara-upgrade-full-service-linkaja` | FAQ LinkAja | How to upgrade to Full Service (KTP eKYC) |
| `/news/fitur-linkaja-syariah` | FAQ LinkAja | What LinkAja Syariah features are |
| `/news/gagal-reset-pin-linkaja-dengan-pertanyaan-keamanan-coba-cek-ini` | FAQ LinkAja | Troubleshooting: PIN reset via security question fails |
| `/news/gagal-reset-pin-linkaja-via-email` | FAQ LinkAja | Troubleshooting: PIN reset via email fails |
| `/news/jumlah-cashback-linkaja-tidak-sesuai` | FAQ LinkAja | Troubleshooting: cashback amount doesn't match |
| `/news/kendala-security-question-linkaja` | FAQ LinkAja | Troubleshooting: security-question issues |
| `/news/kendala-verifikasi-email-linkaja` | FAQ LinkAja | Troubleshooting: email verification issues |
| `/news/limit-saldo-transaksi-linkaja` | FAQ LinkAja | Balance/transaction limit tiers (registered vs Full Service) |
| `/news/linkaja-berhenti-di-homepage` | FAQ LinkAja | Troubleshooting: app stuck on the home screen |
| `/news/pengajuan-full-service-linkaja-ditolak` | FAQ LinkAja | Troubleshooting: Full Service application rejected |
| `/news/tidak-bisa-cek-saldo-linkaja` | FAQ LinkAja | Troubleshooting: cannot check balance |
| `/news/tidak-bisa-hapus-metode-pembayaran-linkaja` | FAQ LinkAja | Troubleshooting: cannot remove a payment method |
| `/news/tidak-bisa-tambah-metode-pembayaran-linkaja` | FAQ LinkAja | Troubleshooting: cannot add a payment method |
| `/news/upgrade-full-service-masih-dalam-antrian` | FAQ LinkAja | Troubleshooting: Full Service upgrade stuck in queue |
| `/news/cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant` | (panduan card) | How to top up via ATM, mobile banking, merchants |
| `/news/cara-kirim-uang-pakai-linkaja-praktis-ke-sesama-linkaja-atau-rekening-bank` | (panduan card) | How to send money to LinkAja users and bank accounts |
| `/news/faq-mgames-linkaja` | (panduan card; fetched) | FAQ-style: mGames access, tokens, tournaments, troubleshooting |
| `/news/cara-hubungkan-linkaja-di-huawei-smartwatch` | Soft News | Pairing LinkAja with Huawei smartwatch |
| `/news/cara-hubungkan-linkaja-syariah-di-huawei-smartwatch` | Soft News | Pairing LinkAja Syariah with Huawei smartwatch |
| `/news/7skill-tahan-ai-2026` | Soft News | Marketing article (AI-resistant skills) — not FAQ |
| `/news/aktifkan-tombol-dana-sosial` | Syariah | Activating the donation (dana sosial) button |
| `/news/program-linkaja-berbagi` | Syariah | "LinkAja Berbagi" charity program |
| `/news/pemberitahuan-penghentian-layanan-pinjaman` | Berita LinkAja | Notice: loan-service termination — announcement, not FAQ |

Roughly 25 of the 31 are genuine FAQ/how-to/troubleshooting units; the rest are announcements and
marketing that 1.3 can either skip or keep with a category filter.

### 1.2 Secondary pages (adjacent public surface, all server-rendered)

| URL | Status | Notes |
|---|---|---|
| `https://www.linkaja.id/` | 200 | Webflow marketing home; links to `/news` panduan cards; no FAQ accordion |
| `https://www.linkaja.id/linkaja` | 200 | Consumer product page; "Ada panduannya" section links to `/news/cara-*` and `/news/faq-mgames-linkaja` |
| `https://www.linkaja.id/linkaja-syariah` | 200 | Syariah product page; same panduan pattern (Syariah how-to articles) |
| `https://www.linkaja.id/syarat-ketentuan` | 200 | Terms of Service (see section 4) |
| `https://www.linkaja.id/kebijakan-privasi` | 200 | Privacy policy (linked from footer; not fetched — legal doc, low FAQ value) |
| `https://www.linkaja.id/contact` | 200 | Contact channels page; links to `livechat.linkaja.id` |
| `https://www.linkaja.id/about`, `/business/disbursement`, `/business/embedded-wallet`, `/business/merchant-solutions`, `/business/smart-ledger` | linked | Corporate/B2B pages; no FAQ content expected (not fetched) |
| `https://company.linkaja.id/` | 200 | Byte-identical alias of `www.linkaja.id/` — same Webflow site |

### 1.3 Legacy surfaces (old consumer-site remnants on the same host)

| URL | Status | Notes |
|---|---|---|
| `https://www.linkaja.id/promo` | 200 | **JS-rendered** legacy page (jQuery 3.3.1/Bootstrap 4, "antikode" agency build): builds promo cards from `data[i].slug` client-side |
| `https://www.linkaja.id/promo/load_data/` | 307 | The JSON feed the legacy JS calls; now redirects to `/promo/` (HTML) — the endpoint is dead, so the listing renders empty without JS |
| `https://www.linkaja.id/promo/pertamina-isi-bbm-lebih-cepat`, `/promo/linkaja-tarik-tunai-linkaja-bisa-di-atm-himbara`, `/promo/linkaja-top-up-dunia-games-bisa-di-linkaja`, `/promo/promo-pay-day-linkaja`, `/promo/top-up-dunia-games-bisa-di-linkaja`, `/promo/top-up-games-upoint-id-bisa-di-linkaja` | linked | Hard-coded promo detail URLs still referenced from live pages (time-bound marketing; low FAQ value) |
| `https://www.linkaja.id/merchants` | 200 | Legacy "Katalog Beragam Merchants LinkAja" page; static, no collection items found; low FAQ value |
| `/cara-pake`, `/artikel`, `/saldo-bonus`, `/tentang`, `/syariah` | 404 | Legacy paths referenced from the old promo template footer — all dead |

### 1.4 Non-crawlable / dead / absent surfaces (checked so task 1.3 does not retry them)

| URL | Result | Notes |
|---|---|---|
| `https://www.linkaja.id/faq`, `/help` | 404 | No dedicated FAQ path on the Webflow site |
| `https://help.linkaja.id`, `https://support.linkaja.id` | DNS NXDOMAIN | No help-center subdomain exists |
| `https://www.linkaja.com`, `https://linkaja.com` | DNS NXDOMAIN | The old `.com` domain is gone entirely |
| `https://livechat.linkaja.id/` | 200 | 1071-byte JS shell, title "LinkAja Support", loads `/livechat.js` — a chat widget, no crawlable FAQ content (out of scope for scraping) |
| `https://linkaja.id/sitemap-pages.xml`, `sitemap-articles.xml`, `sitemap-promos.xml`, `sitemap-merchants.xml` | 404 | Declared in robots.txt and in `sitemap.xml`, but all four child sitemaps are gone; they serve the Webflow 404 page |

## 2. Rendering notes per page

- **Webflow pages** (home, `/linkaja`, `/linkaja-syariah`, `/contact`, `/syarat-ketentuan`,
  `/news` and every `/news/*` article): plain **server-rendered HTML**. Markers: `data-wf-domain`,
  `data-wf-site`, `data-wf-page` attributes and CSS from `cdn.prod.website-files.com`. No Next.js
  (`_next`), Nuxt (`__NUXT__`), Gatsby or React hydration markers found. Nav "dropdown"s (42
  occurrences on `/linkaja`) are Webflow interaction components — their labels are in the HTML;
  only open/close behavior is JS. **A plain HTTP GET captures all content; no headless browser
  needed.**
- **`/news` articles (the corpus source):** each article is an `h1` title plus `h3` subheadings
  that are frequently literal questions ("Apa Itu mGames LinkAja?", "Bagaimana Cara Mengakses
  mGames?", "Saldo Terpotong tapi Token Belum Masuk?") followed by answer paragraphs/lists. This
  maps directly onto the D4 "one chunk per Q&A unit" strategy: split at `h3` boundaries, embed
  question + answer. Each listing card also carries a category eyebrow ("FAQ LinkAja", "Soft
  News", "Syariah", "Berita LinkAja") usable as the corpus `category` field.
- **`/promo` (legacy):** only this part of the site is JS-rendered. The listing builds cards
  client-side from the `https://www.linkaja.id/promo/load_data/` JSON feed, which now 307-redirects
  to the HTML listing — the feed is dead, so without JS the listing is empty. Promo detail URLs
  that are hard-coded elsewhere still resolve. Content is time-bound marketing; recommend
  excluding from the corpus (or scraping only the hard-coded detail pages if the owner wants promos).
- **`/merchants` (legacy):** Antikode/jQuery-era page, static, no collection items detected.
- **`livechat.linkaja.id`:** 1 KB static shell + `/livechat.js` (custom support widget, last
  modified 2023). Nothing to crawl.
- **Infrastructure:** apex `linkaja.id`, `www.linkaja.id` and `company.linkaja.id` all serve the
  same site behind Cloudflare (`server: cloudflare-nginx`; a `_cfuvid` cookie is set with
  `Domain=company.linkaja.id`). robots.txt is byte-identical on apex and www.

## 3. robots.txt verdict

Fetched from both `https://www.linkaja.id/robots.txt` and `https://linkaja.id/robots.txt`
(identical, HTTP 200, 286 bytes). Quoted verbatim:

```
User-agent: *
Disallow: /verify
Disallow: /apps_tcash

Sitemap: https://linkaja.id/sitemap.xml
Sitemap: https://linkaja.id/sitemap-pages.xml
Sitemap: https://linkaja.id/sitemap-articles.xml
Sitemap: https://linkaja.id/sitemap-promos.xml
Sitemap: https://linkaja.id/sitemap-merchants.xml
```

**Verdict: permissive.** With a single `User-agent: *` group, every path on the site is allowed
except `/verify` and `/apps_tcash` (app-verification endpoints, irrelevant to the FAQ surface).
Every URL in section 1 is robots-allowed. Two caveats to record:

1. The robots.txt declares **five** `Sitemap:` lines (the planning notes said four — `sitemap.xml`
   plus four children); the design's D6 record should be read as "index + 4 children".
2. All four child sitemaps return **404** despite being declared — the sitemap surface is stale.
   Discovery for task 1.3 must walk `/news?04327379_page=N` pagination instead, and treat the
   broken sitemaps as the reason the corpus may silently miss brand-new articles on re-scrapes
   (mitigation: the coverage report in task 1.4 should re-walk the listing, not the sitemap).

## 4. ToS verdict

The Terms at `/syarat-ketentuan` ("Syarat dan Ketentuan Layanan — LinkAja", published by
PT Fintek Karya Nusantara / Finarya) are, by their own preamble and definitions, scoped to the
**Aplikasi LinkAja mobile app** ("Terima kasih telah menggunakan Aplikasi LinkAja…"), and nowhere
define the public website as a governed surface. Section **M. Kekayaan Intelektual** nevertheless
contains an explicit anti-automation clause: sub-clause (vi) bans "meluncurkan program otomatis
atau script, termasuk, namun tidak terbatas pada, web crawlers, web robots, web indexers, bots…
dan segala program apapun yang mungkin membuat beberapa permintaan server per detik" and sub-clause
(vii) bans "menggunakan robot, spider, pencarian situs/aplikasi pengambilan kembali, atau perangkat
manual atau otomatis lainnya… untuk mengambil indeks, 'tambang data' (data mine), atau dengan cara
apapun memperbanyak atau menghindari struktur navigasi atau presentasi dari Aplikasi LinkAja dan
isi yang terdapat di dalamnya"; the neighboring IP sub-clause (iii) bans republishing copyrighted
material without consent. On balance: **personal, polite (≥1 req/s), attributed scraping of the
public, robots-allowed marketing/help pages for a private tool appears tolerated but is not
explicitly licensed — verdict "unclear, low risk."** The robots.txt (permissive, with published
sitemaps) signals the site invites crawling, the ToS ban is textually aimed at the app and at
load-inducing automation, and a 1.2 s-interval crawl of ~30 static pages is far from "beban berat";
but because the clause's wording could be argued broadly and the corpus is a committed artifact
(reproduction), this verdict must be re-presented at the D11 gate review, and the D6 fallback
(owner-curated saved pages) remains the escape hatch if the owner judges it adverse. Nothing here
permits bypassing `/verify` or `/apps_tcash`, and public exposure of the corpus would require
revisiting the IP clause (already noted in the design's attribution risk).

## 5. Handoff notes for tasks 1.2 / 1.3

- **Corpus source:** `/news` articles only (plus, optionally, `/syarat-ketentuan` as a long-form
  fallback for the 400–800-token window chunking in D4). Expect ~30 documents → tens of Q&A
  chunks. Category tag from the listing eyebrow ("FAQ LinkAja" is the FAQ subset; "Soft News" /
  "Syariah" / "Berita LinkAja" are candidates to exclude or tag).
- **Discovery:** walk `https://www.linkaja.id/news?04327379_page=N` while `w-pagination-next`
  exists (3 pages today); do not rely on sitemaps (they 404).
- **Extraction:** split each article at `h3` boundaries; question = `h3` text, answer = following
  siblings until the next `h3`. The `h1` is the page title, not a question. Some articles end with
  "Masih Ada Kendala?" CTA sections — trim contact-CTA tails from answer units.
- **Politeness:** UA + ≥1 req/s was honored throughout this inventory; reuse the exact UA from D6.
  Cache raw HTML per URL under `data/raw/` (layout per task 1.2).
- **Do not bother with:** `/promo` JS feed (dead), `/merchants`, `livechat.linkaja.id`,
  `help./support.` subdomains (NXDOMAIN), `linkaja.com` (NXDOMAIN), child sitemaps (404).
- **Language:** all corpus content is Indonesian (`lang="id-ID"`), matching the D9 answer language.

## 6. Corpus schema specification (design D5) — task 1.2

Two JSON artifacts under `data/corpus/`. Both are plain JSON (UTF-8, no BOM), committed to git,
and carry a top-level `"schema_version": 1` (integer; bump on any breaking field change —
consumers reject a major version they do not know). The field names below are the single source
of truth: task 1.3 (python scraper) and task 2.2 (Go indexer) must implement them exactly as
spelled here.

### 6.1 `data/corpus/linkaja-faq.json` — the curated corpus (written by 1.3, read by 2.2)

```json
{
  "schema_version": 1,
  "scraped_at": "2026-11-06T09:30:00Z",
  "source": "linkaja.id",
  "units": [
    {
      "id": "limit-saldo-transaksi-linkaja_01",
      "source_url": "https://www.linkaja.id/news/limit-saldo-transaksi-linkaja",
      "question": "Berapa limit saldo dan transaksi LinkAja?",
      "answer": "Limit saldo akun terdaftar adalah Rp 2.000.000.\nLimit akun Full Service adalah Rp 20.000.000.",
      "category": "FAQ LinkAja",
      "language": "id",
      "content_hash": "69c5d36426d9f86dfc0d2dbd14b572f2b3f2400b91f51c1f6adcffcd8fbce3a3",
      "scraped_at": "2026-11-06T09:12:41Z"
    }
  ]
}
```

The `content_hash` above is the real SHA-256 produced by the 6.3 rule on this exact unit — use
it as the conformance test.

| Field | Type | Rule |
|---|---|---|
| `schema_version` | int | `1` for this change. |
| `scraped_at` | string | RFC 3339 UTC (`YYYY-MM-DDTHH:MM:SSZ`) — when the corpus file was generated. |
| `source` | string | Constant `"linkaja.id"`. |
| `units` | array | One unit per Q&A pair (D4 primary path); sorted by `id` ascending so re-runs produce stable diffs. |
| `units[].id` | string | `<article-slug>_<NN>`: last path segment of `source_url`, `_`, then the zero-padded 2-digit ordinal of the Q&A unit within the article in document order (`_01` even for single-unit articles). The underscore keeps ids parseable when the slug ends in a digit (`7skill-tahan-ai-2026_01`). Ids are stable human-readable labels, NOT integrity keys — dedupe/upsert keys on `content_hash` only. Ids MUST be unique within the corpus; the scraper fails loudly on collision. |
| `units[].source_url` | string | Absolute canonical URL: host normalized to `www.linkaja.id`, no query string, no trailing slash. |
| `units[].question` | string | The `h3` heading text per the section 5 extraction rule. |
| `units[].answer` | string | Answer body: each block (paragraph / list) trimmed with intra-line whitespace collapsed, blocks joined with a single `\n` (LF); list items rendered as `- item` lines; "Masih Ada Kendala?" contact-CTA tails trimmed (section 5). |
| `units[].category` | string or null | Exact eyebrow tag from the `/news` listing card (`"FAQ LinkAja"`, `"Soft News"`, `"Syariah"`, `"Berita LinkAja"`); `"panduan"` for articles reachable only via the product-page panduan cards; `null` only when genuinely unknown. |
| `units[].language` | string | BCP 47 tag; expected `"id"` (all corpus content is Indonesian — section 5). |
| `units[].content_hash` | string | Rule in 6.3. |
| `units[].scraped_at` | string | RFC 3339 UTC — when that article's HTML was actually fetched (per-unit, not the corpus timestamp). |

Long-form fallback (D4, only for articles without usable `h3` Q&A structure): one unit per
article, `question` = the page `h1` title, `answer` = the full body text under the same storage
rules.

### 6.2 `data/corpus/index-manifest.json` — index integrity manifest (written by 2.2)

Consumed by the 2.2 verification (manifest must match the stored `faq_chunks` rows) and the
Wave-3 eval (3.1/3.2 pin and score chunks by `content_hash`).

```json
{
  "schema_version": 1,
  "indexed_at": "2026-11-06T12:00:00Z",
  "model": "text-embedding-3-small",
  "dims": 1536,
  "documents": [
    {
      "source_url": "https://www.linkaja.id/news/limit-saldo-transaksi-linkaja",
      "content_hash": "69c5d36426d9f86dfc0d2dbd14b572f2b3f2400b91f51c1f6adcffcd8fbce3a3",
      "chunk_count": 1,
      "chunks": [{ "chunk_index": 0, "content_hash": "69c5d36426d9f86dfc0d2dbd14b572f2b3f2400b91f51c1f6adcffcd8fbce3a3" }]
    }
  ]
}
```

| Field | Rule |
|---|---|
| `indexed_at` | RFC 3339 UTC — manifest generation time. |
| `model` | Embedding model id from the `faq.embedding` config section (D2: `text-embedding-3-small`). |
| `dims` | Embedding dimension (1536 per D2). |
| `documents[]` | One entry per indexed corpus unit, identified by the (`source_url`, `content_hash`) pair — `source_url` alone is not unique because one article yields several Q&A units. `content_hash` must exist in `linkaja-faq.json`. |
| `documents[].chunk_count` | Derived chunk count: 1 on the primary one-chunk-per-unit path, N for the D4 long-form window fallback. |
| `documents[].chunks[].chunk_index` | 0-based ordinal in embedding order. |
| `documents[].chunks[].content_hash` | SHA-256 (6.3 rule) over that chunk's embedded text. On the 1:1 path, chunk 0's hash equals the document's `content_hash` (both are the unit hash), as in the example. |

### 6.3 `content_hash` — normalization rule (single source of truth)

`content_hash` = lowercase hex of SHA-256 over the UTF-8 bytes of `norm(question) + "\n" + norm(answer)`.

`norm(s)` (must be bit-identical in the python 1.3 scraper and the Go 2.2 indexer):

1. Replace every run of Unicode whitespace with a single ASCII space — equivalently python
   `" ".join(s.split())` or Go `strings.Join(strings.Fields(s), " ")` (both Unicode-aware; for
   this corpus the characters involved are just spaces, tabs and newlines).
2. Trim leading/trailing whitespace (the same functions do this).
3. Join with exactly one LF: `norm(question) + "\n" + norm(answer)`.
4. Hash the UTF-8 bytes; hex-encode lowercase (64 chars).

The rule intentionally erases internal formatting: the stored `answer` keeps `\n` between
blocks, the hash does not. The D4 long-form fallback hashes each chunk window with the same
`norm()` on the window text alone (no question prefix, no join).

Conformance vector (1.3 and 2.2 unit tests must reproduce it):

- input: `question = "Berapa limit saldo dan transaksi LinkAja?"`, stored
  `answer = "Limit saldo akun terdaftar adalah Rp 2.000.000.\nLimit akun Full Service adalah Rp 20.000.000."`
- hash input after `norm` + join: `Berapa limit saldo dan transaksi LinkAja?\nLimit saldo akun terdaftar adalah Rp 2.000.000. Limit akun Full Service adalah Rp 20.000.000.`
- `content_hash` = `69c5d36426d9f86dfc0d2dbd14b572f2b3f2400b91f51c1f6adcffcd8fbce3a3`

## 7. Politeness contract (design D6) — task 1.2

Binding for the task 1.3 spike scraper, the 4.1 CLI subcommands, and any manual probing of
linkaja.id:

1. **User agent.** Every request sends exactly `User-Agent: fantastic-faq-spike/0.1 (personal project; contact via repo)` — the same string used by the section 1–4 probes. No browser-spoofing UA.
2. **Rate.** Serial fetching only (no concurrent workers). Hard floor: ≥ 1.0 s between the start of consecutive requests (≤ 1 request/second). Optional jitter (e.g. +0–500 ms) may be added but never reduces the floor. The section 1–4 probes ran at a 1.2 s interval; that remains the recommended value.
3. **Robots compliance.** Per the section 3 verdict: allowed = every path except `/verify` and `/apps_tcash`; the expected crawl surface is `/news*` only. Re-fetch `https://www.linkaja.id/robots.txt` at the start of every scrape run; if it changed, stop and re-record the verdict before continuing. Disallowed paths are surfaced for an owner decision (D11 gate), never bypassed. Discovery walks `/news?04327379_page=N` while `w-pagination-next` exists — the declared child sitemaps 404 (section 3), so they are never used.
4. **Public pages only.** GET requests for public, unauthenticated content (HEAD permitted for probes). No cookies carried between requests, no logins, no form posts, no retry loops against Cloudflare challenges — a challenge response means stop and record. The dead surfaces in section 1.4 are not retried.
5. **Raw HTML cache** (D5: re-embedding must never re-crawl). Every response body is written to `data/raw/<slug>-<hash12>.html`, where:
   - `slug` = last path segment of the URL path, lowercased, characters outside `[a-z0-9-]` replaced by `-`, truncated to 64 chars; empty → `index`;
   - `hash12` = first 12 lowercase hex chars of SHA-256 over the full request URL as fetched (UTF-8, e.g. including `?04327379_page=2`) — this is what keeps the three `/news` pagination URLs in distinct files.

   Example: `https://www.linkaja.id/news?04327379_page=2` → `data/raw/news-<hash12>.html`.
   An optional sidecar `data/raw/cache-index.json` maps canonical URL → cache file:
   `{"schema_version": 1, "entries": {"<url>": {"file": "news-<hash12>.html", "fetched_at": "2026-11-06T09:12:41Z", "status": 200}}}`. The slug-hash convention is deterministic, so the sidecar is a convenience index, not the source of truth.

## 8. Data directory & git rules (task 1.2)

- `data/raw/` is **gitignored** — machine-local raw cache, reproducible by re-crawl; committing it would bloat the repo. Added to `.gitignore` verbatim as `data/raw/` in this task.
- `data/corpus/` is **committed** — the curated corpus and the index manifest are first-class reproducibility artifacts (D5, explicit owner request). `.gitignore` gains no broad `data/` rule, only the specific `data/raw/` entry, so the corpus stays tracked. Verified: `git check-ignore data/raw/test.html` exits 0 while `git check-ignore data/corpus/linkaja-faq.json` and `git check-ignore data/corpus/index-manifest.json` exit 1 (not ignored). Nothing was committed in this task.
