# Analisis Arsitektur Agentic RAG Qayala — Fase 1

Tanggal analisis, uji reasoning, dan pemeriksaan harga: **9 November 2026**.

## 1. Requirements dan Considerations

Chatbot pusat bantuan Qayala menggunakan **Agentic RAG**. Cakupan fase 1 adalah FAQ aplikasi: fitur, panduan penggunaan, registrasi/login, binding akun, ibadah harian, donasi, dan zakat. Akses data transaksi pengguna serta tindakan pada akun belum termasuk.

| Area | Requirement / pilihan teknis | Consideration |
|---|---|---|
| Akses mobile | Mobile Apps Qayala langsung ke Go Service melalui HTTPS | Integrasi autentikasi mengikuti token Qayala; kontrak API dan streaming disepakati dengan tim mobile. |
| Bahasa pemrograman | **Go** | API chat, API knowledge management, streaming, akses database, dan proses dokumen dapat dibangun dalam satu bahasa. |
| Framework | **Eino ADK untuk Agentic RAG** | Agent mengendalikan loop model → tool → model: menyusun query, membaca hasil, mencari ulang bila diperlukan, lalu menjawab atau meminta klarifikasi. |
| Tool pencarian | `search_faq` di dalam Go Service | Tool menangani embedding dan retrieval. Agent dan tool merupakan komponen internal dalam satu service. Namespace Qayala dan versi indeks ditentukan backend. |
| Database | **PostgreSQL** | Menyimpan percakapan, pesan, konfigurasi Agent, FAQ, metadata, dan feedback. Kapasitas, akses, retensi, serta backup disesuaikan dengan kebutuhan layanan. |
| Vector store | **pgvector dalam PostgreSQL** | Vektor dan metadata FAQ berada di database yang sama. Model dan dimensi embedding harus konsisten pada dokumen dan query; pergantian model memerlukan indeks baru dan re-embedding. |
| Memory percakapan | **20 pesan pengguna/jawaban terbaru + 1 rolling summary** sebagai konteks model | 10 pasang tanya-jawab terbaru; pesan tool call dan hasil pencarian ikut menyertai konteks. Pesan lama diringkas bertahap agar chat dapat terus berlanjut. |
| Bahasa | **Indonesia, Inggris, dan Arab** | Model jawaban dan embedding bisa mendukung ketiganya. Pembahasan kualitas dilakukan pada tahap evaluasi. |
| Knowledge management | **Dokumen FAQ diunggah melalui WebView, dengan tampilan hasil chunking** | Pemrosesan dan penyimpanan knowledge ditangani Go Service; konten mengikuti cakupan fitur Qayala. |

