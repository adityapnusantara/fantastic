# Qayala — Tes ulang reasoning Terra

Pengujian live OpenAI melalui Go + Eino, search_faq, embedding dan PostgreSQL + pgvector. Tiga pertanyaan gabungan × tiga pengulangan × dua effort = 18 interaksi terukur. Hasil ini terpisah dari benchmark sebelumnya.

## Metode

- Model: gpt-6.1-terra, effort low dan medium diverifikasi pada request aktual. Cache dinonaktifkan; max_output_tokens=2048.
- Setiap interaksi memakai salinan baseline yang sama: 10 user + 10 jawaban akhir assistant, beserta pesan tool call/hasil yang utuh, dan satu rolling summary. Total window aktual 40 row. Batas history dan jumlah jawaban diverifikasi.
- 11 turn pemanasan live menggunakan Luna low. Dokumen FAQ yang sama, versi 4, diindeks di database uji baru. current_database() diverifikasi sebelum migrasi dan penulisan.
- Instruksi Agent tetap sama dengan benchmark sebelumnya (teks inti 105 token, sekitar 210 token termasuk definisi tool dan format). Asumsi 520 token tidak diterapkan dalam tes ini.
- Jumlah panggilan tool mengikuti Agent. Tidak ada instruksi meminta chain-of-thought atau menambah reasoning secara artifisial. HTTP/auth tidak diuji.

## Reasoning per interaksi

Reasoning dijumlahkan dari semua panggilan Agent dan update summary dalam satu interaksi.

| Effort | Interaksi | Rata-rata | Minimum–maksimum | P95 sampel | Interaksi > 0 |
|---|---:|---:|---:|---:|---:|
| low | 9 | 0.00 | 0–0 | 0 | 0 |
| medium | 9 | 4.00 | 0–36 | 36 | 1 |

P95 memakai nearest-rank; dengan 9 interaksi per effort, nilainya sama dengan maksimum sampel. Jumlah sampel ini belum cukup untuk menetapkan P95 produksi.

## Per pertanyaan

| Pertanyaan | Effort | Rata-rata reasoning | Minimum–maksimum |
|---|---|---:|---:|
| q1 | low | 0.00 | 0–0 |
| q1 | medium | 0.00 | 0–0 |
| q2 | low | 0.00 | 0–0 |
| q2 | medium | 0.00 | 0–0 |
| q3 | low | 0.00 | 0–0 |
| q3 | medium | 12.00 | 0–36 |

## Usage dan biaya aktual

| Effort | Input/interaksi | Output termasuk reasoning | Embedding query | AI/interaksi | AI/bulan + indexing |
|---|---:|---:|---:|---:|---:|
| low | 39192.00 | 493.11 | 46.44 | $0.041658 | $1398.66 |
| medium | 39176.11 | 508.22 | 52.44 | $0.041718 | $1400.67 |

Proyeksi bulanan memakai 33.574 interaksi dan alokasi indexing 1 juta token ($0,02). Output API sudah mencakup reasoning; tidak dihitung dua kali. Pemanasan dan indexing sekali jalan tidak dimasukkan ke rata-rata biaya chat.

Biaya 18 interaksi terukur: $0.750391; pemanasan + indexing: $0.022385; total run: $0.772776.

## Tahap model

| Effort | Tahap | Panggilan/interaksi | Input | Output termasuk reasoning | Reasoning |
|---|---|---:|---:|---:|---:|
| low | agent | 2.00 | 37203.00 | 278.56 | 0.00 |
| low | summary | 1.00 | 1989.00 | 214.56 | 0.00 |
| low | embedding | 1.00 | 46.44 | 0.00 | 0.00 |
| medium | agent | 2.00 | 37187.11 | 297.67 | 4.00 |
| medium | summary | 1.00 | 1989.00 | 210.56 | 0.00 |
| medium | embedding | 1.00 | 52.44 | 0.00 | 0.00 |

## Pertanyaan yang diuji

