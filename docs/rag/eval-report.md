# FAQ RAG Eval Report — retrieval quality (task 3.2)

Generated at: 2026-11-05T21:26:29Z by `go run ./scripts/spike/faq-eval` (re-run command at the bottom).

## Binding scoring rule (verbatim from `data/corpus/golden-set.json` `scoring_rule`)

> a hit is scored at the CHUNK level — top-k must contain the chunk whose `content_hash` equals the pinned expected chunk; MRR on the same unit

Hit@k therefore means: the pinned chunk (`content_hash` equality) appears within the top-k. URL-level hits are recorded only as failure-analysis evidence, never scored. Retrieval is the production path unchanged: pgvector cosine `embedding <=>` ordering, `score = 1 - distance`, top-k=5, no similarity floor, no reranking (design D7/D11).

## Run metadata

| Field | Value |
|---|---|
| Embedding model | `text-embedding-3-small` (config `faq.embedding`, design D2) |
| Embedding dims | 1536 |
| Indexed corpus | 44 documents / 421 chunks (`faq_documents` / `faq_chunks` counts at run time; manifest: 44 docs / 421 chunks) |
| Fixture | `data/corpus/golden-set.json` frozen at 2026-11-05T18:56:52Z |
| Scored questions | 9 (frozen set of 10 minus 1 unresolvable drop — see substitutions) |
| Top-k | 5 (design D7 default) |
| Diagnostic depth | 20 (miss analysis only; not scored) |
| Retrieval SQL | `internal/adapters/repositories/postgres/faq_search.go` `FaqSearchStore.Search` (unchanged 2.3 code) |
| Embedder build | `internal/cmd/faq.go` `faqEmbedder` pattern: eino-ext `components/embedding/openai` from `faq.embedding` config |

## Aggregate results vs gate bar (design D11)

| Metric | Value | Bar | Verdict |
|---|---|---|---|
| hit@1 | 0.3333 (3/9) | — | — |
| hit@3 | 0.5556 (5/9) | — | — |
| hit@5 | 0.8889 (8/9) | ≥ 0.8 | PASS |
| MRR | 0.4796 | ≥ 0.6 | FAIL |

**Retrieval-only gate (hit@5 ≥ 0.8 AND MRR ≥ 0.6): FAIL.** The full D11 gate also includes corpus-coverage review, ≥7/10 demo answers (owner-judged, task 3.3/3.4) and owner sign-off — not measured here.

## Per-question results (scored set)

Rank 0 = pinned chunk not in top-`k`. MRR contribution = 1/rank of the pinned chunk, 0 when absent.

| n | Question | Pinned hash in top-`k` | Rank | hit@1 | hit@3 | hit@5 | MRR contrib | embed ms | retrieve ms |
|---|---|---|---|---|---|---|---|---|---|
| 1 | Apa itu LinkAja dan apa saja layanannya? | yes | 5 | fail | fail | PASS | 0.2000 | 790 | 10 |
| 2 | Bagaimana cara top up / isi saldo LinkAja? | yes | 5 | fail | fail | PASS | 0.2000 | 681 | 7 |
| 3 | Berapa limit saldo dan transaksi LinkAja? | yes | 1 | PASS | PASS | PASS | 1.0000 | 496 | 6 |
| 4 | Bagaimana cara transfer saldo LinkAja ke pengguna lain atau ke bank? | no | 0 | fail | fail | fail | 0.0000 | 318 | 7 |
| 5 | Bagaimana cara reset PIN LinkAja kalau lupa? | yes | 3 | fail | PASS | PASS | 0.3333 | 543 | 8 |
| 6 | Bagaimana cara menghubungi customer service LinkAja? | yes | 1 | PASS | PASS | PASS | 1.0000 | 318 | 3 |
| 7 | Apa itu LinkAja Syariah dan apa bedanya dengan LinkAja biasa? | yes | 1 | PASS | PASS | PASS | 1.0000 | 403 | 7 |
| 9 | Di mana saja LinkAja bisa dipakai untuk bayar (merchant/QRIS)? | yes | 4 | fail | fail | PASS | 0.2500 | 504 | 7 |
| 10 | Bagaimana cara upgrade akun ke KYC terverifikasi dan apa manfaatnya? | yes | 3 | fail | PASS | PASS | 0.3333 | 484 | 7 |

