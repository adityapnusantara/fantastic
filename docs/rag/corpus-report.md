# LinkAja FAQ corpus validation & coverage report (task 1.4)

OpenSpec change `add-faq-rag-search`, task 1.4. Validates `data/corpus/linkaja-faq.json`
(produced by task 1.3) against the binding schema of `docs/rag/inventory.md` sections 6–7,
dedupes by `content_hash`, re-walks the live `/news` listing for a captured/missed coverage
map, and lists corpus gaps for the Wave-3 golden-set freeze (task 3.1).

Method: `scripts/spike/validate_corpus_1_4.py` (throwaway, python3 stdlib only; imports
`scrape_faq.py` for the binding constants/parsers so the two spike scripts cannot drift;
deleted together with it in task 4.1). Run 2026-11-06:

- `python3 -I scripts/spike/validate_corpus_1_4.py` — full run, exit 0.
- Network footprint: 6 serial GETs at ≥1.2 s spacing (robots.txt re-check + 5 listing
  pages), UA `fantastic-faq-spike/0.1 (personal project; contact via repo)`, `/news*` only,
  no cookies, no retries needed, 0 failures, 0 Cloudflare challenges. Articles were NOT
  re-fetched (the 1.3 cache in `data/raw/` is authoritative for article bodies and was read
  read-only); `data/raw/` was not written by this task.

---

## 1. Schema validation — PASS (0 violations)

All 420 units checked against inventory 6.1/6.3 by the script (not by eye):

| Check | Result |
|---|---|
| File UTF-8, no BOM; parses as JSON | ok |
| Top-level keys exactly `{schema_version, scraped_at, source, units}` | ok |
| `schema_version == 1` (int); `source == "linkaja.id"` | ok |
| Corpus `scraped_at` RFC 3339 UTC `Z` (`2026-11-05T18:17:35Z`) | ok |
| Unit keys exactly `{id, source_url, question, answer, category, language, content_hash, scraped_at}` on all 420 | ok |
| `id` = `<slug>_<NN>` and id prefix == last path segment of `source_url`; ids unique; units sorted ascending | ok |
| `source_url` canonical `https://www.linkaja.id/news/<slug>` (no query, no trailing slash) | ok |
| `question`/`answer` non-empty strings (no answerless chunks) | ok |
| `category` in `{FAQ LinkAja, Soft News, Syariah, Berita LinkAja, panduan, null}` | ok (values used: FAQ LinkAja / Soft News / Syariah / Berita LinkAja) |
| `language == "id"` on all units | ok |
| `scraped_at` RFC 3339 UTC per unit | ok |
| **`content_hash` recomputed = sha256(norm(q)+"\n"+norm(a)) matches stored, 420/420** | ok |
| Inventory 6.3 conformance vector (`69c5d364…bce3a3`) reproduced at startup | ok |

**Verdict: `schema violations: NONE`.**

## 2. Dedupe by `content_hash` — PASS (0 duplicates)