1. Saya mendaftar Qayala memakai Google, lalu pindah ke HP baru. Di sisi lain akun LinkAja saya masih reguler dan belum tersambung ke Qayala. Jelaskan terpisah: apakah login Google selalu perlu OTP tambahan, apakah menu ganti PIN Qayala tersedia untuk saya, bagaimana menyambungkan LinkAja reguler, dan apakah setelah tersambung saya sudah bisa kirim serta tarik saldo pada MVP? Bedakan PIN Qayala dan PIN LinkAja; jangan mengasumsikan keduanya sama.
2. Saya bepergian ke kota baru tanpa internet. Jadwal salat pernah saya buka online di kota lama, lokasi terakhir masih tersimpan, dan GPS satelit mungkin tersedia. Mana yang masih bisa dipakai dan apa syaratnya untuk jadwal salat, arah kiblat, dan baca Al-Quran? Apakah jadwal pasti langsung sesuai kota baru, apakah murotal sudah ada, dan apakah pada MVP saya bisa memilih suara adzan serta reminder sebelum adzan? Tolong bedakan yang dijamin FAQ, bersyarat, dan fitur fase berikutnya.
3. Saya belum binding akun dan ingin membayar fidyah lewat Qayala untuk tiga anggota keluarga: 7, 11, dan 13 hari. Sebagai contoh, program yang saya pilih menampilkan rate Rp37.500 per hari dan anggaran saya Rp1.200.000. Hitung nominal masing-masing, total dan sisa anggaran; jangan menganggap rate ini universal. Jelaskan juga apakah saya harus binding dulu, apakah Wakaf dan Kurban sudah bisa diklik pada MVP, dan apakah transaksi gagal boleh dicatat sukses atau otomatis berarti saldo sudah terpotong. Pisahkan perhitungan dari ketentuan yang memang ada di FAQ.

## Batas interpretasi

- parallel_tool_calls_disabled=True. Tool search_faq dapat dipanggil beberapa kali secara berurutan. Konfigurasi ini dicatat karena berbeda dari default benchmark sebelumnya.
- Pertanyaan, panjang history, dan jumlah jawaban berubah dari tes sebelumnya. Selisih angka tidak bisa dianggap efek perubahan effort saja. Low dan medium pada run ini menggunakan workload dan baseline yang sama.
- Ini sampel FAQ Indonesia, bukan distribusi trafik produksi atau pengukuran Bedrock.
- Receipt API completed bukan evaluasi kualitas jawaban menyeluruh. Data jawaban dan summary disimpan untuk pemeriksaan.
- Angka nol tetap dilaporkan apabila usage API menyatakan nol. Pengukuran tidak diubah menjadi asumsi positif.
- Biaya infrastruktur dan pajak belum termasuk. Database uji dihapus oleh runner saat keluar; status cleanup diperiksa di output run.

## Reproduksi

```sh
go run ./scripts/spike/qayala-usage --fixture scripts/spike/qayala-usage/fixture-terra-complex.json --models gpt-6.1-terra --history-turns 10 --sequential-tools --repeats 3 --out /tmp/qayala-terra-complex
python3 scripts/spike/qayala-usage/report_reasoning.py --input /tmp/qayala-terra-complex --output docs/qayala
```

Tarif per juta token: [Terra](https://developers.openai.com/api/docs/models/gpt-6.1-terra) input $1/output $5; [Luna](https://developers.openai.com/api/docs/models/gpt-6-luna) input $0,10/output $0,50 untuk pemanasan; [embedding](https://developers.openai.com/api/docs/models/text-embedding-3-small) $0,02.

## Percobaan yang dikeluarkan

Percobaan awal berhenti pada kasus pertama karena Eino gagal membaca argumen function tool (EOF). Seluruh hasil run tersebut dikeluarkan dari statistik reasoning. Database uji dihapus sebelum run berikutnya. Request yang sudah terkirim tetap memiliki biaya.

- Run 1: biaya request tercatat, termasuk 11 turn pemanasan, $0.041048; indexing awal tidak terekam di metadata run yang berhenti dan belum termasuk angka ini.
