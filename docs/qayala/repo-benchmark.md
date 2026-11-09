# Qayala — Pengukuran Usage melalui Repository

Pengujian 9 November 2026. Seluruh angka input, output, reasoning, dan embedding query di bawah berasal dari usage API pada alur repo; tidak memakai asumsi 13.000/700 token.

## Metode

- Menjalankan `chat.NewService` dan Eino ADK, tool `search_faq`, embedding OpenAI, pencarian PostgreSQL + pgvector, penyimpanan pesan, serta rolling summary asinkron. HTTP/auth tidak diuji karena bukan pembentuk prompt/token.
- Fixture: FAQ Qayala versi 4, 11 bagian, diindeks memakai pipeline chunking/embedding repo ke database benchmark lokal yang baru. `current_database()` diverifikasi sebelum migrasi/penulisan. Database uji dihapus setelah selesai.
- Prompt dan deskripsi tool disesuaikan untuk Qayala; implementasi retrieval, service chat, repository dan compaction tidak diubah. Tidak ada hasil retrieval tiruan.
- 6 turn pemanasan live menghasilkan history dan summary nyata. Setiap pengulangan memakai salinan baseline yang sama dari 24 row tersimpan, bukan melanjutkan ulangan sebelumnya.
- Top-k retrieval: 5; history_window: 20 row; max_output_tokens: 2048. Effort low/medium diverifikasi pada request aktual. Cache dinonaktifkan dan store=false untuk pengukuran.
- Empat konfigurasi × 5 ulangan = 20 interaksi terukur; 80 request: 40 Agent, 20 summary, 20 embedding query. Warmup dan indexing terpisah dari biaya per interaksi.

Pertanyaan yang sama:

> Saya sudah punya akun LinkAja reguler. Bagaimana cara menghubungkannya ke Qayala, dan apakah setelah terhubung saya bisa langsung kirim saldo?

## Token aktual dan proyeksi biaya

| Model | Effort | Input model/interaksi | Output non-reasoning | Reasoning | Embedding query | Chat + embedding/bulan |
|---|---|---:|---:|---:|---:|---:|
| gpt-6-luna | low | 21,861.2 | 385.2 | 32.6 | 41.6 | $80.44 |
| gpt-6.1-terra | low | 21,792.4 | 392.0 | 0.0 | 33.0 | $797.49 |
| gpt-6-luna | medium | 21,866.2 | 383.2 | 38.6 | 46.6 | $80.53 |
| gpt-6.1-terra | medium | 21,842.8 | 398.6 | 0.0 | 40.8 | $800.29 |

Angka token adalah rata-rata per interaksi, dijumlahkan dari seluruh panggilan aktual. Output API sudah mencakup reasoning sehingga tidak dihitung dua kali. Proyeksi = 33.574 × rata-rata biaya aktual per interaksi, dengan tarif Standard OpenAI dan tanpa caching.

Biaya bulanan tabel mencakup model Agent, update summary, dan embedding query. Indexing dokumen baru belum termasuk: bila tetap memakai alokasi perencanaan 1 juta token/bulan, tambahkan $0,02. Biaya infrastruktur dan pajak belum termasuk.

## Rincian per tahap

| Model | Effort | Tahap | Panggilan/interaksi | Input/interaksi | Output termasuk reasoning | Reasoning |
|---|---|---|---:|---:|---:|---:|
| gpt-6-luna | low | agent | 2.0 | 19874.2 | 156.6 | 0.0 |
| gpt-6-luna | low | summary | 1.0 | 1987.0 | 261.2 | 32.6 |
| gpt-6-luna | low | embedding | 1.0 | 41.6 | 0.0 | 0.0 |
| gpt-6.1-terra | low | agent | 2.0 | 19805.4 | 179.8 | 0.0 |
| gpt-6.1-terra | low | summary | 1.0 | 1987.0 | 212.2 | 0.0 |
| gpt-6.1-terra | low | embedding | 1.0 | 33.0 | 0.0 | 0.0 |
| gpt-6-luna | medium | agent | 2.0 | 19879.2 | 162.4 | 0.0 |
| gpt-6-luna | medium | summary | 1.0 | 1987.0 | 259.4 | 38.6 |
| gpt-6-luna | medium | embedding | 1.0 | 46.6 | 0.0 | 0.0 |
| gpt-6.1-terra | medium | agent | 2.0 | 19855.8 | 188.6 | 0.0 |
| gpt-6.1-terra | medium | summary | 1.0 | 1987.0 | 210.0 | 0.0 |
| gpt-6.1-terra | medium | embedding | 1.0 | 40.8 | 0.0 | 0.0 |

## Perbedaan dengan rancangan dan batas pengukuran

- Window repo berisi 20 **row mentah**, termasuk tool call/hasil: baseline pengukuran ini berisi 5 user, 10 assistant, dan 5 tool. Assistant mencakup pesan pemanggilan tool, bukan hanya jawaban akhir. Ini belum sama dengan rancangan 20 pesan pengguna/jawaban akhir ≈ 10 pasang.
- Hasil retrieval ikut disimpan dan dikirim pada history. Summary memproses seluruh row yang keluar, termasuk hasil tool, bukan selalu tepat satu pasang tanya-jawab.
- Jumlah tool call dan panjang hasil retrieval mengikuti keputusan Agent. Tidak dipaksa satu pencarian; variasinya tercatat pada hasil per interaksi.
- Satu pertanyaan diulang lima kali untuk setiap konfigurasi. Ini angka nyata untuk workload uji ini, bukan rata-rata representatif seluruh FAQ, bahasa, panjang sesi, atau trafik produksi.
- Respons completed dan summary tersimpan tidak otomatis menjamin kualitas jawaban/summary. Pengukuran ini berfokus pada konsumsi token, bukan evaluasi kualitas menyeluruh.
- Bedrock tidak diuji. Angka ini tidak boleh disebut hasil pengukuran Bedrock.
- Pada sampel ini Terra mencatat 0 token reasoning di low maupun medium meskipun parameter effort pada request sudah diverifikasi. Ini bukan jaminan penggunaan reasoning selalu nol pada pertanyaan lain.
- Uji eksplorasi awal mengalami kesalahan pemilihan database. Hanya 5 percakapan benchmark, agent benchmark, dan 11 dokumen fixture yang dibuat runner telah dihapus dengan pemeriksaan ID/hash. Keberadaan sisa data benchmark di database aplikasi diverifikasi nol. Hasil eksplorasi tersebut dikeluarkan dari laporan ini; suite ini dijalankan ulang setelah verifikasi isolasi sebelum migrasi.

## Reproduksi

```sh
go run ./scripts/spike/qayala-usage --repeats 5 --out /tmp/qayala-repo-usage
python3 scripts/spike/qayala-usage/report.py --input /tmp/qayala-repo-usage --output docs/qayala
```

Runner membaca konfigurasi dan kredensial lokal dari .env tanpa menuliskannya ke hasil. Memerlukan PostgreSQL lokal dengan izin membuat database; hanya database baru dengan prefix qayala_bench_ yang dibuat/dihapus. Pemanggilan OpenAI dikenai biaya.

[Usage setiap request](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/repo-benchmark-usage.csv) · [Hasil JSON](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/repo-benchmark-results.json)

Tarif input/output per juta token: [Luna](https://developers.openai.com/api/docs/models/gpt-6-luna) $0,10/$0,50; [Terra](https://developers.openai.com/api/docs/models/gpt-6.1-terra) $1/$5; [embedding](https://developers.openai.com/api/docs/models/text-embedding-3-small) $0,02.
