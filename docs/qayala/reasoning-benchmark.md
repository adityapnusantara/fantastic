# Uji Reasoning FAQ Qayala

Tanggal: **9 November 2026**. Pengujian langsung ke OpenAI Responses API, 5 ulangan untuk setiap kombinasi model dan effort. Setiap ulangan menjalankan 2 panggilan Agent + 1 update summary: **20 interaksi uji, 60 request terukur**.

Pertanyaan yang sama pada seluruh ulangan:

> Saya sudah punya akun LinkAja reguler. Bagaimana cara menghubungkannya ke Qayala, dan apakah setelah terhubung saya bisa langsung kirim saldo?

Input menggunakan 20 pesan sintetis dari FAQ bagian A–J dan satu rolling summary. Hasil tool `search_faq` dibuat tetap dari bagian H–I [FAQ Qayala versi 4](https://tcashsquad.atlassian.net/wiki/spaces/com/pages/5457182727). Update summary hanya memproses summary lama dan satu pasangan pesan yang keluar. Riwayat sudah memuat topik Lifestyle dan binding; ini bukan pertanyaan dengan topik baru dalam sesi.

| Model | Effort | Rata-rata reasoning/interaksi | Rentang 5 ulangan | Tool decision | Jawaban | Update summary |
|---|---|---:|---:|---:|---:|---:|
| GPT-6 Luna | `low` | 56,8 | 44–85 | 0 | 0 | 56,8 |
| GPT-6.1 Terra | `low` | 2,2 | 0–11 | 0 | 2,2 | 0 |
| GPT-6 Luna | `medium` | 193 | 159–225 | 0 | 100,6 | 92,4 |
| GPT-6.1 Terra | `medium` | 54 | 45–71 | 0 | 23,2 | 30,8 |

Angka per tahap merupakan rata-rata token. Total reasoning/interaksi menjumlahkan ketiga tahap. Field API `output_tokens` sudah mencakup reasoning; jangan menambahkannya lagi saat menghitung biaya dari usage aktual.

Seluruh request terukur berstatus `completed`, memakai tepat satu tool call per interaksi, dan tidak terpotong. Cache dinonaktifkan melalui `prompt_cache_options.mode: explicit` tanpa breakpoint; seluruh `cached_input_tokens` dan cache-write bernilai 0. [Dokumentasi caching](https://developers.openai.com/api/docs/guides/prompt-caching).

Proyeksi di analisis mempertahankan alokasi **13.000 input + 700 output non-reasoning** per interaksi, lalu menambahkan rata-rata reasoning hasil uji. Pemakaian aktual pada tes lebih pendek: sekitar 6.511–6.524 input dan 382–426 output non-reasoning per interaksi, dijumlahkan dari tiga request. Karena itu proyeksi merupakan gabungan asumsi perencanaan dan pengukuran reasoning, bukan biaya aktual tes yang langsung dikalikan trafik.

Hasil hanya berasal dari **satu pertanyaan yang diulang**, bukan evaluasi seluruh FAQ atau trafik produksi. Retrieval menggunakan data tetap; Eino, embedding, dan PostgreSQL tidak dijalankan. Belum ada pengujian Bedrock. Pemakaian reasoning dapat berubah pada pertanyaan lain, pencarian ulang, bahasa lain, dan history yang berbeda.

Biaya model untuk 60 request terukur adalah **$0,0964614** dengan tarif Standard OpenAI. Satu request setup sebelumnya tidak masuk statistik dan biayanya tidak tercatat. Pemeriksaan manual menemukan jawaban tentang perubahan akun reguler ke Syariah dan batas Kirim Saldo pada MVP konsisten di seluruh 20 jawaban; ini bukan evaluasi kualitas menyeluruh.

[Usage per request dan response ID](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/reasoning-benchmark-usage.csv) · [Perhitungan biaya](/Users/aditya/Documents/pribadi/fantastic/docs/qayala/cost-estimate.csv)