- 420 units → **420 distinct `content_hash` values; 0 duplicate groups.**
- Informational: 0 exact-duplicate answers across units. 17 question strings repeat across
  articles (worst: "1. pastikan koneksi internet stabil" ×4, "hubungi layanan pelanggan
  linkaja" ×4, "cara menambah metode pembayaran di linkaja" ×3) — see section 4c; the units
  themselves are distinct because the answers differ.

## 3. Coverage — 44/44 captured, 0 missed

The 1.1 inventory table is **stale and was not used as the walk target** (it enumerated 31
URLs from 3 listing pages; the live collection has grown). The listing was re-walked live on
2026-11-06 following `w-pagination-next` dynamically:

- Live `/news` listing: **5 pages** (`?04327379_page=2..5` beyond page 1), 10 cards on pages
  1–4, 8 on page 5 → **44 articles**, all HTTP 200.
- **Live vs 1.3 cache drift: 0 new, 0 gone** — the corpus snapshot (cached 2026-11-05T18:08–18:17Z)
  is still current.
- **Live vs corpus: 44/44 articles captured (0 missed); 0 corpus articles absent from the live
  listing.** The 31 URLs from the stale 1.1 table are all among the 44; the 13 articles that
  appeared since 1.1 are: `cek-status-binding-linkaja`, `riwayat-transaksi-linkaja-tidak-muncul`,
  `gangguan-layanan-linkaja`, `cara-mengatasi-token-linkaja-bermasalah`,
  `tidak-bisa-scan-qris-linkaja`, `kendala-pembelian-pembayaran-linkaja`,
  `salah-nomor-tujuan-atau-nominal-saat-kirim-uang-linkaja-ini-yang-harus-dilakukan`,
  `isi-saldo-linkaja-gagal-ini-yang-harus-kamu-lakukan`, `jaga-akun-linkaja-tetap-aman-ganti-pin-dan-atur-pertanyaan-keamanan`,
  `cara-tarik-tunai-tanpa-kartu-pakai-linkaja-praktis-dan-mudah`, `layanan-pelanggan-linkaja`,
  `produktif-tanpa-overwork-bisa-ikuti-7-cara-ini`, `daftar-biaya-admin-di-linkaja`.
- Live card eyebrows re-read during this walk agree with the stored `category` on all 44
  articles (36 FAQ LinkAja / 4 Soft News / 2 Syariah / 2 Berita LinkAja — identical to the
  corpus histogram).

Coverage map — every live article URL (live-walk card order), its live eyebrow, corpus unit
count, capture status:

| Article | Live category | Units | Status |
|---|---|---|---|
| `cara-daftar-aktivasi-akun-linkaja` | FAQ LinkAja | 14 | captured |
| `program-linkaja-berbagi` | Syariah | 1 | captured |
| `cara-hubungkan-linkaja-syariah-di-huawei-smartwatch` | Soft News | 1 | captured |
| `cara-hubungkan-linkaja-di-huawei-smartwatch` | Soft News | 1 | captured |
| `aktifkan-tombol-dana-sosial` | Syariah | 1 | captured |
| `pemberitahuan-penghentian-layanan-pinjaman` | Berita LinkAja | 1 | captured |
| `jumlah-cashback-linkaja-tidak-sesuai` | FAQ LinkAja | 5 | captured |
| `cara-upgrade-full-service-linkaja` | FAQ LinkAja | 3 | captured |
| `upgrade-full-service-masih-dalam-antrian` | FAQ LinkAja | 12 | captured |
| `pengajuan-full-service-linkaja-ditolak` | FAQ LinkAja | 13 | captured |
| `kendala-security-question-linkaja` | FAQ LinkAja | 11 | captured |
| `kendala-verifikasi-email-linkaja` | FAQ LinkAja | 13 | captured |
| `tidak-bisa-cek-saldo-linkaja` | FAQ LinkAja | 12 | captured |
| `gagal-reset-pin-linkaja-via-email` | FAQ LinkAja | 12 | captured |
| `7skill-tahan-ai-2026` | Soft News | 11 | captured |
| `gagal-reset-pin-linkaja-dengan-pertanyaan-keamanan-coba-cek-ini` | FAQ LinkAja | 9 | captured |
| `aplikasi-linkaja-mode-maintenance` | FAQ LinkAja | 9 | captured |
| `aplikasi-linkaja-tidak-bisa-dibuka` | FAQ LinkAja | 12 | captured |
| `linkaja-berhenti-di-homepage` | FAQ LinkAja | 11 | captured |
| `fitur-linkaja-syariah` | FAQ LinkAja | 12 | captured |
| `cara-kembali-ke-linkaja-reguler` | FAQ LinkAja | 8 | captured |
| `cara-aktivasi-linkaja-syariah` | FAQ LinkAja | 11 | captured |
| `cara-pembayaran-pembelian-linkaja` | FAQ LinkAja | 10 | captured |
| `limit-saldo-transaksi-linkaja` | FAQ LinkAja | 14 | captured |
| `tidak-bisa-tambah-metode-pembayaran-linkaja` | FAQ LinkAja | 13 | captured |
| `cara-tambah-metode-pembayaran-linkaja` | FAQ LinkAja | 9 | captured |
| `tidak-bisa-hapus-metode-pembayaran-linkaja` | FAQ LinkAja | 9 | captured |
| `cara-hapus-metode-pembayaran-linkaja` | FAQ LinkAja | 8 | captured |
| `cek-status-binding-linkaja` | FAQ LinkAja | 16 | captured |
| `riwayat-transaksi-linkaja-tidak-muncul` | FAQ LinkAja | 10 | captured |
| `gangguan-layanan-linkaja` | FAQ LinkAja | 10 | captured |
| `faq-mgames-linkaja` | FAQ LinkAja | 11 | captured |
| `cara-mengatasi-token-linkaja-bermasalah` | FAQ LinkAja | 11 | captured |
| `tidak-bisa-scan-qris-linkaja` | FAQ LinkAja | 11 | captured |
| `kendala-pembelian-pembayaran-linkaja` | FAQ LinkAja | 12 | captured |
| `salah-nomor-tujuan-atau-nominal-saat-kirim-uang-linkaja-ini-yang-harus-dilakukan` | FAQ LinkAja | 10 | captured |
| `isi-saldo-linkaja-gagal-ini-yang-harus-kamu-lakukan` | FAQ LinkAja | 9 | captured |
| `cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant` | FAQ LinkAja | 14 | captured |
| `cara-kirim-uang-pakai-linkaja-praktis-ke-sesama-linkaja-atau-rekening-bank` | FAQ LinkAja | 15 | captured |
| `jaga-akun-linkaja-tetap-aman-ganti-pin-dan-atur-pertanyaan-keamanan` | FAQ LinkAja | 10 | captured |
| `cara-tarik-tunai-tanpa-kartu-pakai-linkaja-praktis-dan-mudah` | FAQ LinkAja | 18 | captured |
| `layanan-pelanggan-linkaja` | FAQ LinkAja | 11 | captured |
| `produktif-tanpa-overwork-bisa-ikuti-7-cara-ini` | Soft News | 5 | captured |
| `daftar-biaya-admin-di-linkaja` | Berita LinkAja | 1 | captured |

## 4. Gap list (feeds task 3.1 golden-set freeze)

### (a) Image-only fee table — the fee-schedule page contributes no fee amounts

`daftar-biaya-admin-di-linkaja` (live category Berita LinkAja, article literally titled
"Daftar Biaya Admin di LinkAja!") has **no usable Q&A structure** (task 1.3 long-form
fallback). Verified in its cached HTML: the article body is a single `w-richtext` container
holding **4 `<img>` elements (the fee tables are images) and 0 `<table>` elements**. The
corpus unit `daftar-biaya-admin-di-linkaja_01` is a 138-char long-form unit whose answer
effectively ends at the line "Biaya Admin Isi Saldo LinkAja" — **no fee amounts in text**.

Consequence for the planned golden question **"Berapa biaya admin untuk top up LinkAja?"**:
a site-wide scan finds **no unit containing isi-saldo/top-up fee amounts**. The closest unit,
`cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant_07`, only says fees "dapat
berbeda tergantung bank atau merchant" and to check the amount in the chosen channel — a
correct but amountless answer. (Transfer fees *are* in text: `cara-kirim-uang…_07` has
"biaya admin mulai dari Rp1.000"; ATM Himbara cash withdrawal *is* in text:
`cara-tarik-tunai…_17` has "biaya admin Rp5.000" — but those are different transactions.)
Options for 3.1, per the D11 substitution rule: substitute this question with the owner, or
keep it but pin the expected chunk to `cara-isi-saldo…_07` with an amountless pinned fact
("biaya admin isi saldo berbeda tergantung bank/merchant dan ditampilkan sebelum transaksi")
— decided with the owner BEFORE freezing.

### (b) Units dropped / degraded at extraction (from task 1.3, re-confirmed here)

- **6 long-form fallback units** (= the only 6 single-unit articles, independently confirmed
  by this task's histogram): `aktifkan-tombol-dana-sosial`, `program-linkaja-berbagi`,
  `pemberitahuan-penghentian-layanan-pinjaman`, `daftar-biaya-admin-di-linkaja`,
  `cara-hubungkan-linkaja-di-huawei-smartwatch`, `cara-hubungkan-linkaja-syariah-di-huawei-smartwatch`.
  These embed as one big chunk each (marketing/announcement content in 4 of 6) — acceptable,
  but they are the weakest chunks for FAQ retrieval.
- **2 question-only parent headings dropped** (headings with no body — nothing recoverable):
  `7skill-tahan-ai-2026_02`-position and `kendala-verifikasi-email-linkaja_04`-position headings.
- **1 contact-CTA section dropped** ("Masih Ada Kendala?" in `faq-mgames-linkaja`) — correct per spec.
- **2 prefaces dropped per spec-strict rule** (content before the first heading):
  `cara-daftar-aktivasi-akun-linkaja` (228 chars) and `produktif-tanpa-overwork…` (550 chars).
- Images inside answer blocks skipped: the 4 fee-table images above + 1 in
  `limit-saldo-transaksi-linkaja`. 36 decorative zero-width headings (1/article) skipped — cosmetic.

### (c) Thin / collision-prone content

- **1 tiny unit**: `limit-saldo-transaksi-linkaja_06` ("Kenapa Transaksi Bisa Terkendala
  Limit?" → 38-char lead-in "Ada beberapa kondisi yang paling umum.") — a lead-in fragment;
  retrieval on this question should not pin this chunk.
- **64 small units** (50–199 chars) — mostly numbered step-headings with one-line answers;
  fine for retrieval but weak as standalone answers.
- **17 exactly-duplicate question headings across articles** (worst ×4: "1. pastikan koneksi
  internet stabil", "hubungi layanan pelanggan linkaja"; ×3: "cara menambah metode pembayaran
  di linkaja"). The units are distinct (answers differ ⇒ different `content_hash`), but a
  golden question phrased like these generic headings could legitimately rank a sibling
  article's chunk — avoid pinning expected chunks on generic numbered-step headings.
- 26 long units (≥600 chars, max 3180) — fine for D4 one-chunk-per-unit, but they make the
  largest embedded texts and the slowest embeddings.

## 5. Quality histogram

- **Corpus size**: 420 units over 44 articles (all 44 captured from the live listing).
- **Units per article**: min 1, median 11, max 18.
  Distribution `{1:6, 3:1, 5:2, 8:2, 9:5, 10:5, 11:8, 12:6, 13:3, 14:3, 15:1, 16:1, 18:1}`
  (the `1` bucket is exactly the 6 long-form fallback articles).
- **Answer length (characters)**: min 38, median 350, max 3180. Buckets:
  tiny <50: **1** · small 50–199: **64** · medium 200–599: **329** · long ≥600: **26**.
- **Categories** (articles/units): FAQ LinkAja 36/398 · Soft News 4/18 · Syariah 2/2 ·
  Berita LinkAja 2/2.

Retrieval-eval takeaway: 93% of units are 50–600 chars — comfortable one-chunk-per-unit
(D4 primary path) sizes; only 1 unit is below 50 chars and only 26 exceed 600.