Eino ADK menyediakan Agent dengan loop pemanggilan model dan tool; pgvector menyediakan pencarian vektor di PostgreSQL. Lihat [dokumentasi Eino Agent](https://www.cloudwego.io/docs/eino/core_modules/eino_adk/agent_implementation/chat_model/) dan [pgvector](https://github.com/pgvector/pgvector).

**Paket AI yang dibandingkan:**

| Komponen | OpenAI langsung | AWS Bedrock |
|---|---|---|
| Model jawaban dan tool calling | GPT-6 Luna — `gpt-6-luna`; GPT-6.1 Terra — `gpt-6.1-terra` | Global Cross-Region: `global.openai.gpt-6-luna` atau `global.openai.gpt-6.1-terra` |
| Reasoning effort | Setiap model dibandingkan pada `low` dan `medium` | Konfigurasi yang sama untuk proyeksi biaya Bedrock |
| Embedding | `text-embedding-3-small`, 1.536 dimensi | Titan Text Embeddings V2 — `amazon.titan-embed-text-v2:0`, 1.024 dimensi, **in-region** |
| Pertimbangan pemilihan | Pengelolaan akses dan tagihan melalui akun OpenAI | Pengelolaan akses dan tagihan melalui AWS; Global Cross-Region memungkinkan pemrosesan lintas region secara global |

Pemilihan provider mengikuti kesiapan akun, kebutuhan tata kelola, dan ketentuan lokasi pemrosesan. Model jawaban Bedrock memakai tarif Global; Titan tetap in-region karena tidak mendukung Global Cross-Region. [Model card Luna AWS](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-6-luna.html), [Terra AWS](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-6-1-terra.html), dan [Titan AWS](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-amazon-titan-text-embeddings-v2.html).

## 2. Flow / Diagram Drawio

### Service Agentic RAG

![Diagram service Agentic RAG Qayala](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/architecture.png)

[Edit diagram service di drawio](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/architecture.drawio)

### WebView Knowledge Management

![Diagram WebView Knowledge Management](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/knowledge-management.png)

[Edit diagram knowledge management di drawio](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/knowledge-management.drawio)

[Kedua diagram dalam satu file drawio](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/qayala-architecture.drawio)

## 3. Cost

Estimasi dalam **USD per bulan**, sebelum pajak dan diskon kontrak organisasi.

Biaya ditampilkan sebagai **upper estimate** untuk kondisi memory penuh dan **average estimate** berdasarkan campuran panjang sesi. Keduanya memakai baseline **33.574 interaksi/bulan**, alokasi instruksi **520 token/panggilan Agent**, serta indexing **1 juta token/bulan ($0,02)**.

**Tarif per satu juta token:**

| Komponen | OpenAI langsung | AWS Bedrock |
|---|---:|---:|
| Input GPT-6 Luna, Standard | $0,10 | $0,10 — Global Cross-Region |
| Output GPT-6 Luna, Standard | $0,50 | $0,50 — Global Cross-Region |
| Input GPT-6.1 Terra, Standard | $1,00 | $1,00 — Global Cross-Region |
| Output GPT-6.1 Terra, Standard | $5,00 | $5,00 — Global Cross-Region |
| Embedding | $0,02 | $0,02 — tarif perencanaan Titan* |

Sumber: OpenAI [Luna](https://developers.openai.com/api/docs/models/gpt-6-luna), [Terra](https://developers.openai.com/api/docs/models/gpt-6.1-terra), [embedding](https://developers.openai.com/api/docs/models/text-embedding-3-small); Bedrock Global [Luna](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-6-luna.html), [Terra](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-6-1-terra.html).

*Tarif Titan berasal dari [contoh biaya resmi AWS di US East](https://docs.aws.amazon.com/solutions/latest/generative-ai-application-builder-on-aws/cost.html). Region embedding dan tarif aktualnya perlu dikonfirmasi sebelum anggaran final.*

### Upper estimate — memory penuh

Baseline **33.574 interaksi per bulan**; satu interaksi adalah satu pertanyaan baru pengguna dan satu jawaban chatbot. Keempat konfigurasi diuji dalam satu suite: **3 pertanyaan × 3 ulangan per konfigurasi = 36 interaksi**, melalui Go + Eino, embedding, dan PostgreSQL + pgvector.

Setiap kasus memakai **10 pasang tanya-jawab + rolling summary yang identik**, lengkap dengan pesan tool call dan hasil pencarian (40 row pada pengujian). Seluruh konfigurasi memakai tool berurutan, model dan effort yang sama untuk Agent serta summary, dan tanpa caching. Tiap interaksi terukur menjalankan **2 panggilan Agent + 1 update summary + 1 embedding query**.

Proyeksi menggunakan **520 token instruksi per panggilan Agent**, termasuk definisi tool. Prompt tes sekitar 210 token; penyesuaian menjadi 520 menambahkan **(520 − 210) × 2 = 620 input token/interaksi**. Output dan jumlah panggilan diasumsikan tetap. Indexing dokumen dialokasikan **1 juta token/bulan**.

**Rata-rata token per interaksi:**

| Komponen | Luna `low` | Luna `medium` | Terra `low` | Terra `medium` |
|---|---:|---:|---:|---:|
| Instruksi Agent, termasuk definisi tool | 1.040 | 1.040 | 1.040 | 1.040 |
| Input lainnya: history, summary, pertanyaan, retrieval, dan format | 36.701,7 | 36.702,0 | 36.650,4 | 36.681,1 |
| **Total input Agent** | **37.741,7** | **37.742,0** | **37.690,4** | **37.721,1** |
| Output Agent tanpa reasoning: tool call + jawaban | 312,1 | 303,7 | 284,2 | 299,8 |
| Input update summary | 1.965 | 1.965 | 1.965 | 1.965 |
| Output summary tanpa reasoning | 236,0 | 234,1 | 217,6 | 234,6 |
| Reasoning Agent | 0 | 173,0 | 0 | 3,4 |
| Reasoning summary | 28,8 | 41,4 | 0 | 0 |
| Embedding query | 71,0 | 71,0 | 45,8 | 58,3 |

History dan retrieval belum diukur terpisah; keduanya termasuk input lainnya. Reasoning dipisahkan dari output biasa agar tidak dihitung dua kali. Angka nol mengikuti usage API pada sampel ini dan dapat berubah pada pertanyaan lain.

**Breakdown biaya bulanan — USD:**

| Komponen biaya | Luna `low` | Luna `medium` | Terra `low` | Terra `medium` |
|---|---:|---:|---:|---:|
| Input Agent | $126,714 | $126,715 | $1.265,419 | $1.266,449 |
| Output Agent tanpa reasoning | $5,239 | $5,098 | $47,712 | $50,324 |
| Input update summary | $6,597 | $6,597 | $65,973 | $65,973 |
| Output summary tanpa reasoning | $3,962 | $3,930 | $36,521 | $39,375 |
| Reasoning Agent + summary | $0,483 | $3,600 | $0,000 | $0,578 |
| Embedding query | $0,048 | $0,048 | $0,031 | $0,039 |
| Indexing dokumen: alokasi 1 juta token/bulan | $0,020 | $0,020 | $0,020 | $0,020 |
| **Total AI OpenAI/bulan** | **$143,06** | **$146,01** | **$1.415,68** | **$1.422,76** |
| **Proyeksi Bedrock Global + Titan/bulan** | **$143,06** | **$146,01** | **$1.415,68** | **$1.422,76** |

Biaya komponen chat dan embedding query = rata-rata token/interaksi × 33.574 × tarif per juta token ÷ 1.000.000; alokasi indexing bulanan ditambahkan terpisah. Total dihitung sebelum pembulatan. Proyeksi Bedrock menggunakan jumlah token yang sama serta tarif perencanaan Titan; Bedrock belum diuji langsung.

**Komponen kenaikan terbesar: input Agent.** Dibanding pengukuran sebelumnya dengan sekitar 5 pasang tanya-jawab, total input Agent naik dari sekitar **20.500 menjadi 37.700 token/interaksi (+84%)**. History kini memuat 10 pasang beserta tool call dan hasil retrieval yang dikirim kembali pada kedua panggilan Agent. Input update summary tetap sekitar 2.000 token. Pertanyaan dan output pada suite baru juga berbeda, sehingga selisih biaya bukan pengukuran pengaruh memory saja.

Sebagai contoh, biaya input Agent Luna `low` naik dari **$68,81 menjadi $126,71/bulan**, sedangkan Terra `low` dari **$685,76 menjadi $1.265,42/bulan**. Perubahan output dan reasoning memberi kontribusi biaya yang lebih kecil.

### Average estimate — campuran panjang sesi

Asumsi distribusi adalah **25% sesi masing-masing sepanjang 1, 3, 5, dan 15 pertanyaan**, sehingga rata-rata **6 pertanyaan/sesi**. Bobot ini merupakan asumsi perencanaan dan dapat diganti saat distribusi penggunaan produksi tersedia.

Pengujian menjalankan **3 urutan FAQ × 15 pertanyaan × 4 konfigurasi = 180 interaksi**, semuanya dimulai dari history kosong dengan batas 10 pasang tanya-jawab. Biaya sesi 1, 3, dan 5 pertanyaan diambil dari bagian awal masing-masing sesi 15 pertanyaan. Semua konfigurasi memakai urutan pertanyaan dan pengaturan yang sama; history serta output berkembang mengikuti jawaban masing-masing model. Penyesuaian instruksi menjadi 520 token diterapkan dengan cara yang sama seperti upper estimate.

`Average biaya/interaksi = total biaya sesi berbobot ÷ total pertanyaan berbobot`

`Average biaya/bulan = 33.574 × average biaya/interaksi + indexing bulanan`

Rata-rata biaya dihitung terhadap jumlah interaksi, sehingga sesi panjang berkontribusi sesuai jumlah pertanyaannya. Pada distribusi ini, **update summary terjadi sekitar 20,8% interaksi**, mulai setelah jawaban ke-11. Sesi pendek tidak menjalankan update summary. Rata-rata input Agent turun menjadi sekitar **18.800–19.000 token/interaksi**, dibanding sekitar 37.700 token saat memory penuh. Output dan reasoning memakai hasil pengukuran sesi baru.

**Rata-rata token per interaksi:**

| Komponen | Luna `low` | Luna `medium` | Terra `low` | Terra `medium` |
|---|---:|---:|---:|---:|
| Instruksi Agent, termasuk definisi tool | 1.040 | 1.040 | 1.040 | 1.040 |
| Input lainnya: history, summary, pertanyaan, retrieval, dan format | 17.790,5 | 17.984,4 | 17.842,5 | 17.947,1 |
| **Total input Agent** | **18.830,5** | **19.024,4** | **18.882,5** | **18.987,1** |
| Output Agent tanpa reasoning: tool call + jawaban | 116,4 | 120,5 | 110,7 | 116,2 |
| Input update summary | 410,2 | 412,6 | 411,4 | 410,3 |
| Output summary tanpa reasoning | 47,6 | 50,5 | 51,1 | 43,7 |
| Reasoning Agent | 0 | 23,8 | 0 | 0 |
| Reasoning summary | 5,3 | 10,0 | 0 | 0 |
| Embedding query | 30,0 | 29,0 | 23,0 | 28,1 |

Nilai summary pada tabel sudah dirata-ratakan terhadap **seluruh interaksi**, termasuk yang tidak memicu update summary. History dan retrieval tetap tergabung dalam input lainnya. Reasoning dipisahkan dari output biasa agar tidak dihitung dua kali; angka nol mengikuti hasil sampel pengujian average.

**Breakdown biaya bulanan — USD:**

| Komponen biaya | Luna `low` | Luna `medium` | Terra `low` | Terra `medium` |
|---|---:|---:|---:|---:|
| Input Agent | $63,222 | $63,872 | $633,961 | $637,471 |
| Output Agent tanpa reasoning | $1,955 | $2,022 | $18,587 | $19,499 |
| Input update summary | $1,377 | $1,385 | $13,812 | $13,774 |
| Output summary tanpa reasoning | $0,799 | $0,848 | $8,580 | $7,342 |
| Reasoning Agent + summary | $0,089 | $0,567 | $0,000 | $0,000 |
| Embedding query | $0,020 | $0,019 | $0,015 | $0,019 |
| Indexing dokumen: alokasi 1 juta token/bulan | $0,020 | $0,020 | $0,020 | $0,020 |
| **Total AI OpenAI/bulan** | **$67,48** | **$68,73** | **$674,97** | **$678,13** |
| **Proyeksi Bedrock Global + Titan/bulan** | **$67,48** | **$68,73** | **$674,97** | **$678,13** |

Komponen chat dan embedding query dihitung dari rata-rata token berbobot × 33.574 × tarif per juta token ÷ 1.000.000. Alokasi indexing bulanan ditambahkan terpisah; total dihitung sebelum pembulatan.

**Perbandingan total AI per bulan — USD:**

| Estimasi | Luna `low` | Luna `medium` | Terra `low` | Terra `medium` |
|---|---:|---:|---:|---:|
| Upper estimate — OpenAI | $143,06 | $146,01 | $1.415,68 | $1.422,76 |
| **Average estimate — OpenAI** | **$67,48** | **$68,73** | **$674,97** | **$678,13** |
| Average estimate — proyeksi Bedrock Global + Titan | $67,48 | $68,73 | $674,97 | $678,13 |

Average estimate pada skenario ini sekitar **52–53% lebih rendah** daripada upper estimate. Selisih mencakup perubahan history, frekuensi summary, pertanyaan, output, dan reasoning; angka ini belum merupakan rata-rata trafik produksi. Proyeksi Bedrock tetap memakai jumlah token yang sama dan tarif perencanaan Titan, tanpa pengujian langsung.

[Rincian average estimate JSON](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/session-average.json) · [Usage pengujian sesi CSV](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/session-average.csv)

Estimasi mencakup model Agent, update summary, reasoning, embedding query, dan indexing dokumen. Pencarian ulang dapat menambah biaya. Infrastruktur serta pajak belum termasuk; sampel ini belum mewakili seluruh trafik produksi.

[Laporan pengujian dengan history setara](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/equal-history-comparison.md) · [Usage setiap request CSV](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/equal-history-comparison.csv) · [Rincian perhitungan JSON](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/equal-history-comparison.json)