## Provisional substitution / drop decisions (owner decisions, recorded by the orchestrator — USER HOLDS VETO AT THE GATE)

These decisions are **provisional** and must be re-confirmed by the owner before the D11 gate verdict is final:

- **Original Q3 (fee-table question, "Berapa biaya admin untuk top up LinkAja?") was substituted** with the limit-saldo question (`n=3`, "Berapa limit saldo dan transaksi LinkAja?") because fee tables are images with no extractable amounts (see `known_gaps` in the fixture and `docs/rag/corpus-report.md` §4a). Status: `substitution_status: pending_user_confirmation`.
- **Original Q8 (account closure, "Bagaimana cara menghapus atau menutup akun LinkAja?") is DROPPED from the scored set** — the topic is genuinely absent from the corpus (no unit or article covers account closure; fixture `n=8` has `expected_chunk_hash: null`, `substitution_status: unresolvable`). The scored set is therefore **9 questions**. Escalation per fixture: add a corpus article or replace the golden question.

Fixture `known_gaps` (verbatim):

- `n=8` (Account lifecycle): Topic genuinely absent from the corpus: no unit in data/corpus/linkaja-faq.json (420 units, questions and answers both scanned) covers deleting/closing/deactivating a LinkAja account. All 'hapus'/'tutup' hits are unrelated (metode pembayaran removal, 'Tutup dan Buka Kembali Aplikasi' restart step). No page-URL fallback pinned because no article is about account closure either. Escalate to owner: add a corpus article or replace this golden question.

## Failure analysis inputs (for the 3.3 decision memo)

For every question that is not rank 1: where the pinned chunk actually reached (diagnostic depth, same SQL path), whether the expected article URL surfaced in the top-`k`, and what the top-1 chunk was instead (topical-adjacency evidence). Nothing here changes the score — scoring stays chunk-level per the binding rule.

### n=1 — Apa itu LinkAja dan apa saja layanannya?

