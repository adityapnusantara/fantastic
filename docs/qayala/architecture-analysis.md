# Analisis Arsitektur Agentic RAG Qayala — Fase 1

Tanggal analisis dan pemeriksaan harga: **8 November 2026**.

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
| Memory percakapan | **10 pesan terbaru + 1 rolling summary** sebagai konteks model | Sekitar 5 pasang tanya-jawab terbaru; pesan lama diringkas bertahap agar chat dapat terus berlanjut. |
| Bahasa | **Indonesia, Inggris, dan Arab** | Model jawaban dan embedding bisa mendukung ketiganya. Pembahasan kualitas dilakukan pada tahap evaluasi. |
| Knowledge management | **Dokumen FAQ diunggah melalui WebView, dengan tampilan hasil chunking** | Pemrosesan dan penyimpanan knowledge ditangani Go Service; konten mengikuti cakupan fitur Qayala. |

Eino ADK menyediakan Agent dengan loop pemanggilan model dan tool; pgvector menyediakan pencarian vektor di PostgreSQL. Lihat [dokumentasi Eino Agent](https://www.cloudwego.io/docs/eino/core_modules/eino_adk/agent_implementation/chat_model/) dan [pgvector](https://github.com/pgvector/pgvector).

**Paket AI yang dibandingkan:**

| Komponen | OpenAI langsung | AWS Bedrock |
|---|---|---|
| Model jawaban dan tool calling | GPT-6 Luna — `gpt-6-luna` | GPT-6 Luna Global Cross-Region — `global.openai.gpt-6-luna` |
| Embedding | `text-embedding-3-small`, 1.536 dimensi | Titan Text Embeddings V2 — `amazon.titan-embed-text-v2:0`, 1.024 dimensi, **in-region** |
| Pertimbangan pemilihan | Pengelolaan akses dan tagihan melalui akun OpenAI | Pengelolaan akses dan tagihan melalui AWS; Global Cross-Region memungkinkan pemrosesan lintas region secara global |

Pemilihan provider mengikuti kesiapan akun, kebutuhan tata kelola, dan ketentuan lokasi pemrosesan. Model jawaban Bedrock memakai tarif Global; Titan tetap in-region karena tidak mendukung Global Cross-Region. [Model card GPT-6 Luna AWS](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-6-luna.html), [model card Titan AWS](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-amazon-titan-text-embeddings-v2.html).

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

**Tarif per satu juta token:**

| Komponen | OpenAI langsung | AWS Bedrock |
|---|---:|---:|
| Input GPT-6 Luna, Standard | $0,10 | $0,10 — Global Cross-Region |
| Output GPT-6 Luna, Standard | $0,50 | $0,50 — Global Cross-Region |
| Embedding | $0,02 | $0,02 — tarif perencanaan Titan* |

Sumber: [harga GPT-6 Luna OpenAI](https://developers.openai.com/api/docs/models/gpt-6-luna), [harga Bedrock Global](https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-openai-gpt-6-luna.html), dan [harga embedding OpenAI](https://developers.openai.com/api/docs/models/text-embedding-3-small).

*Tarif Titan berasal dari [contoh biaya resmi AWS di US East](https://docs.aws.amazon.com/solutions/latest/generative-ai-application-builder-on-aws/cost.html). Region embedding dan tarif aktualnya perlu dikonfirmasi sebelum anggaran final.*

**Asumsi memory dan biaya per pertanyaan:**

Estimasi saat memory sudah penuh. Chat dapat terus berlanjut; setiap pasangan pesan lama yang keluar diringkas ke rolling summary.

| Komponen | Asumsi |
|---|---|
| Memory | **10 pesan** ≈ 5 pasang tanya-jawab (**2.000 token**) + summary **300 token** |
| Model per pertanyaan | **2 panggilan Agent + 1 update summary**; total **9.000 token input + 700 token output**, termasuk konteks dan retrieval |
| Embedding query | **1 pencarian × 100 token** per pertanyaan |
| Indexing dokumen | Alokasi **1 juta token/bulan** |

Agent dan summary memakai model yang sama, reasoning `none`, tanpa caching. Biaya per pertanyaan ≈ **$0,001252** (model + embedding query), ditambah indexing **$0,02/bulan**. Dengan N pertanyaan, **biaya AI bulanan = N × $0,001252 + $0,02**. Pemakaian aktual mengikuti jumlah token.

Baseline **33.574 interaksi per bulan**, dengan satu interaksi dihitung sebagai satu pertanyaan baru pengguna dan satu jawaban chatbot.

| Pertanyaan/bulan | Agent + update summary | Embedding query + indexing | Total AI OpenAI | Total AI Bedrock Global + Titan |
|---:|---:|---:|---:|---:|
| 33.574 | $41,968 | $0,087 | **$42,055** | **$42,055** |

Estimasi mencakup pemakaian model dan embedding. Pencarian ulang oleh Agent dapat menambah biaya. Biaya infrastruktur belum termasuk dan mengikuti alokasi layanan yang digunakan.

[Rincian perhitungan dan skenario biaya CSV](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/cost-estimate.csv)