- Pinned chunk: `0e9b88c1dd4d…` (article https://www.linkaja.id/news/pengajuan-full-service-linkaja-ditolak)
- Pinned chunk rank in top-`k`: **5** (score 0.6570) — hit@5 yes, drags MRR to 0.2000
- Expected URL in top-`5`: yes
- Top-1 instead: https://www.linkaja.id/news/cara-pembayaran-pembelian-linkaja (score 0.6724, same article as pinned: no)
- Top-1 text starts with: “Apa Saja yang Bisa Dibayar atau Dibeli di LinkAja? Melalui aplikasi LinkAja, kamu bisa mengakses berbagai laya…”
- Top-`k` listing:

  | # | pinned? | score | source URL | chunk starts with |
  |---|---|---|---|---|
  | 1 | no | 0.6724 | https://www.linkaja.id/news/cara-pembayaran-pembelian-linkaja | Apa Saja yang Bisa Dibayar atau Dibeli di LinkAja? Melalui aplikasi LinkAja, kamu bisa mengakses berbagai laya… |
  | 2 | no | 0.6718 | https://www.linkaja.id/news/cara-aktivasi-linkaja-syariah | Apa Itu LinkAja Syariah? LinkAja Syariah merupakan fitur layanan uang elektronik LinkAja yang dikelola sesuai … |
  | 3 | no | 0.6683 | https://www.linkaja.id/news/fitur-linkaja-syariah | Apa Itu LinkAja Syariah? LinkAja Syariah adalah fitur layanan uang elektronik LinkAja yang dikelola sesuai kai… |
  | 4 | no | 0.6631 | https://www.linkaja.id/news/cara-daftar-aktivasi-akun-linkaja | LinkAja atau LinkAja Syariah? Kamu tidak perlu membuat akun terpisah untuk menggunakan LinkAja Syariah. Saat p… |
  | 5 | yes | 0.6570 | https://www.linkaja.id/news/pengajuan-full-service-linkaja-ditolak | Apa Itu LinkAja Full Service? LinkAja memiliki dua tipe akun, yaitu Basic Service dan Full Service. Akun Basic… |

### n=2 — Bagaimana cara top up / isi saldo LinkAja?

- Pinned chunk: `0642a0d958ad…` (article https://www.linkaja.id/news/cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant)
- Pinned chunk rank in top-`k`: **5** (score 0.7573) — hit@5 yes, drags MRR to 0.2000
- Expected URL in top-`5`: yes
- Top-1 instead: https://www.linkaja.id/news/cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant (score 0.7915, same article as pinned: yes)
- Top-1 text starts with: “Cara Isi Saldo LinkAja via ATM Kalau sedang berada dekat ATM, kamu juga bisa langsung mengisi saldo LinkAja. S…”
- Top-`k` listing:

  | # | pinned? | score | source URL | chunk starts with |
  |---|---|---|---|---|
  | 1 | no | 0.7915 | https://www.linkaja.id/news/cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant | Cara Isi Saldo LinkAja via ATM Kalau sedang berada dekat ATM, kamu juga bisa langsung mengisi saldo LinkAja. S… |
  | 2 | no | 0.7611 | https://www.linkaja.id/news/fitur-linkaja-syariah | 6. Isi Saldo Sebelum bertransaksi, kamu bisa mengisi saldo LinkAja melalui bank atau channel yang bekerja sama… |
  | 3 | no | 0.7597 | https://www.linkaja.id/news/cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant | Cara Isi Saldo LinkAja via Mobile Banking Nggak mau keluar rumah? Isi saldo juga bisa dilakukan lewat mobile b… |
  | 4 | no | 0.7583 | https://www.linkaja.id/news/cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant | Cara Isi Saldo LinkAja via Bank Kamu bisa mengisi saldo LinkAja melalui berbagai bank, termasuk Bank Himbara: … |
  | 5 | yes | 0.7573 | https://www.linkaja.id/news/cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant | Cara Isi Saldo LinkAja via ATM, Mobile Banking, dan Merchant Saldo LinkAja mulai menipis? Tenang, isi saldo bi… |

### n=4 — Bagaimana cara transfer saldo LinkAja ke pengguna lain atau ke bank?

- Pinned chunk: `41f729990c25…` (article https://www.linkaja.id/news/cara-kirim-uang-pakai-linkaja-praktis-ke-sesama-linkaja-atau-rekening-bank)
- Pinned chunk rank in top-`k`: **absent (rank 0, score n/a)**
- Diagnostic: pinned chunk **does exist at rank 7 of the deeper (k=20) search** — a ranking/threshold problem, not an indexing problem
- Expected URL in top-`5`: yes
- Top-1 instead: https://www.linkaja.id/news/cara-kirim-uang-pakai-linkaja-praktis-ke-sesama-linkaja-atau-rekening-bank (score 0.8217, same article as pinned: yes)
- Top-1 text starts with: “Cara Transfer Saldo LinkAja ke Rekening Bank Selain ke sesama pengguna LinkAja, kamu juga bisa mengirim saldo …”
- Top-`k` listing:

  | # | pinned? | score | source URL | chunk starts with |
  |---|---|---|---|---|
  | 1 | no | 0.8217 | https://www.linkaja.id/news/cara-kirim-uang-pakai-linkaja-praktis-ke-sesama-linkaja-atau-rekening-bank | Cara Transfer Saldo LinkAja ke Rekening Bank Selain ke sesama pengguna LinkAja, kamu juga bisa mengirim saldo … |
  | 2 | no | 0.7865 | https://www.linkaja.id/news/cara-kirim-uang-pakai-linkaja-praktis-ke-sesama-linkaja-atau-rekening-bank | Cara Kirim Uang ke Sesama Pengguna LinkAja Mau kirim saldo ke teman atau keluarga yang sama-sama menggunakan L… |
  | 3 | no | 0.7519 | https://www.linkaja.id/news/cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant | Cara Isi Saldo LinkAja via ATM Kalau sedang berada dekat ATM, kamu juga bisa langsung mengisi saldo LinkAja. S… |
  | 4 | no | 0.7498 | https://www.linkaja.id/news/cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant | Cara Isi Saldo LinkAja via Bank Kamu bisa mengisi saldo LinkAja melalui berbagai bank, termasuk Bank Himbara: … |
  | 5 | no | 0.7496 | https://www.linkaja.id/news/cara-isi-saldo-linkaja-via-atm-mobile-banking-dan-merchant | Cara Isi Saldo LinkAja via Internet Banking Selain mobile banking, kamu juga dapat menggunakan layanan interne… |

### n=5 — Bagaimana cara reset PIN LinkAja kalau lupa?

- Pinned chunk: `9da17a9bc71a…` (article https://www.linkaja.id/news/gagal-reset-pin-linkaja-via-email)
- Pinned chunk rank in top-`k`: **3** (score 0.7734) — hit@5 yes, drags MRR to 0.3333
- Expected URL in top-`5`: yes
- Top-1 instead: https://www.linkaja.id/news/gagal-reset-pin-linkaja-dengan-pertanyaan-keamanan-coba-cek-ini (score 0.7869, same article as pinned: no)
- Top-1 text starts with: “Gagal Reset PIN LinkAja dengan Pertanyaan Keamanan? Coba Cek Ini Lupa PIN LinkAja dan mencoba melakukan reset …”
- Top-`k` listing:

  | # | pinned? | score | source URL | chunk starts with |
  |---|---|---|---|---|
  | 1 | no | 0.7869 | https://www.linkaja.id/news/gagal-reset-pin-linkaja-dengan-pertanyaan-keamanan-coba-cek-ini | Gagal Reset PIN LinkAja dengan Pertanyaan Keamanan? Coba Cek Ini Lupa PIN LinkAja dan mencoba melakukan reset … |
  | 2 | no | 0.7745 | https://www.linkaja.id/news/gagal-reset-pin-linkaja-dengan-pertanyaan-keamanan-coba-cek-ini | Cara Reset PIN dengan Pertanyaan Keamanan Jika metode Pertanyaan Keamanan tersedia pada aplikasi kamu, secara … |
  | 3 | yes | 0.7734 | https://www.linkaja.id/news/gagal-reset-pin-linkaja-via-email | Bagaimana Reset PIN LinkAja via Email? Email menjadi salah satu metode verifikasi yang dapat digunakan ketika … |
  | 4 | no | 0.7615 | https://www.linkaja.id/news/gagal-reset-pin-linkaja-via-email | Bisa Reset PIN dengan Cara Lain? Bisa. Selain menggunakan email, LinkAja juga memiliki metode reset PIN melalu… |
  | 5 | no | 0.7602 | https://www.linkaja.id/news/gagal-reset-pin-linkaja-via-email | Gagal Reset PIN LinkAja via Email? Coba Cek Ini Lupa PIN LinkAja dan mencoba melakukan reset melalui email, ta… |

### n=9 — Di mana saja LinkAja bisa dipakai untuk bayar (merchant/QRIS)?

- Pinned chunk: `0a10a7613dfd…` (article https://www.linkaja.id/news/cara-pembayaran-pembelian-linkaja)
- Pinned chunk rank in top-`k`: **4** (score 0.7231) — hit@5 yes, drags MRR to 0.2500
- Expected URL in top-`5`: yes
- Top-1 instead: https://www.linkaja.id/news/fitur-linkaja-syariah (score 0.8076, same article as pinned: no)
- Top-1 text starts with: “1. Bayar di Merchant dengan QRIS Pakai LinkAja Syariah untuk membayar transaksi di merchant yang menerima QRIS…”
- Top-`k` listing:

  | # | pinned? | score | source URL | chunk starts with |
  |---|---|---|---|---|
  | 1 | no | 0.8076 | https://www.linkaja.id/news/fitur-linkaja-syariah | 1. Bayar di Merchant dengan QRIS Pakai LinkAja Syariah untuk membayar transaksi di merchant yang menerima QRIS… |
  | 2 | no | 0.8044 | https://www.linkaja.id/news/cara-pembayaran-pembelian-linkaja | Mau Bayar Langsung di Merchant? Pakai QRIS Selain melakukan pembelian dan pembayaran dari menu aplikasi, LinkA… |
  | 3 | no | 0.7705 | https://www.linkaja.id/news/tidak-bisa-scan-qris-linkaja | 4. Gunakan Menu Bayar di Aplikasi LinkAja Untuk melakukan pembayaran dengan scan QRIS: - Buka aplikasi LinkAja… |
  | 4 | yes | 0.7231 | https://www.linkaja.id/news/cara-pembayaran-pembelian-linkaja | Apa Saja yang Bisa Dibayar atau Dibeli di LinkAja? Melalui aplikasi LinkAja, kamu bisa mengakses berbagai laya… |
  | 5 | no | 0.7118 | https://www.linkaja.id/news/cara-pembayaran-pembelian-linkaja | Cara Melakukan Pembayaran atau Pembelian Pakai LinkAja Bayar tagihan, beli pulsa, paket data, token listrik, a… |

### n=10 — Bagaimana cara upgrade akun ke KYC terverifikasi dan apa manfaatnya?

- Pinned chunk: `ffede28ad0a6…` (article https://www.linkaja.id/news/cara-upgrade-full-service-linkaja)
- Pinned chunk rank in top-`k`: **3** (score 0.5979) — hit@5 yes, drags MRR to 0.3333
- Expected URL in top-`5`: yes
- Top-1 instead: https://www.linkaja.id/news/upgrade-full-service-masih-dalam-antrian (score 0.6287, same article as pinned: no)
- Top-1 text starts with: “Mengapa Upgrade Full Service Perlu Diverifikasi? Full Service memberikan akses terhadap fitur dan limit transa…”
- Top-`k` listing:

  | # | pinned? | score | source URL | chunk starts with |
  |---|---|---|---|---|
  | 1 | no | 0.6287 | https://www.linkaja.id/news/upgrade-full-service-masih-dalam-antrian | Mengapa Upgrade Full Service Perlu Diverifikasi? Full Service memberikan akses terhadap fitur dan limit transa… |
  | 2 | no | 0.6272 | https://www.linkaja.id/news/upgrade-full-service-masih-dalam-antrian | Bagaimana Proses Upgrade Full Service? Secara umum, proses upgrade dilakukan langsung melalui aplikasi LinkAja… |
  | 3 | yes | 0.5979 | https://www.linkaja.id/news/cara-upgrade-full-service-linkaja | Cara Upgrade Akun LinkAja ke Full Service Upgrade dapat dilakukan langsung melalui aplikasi LinkAja tanpa perl… |
  | 4 | no | 0.5854 | https://www.linkaja.id/news/pengajuan-full-service-linkaja-ditolak | Bagaimana Cara Upgrade ke Full Service? Pengajuan Full Service dapat dilakukan langsung melalui aplikasi LinkA… |
  | 5 | no | 0.5753 | https://www.linkaja.id/news/upgrade-full-service-masih-dalam-antrian | Apa yang Perlu Diperhatikan Selama Proses Verifikasi? Selama menunggu proses upgrade selesai, ada beberapa hal… |

## Embedding cost (estimate, text-embedding-3-small)

| Item | Value |
|---|---|
| Question characters (scored set) | 478 |
| Estimated tokens (chars ÷ 4) | 120 |
| Measured tokens (one batched `/embeddings` usage probe, same model) | 140 |
| Measured cost @ $0.02 / 1M tokens | $0.00000280 |
| Estimated cost @ $0.02 / 1M tokens | $0.00000240 |
| Per-question query embed latency | see table above (embed ms ≈ one OpenAI round trip) |

Full-corpus re-index cost for scale context: ~49.9k tokens ≈ $0.001 per `faq index` run (task 2.2 measurement).

## Repeatability

The runner is deterministic against the frozen fixture: same questions → same embeddings pipeline → same per-question ranks, **modulo** (a) the report timestamp and (b) latency/cost columns (real network timing) and (c) OpenAI embedding bit-nondeterminism across calls (same-text cosine ≥ 0.9988 between consecutive runs, task 2.2 measurement) which can in principle flip near-tied ranks.

Re-run command (from the repo root; docker postgres on 5555 must be up, `OPENAI_API_KEY` resolvable via `.env` or environment):

```sh
go run ./scripts/spike/faq-eval
```

Flags: `--config`, `--golden`, `--report`, `--json`, `--database-url`, `--k`, `--diag-k`, `--skip-cost-probe`. The runner exits non-zero if any pinned `expected_chunk_hash` is absent from `faq_chunks` (stale index vs frozen fixture guard).
