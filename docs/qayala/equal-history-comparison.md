# Qayala — Perbandingan dengan history setara

Semua konfigurasi diuji dalam satu suite live melalui Go + Eino, embedding OpenAI dan PostgreSQL + pgvector. Setiap interaksi memakai 10 pesan pengguna + 10 jawaban akhir, pesan tool yang utuh, serta rolling summary yang sama. Fingerprint history/summary identik untuk seluruh kasus.

Tiga pertanyaan gabungan × tiga pengulangan × empat konfigurasi = 36 interaksi terukur. Fixture FAQ versi 4, top-k 5, max_output_tokens 2048, tool berurutan, cache dinonaktifkan. Effort low/medium diverifikasi pada request. Database uji diverifikasi sebelum migrasi/penulisan dan dihapus setelah run.

## Asumsi proyeksi

- 33.574 interaksi/bulan; tarif Standard tanpa caching.
- Instruksi dalam tes tetap prompt sebelumnya, sekitar 210 token termasuk definisi tool dan format. Proyeksi menggunakan 520 token per panggilan Agent, sehingga tambahan input = (520 − 210) × jumlah panggilan Agent. Output dan jumlah panggilan diasumsikan tetap. Angka 520 bukan hasil pengujian prompt baru.
- Embedding query memakai jumlah token terukur. Indexing dokumen dialokasikan 1 juta token/bulan ($0,02).

## Rata-rata token per interaksi

| Komponen | Luna low | Luna medium | Terra low | Terra medium |
|---|---:|---:|---:|---:|
| Instruksi Agent, termasuk definisi tool | 1.040,0 | 1.040,0 | 1.040,0 | 1.040,0 |
| Input Agent lainnya: history, summary, pertanyaan, retrieval dan format | 36.701,7 | 36.702,0 | 36.650,4 | 36.681,1 |
| **Total input Agent** | 37.741,7 | 37.742,0 | 37.690,4 | 37.721,1 |
| Output Agent tanpa reasoning: tool call + jawaban | 312,1 | 303,7 | 284,2 | 299,8 |
| Input update summary | 1.965,0 | 1.965,0 | 1.965,0 | 1.965,0 |
| Output summary tanpa reasoning | 236,0 | 234,1 | 217,6 | 234,6 |
| Reasoning Agent | 0,0 | 173,0 | 0,0 | 3,4 |
| Reasoning summary | 28,8 | 41,4 | 0,0 | 0,0 |
| Embedding query | 71,0 | 71,0 | 45,8 | 58,3 |

Input Agent lainnya merupakan selisih total input dan alokasi instruksi. History dan retrieval belum dihitung terpisah; teks retrieval dikenakan biaya saat masuk ke input model. Reasoning dipisahkan dari output biasa agar tidak dihitung dua kali.

## Breakdown biaya bulanan — USD

| Komponen | Luna low | Luna medium | Terra low | Terra medium |
|---|---:|---:|---:|---:|
| Input Agent | $126,714 | $126,715 | $1.265,419 | $1.266,449 |
| Output Agent tanpa reasoning | $5,239 | $5,098 | $47,712 | $50,324 |
| Input update summary | $6,597 | $6,597 | $65,973 | $65,973 |
| Output summary tanpa reasoning | $3,962 | $3,930 | $36,521 | $39,375 |
| Reasoning Agent + summary | $0,483 | $3,600 | $0,000 | $0,578 |
| Embedding query | $0,048 | $0,048 | $0,031 | $0,039 |
| Indexing dokumen: alokasi 1 juta token/bulan | $0,020 | $0,020 | $0,020 | $0,020 |
| **Total AI/bulan** | **$143,06** | **$146,01** | **$1.415,68** | **$1.422,76** |

Total dihitung sebelum pembulatan. Biaya infrastruktur dan pajak belum termasuk.

Biaya aktual seluruh run, termasuk pemanasan dan indexing awal: $0.848641. Biaya run ini terpisah dari proyeksi bulanan.

## Panggilan dan reasoning

| Konfigurasi | Agent/interaksi | Summary/interaksi | Embedding/interaksi | Reasoning minimum–maksimum |
|---|---:|---:|---:|---:|
| gpt-6-luna low | 2,0 | 1,0 | 1,0 | 0–47 |
| gpt-6-luna medium | 2,0 | 1,0 | 1,0 | 147–289 |
| gpt-6.1-terra low | 2,0 | 1,0 | 1,0 | 0–0 |
| gpt-6.1-terra medium | 2,0 | 1,0 | 1,0 | 0–31 |

## Pertanyaan

1. Saya mendaftar Qayala memakai Google, lalu pindah ke HP baru. Di sisi lain akun LinkAja saya masih reguler dan belum tersambung ke Qayala. Jelaskan terpisah: apakah login Google selalu perlu OTP tambahan, apakah menu ganti PIN Qayala tersedia untuk saya, bagaimana menyambungkan LinkAja reguler, dan apakah setelah tersambung saya sudah bisa kirim serta tarik saldo pada MVP? Bedakan PIN Qayala dan PIN LinkAja; jangan mengasumsikan keduanya sama.
2. Saya bepergian ke kota baru tanpa internet. Jadwal salat pernah saya buka online di kota lama, lokasi terakhir masih tersimpan, dan GPS satelit mungkin tersedia. Mana yang masih bisa dipakai dan apa syaratnya untuk jadwal salat, arah kiblat, dan baca Al-Quran? Apakah jadwal pasti langsung sesuai kota baru, apakah murotal sudah ada, dan apakah pada MVP saya bisa memilih suara adzan serta reminder sebelum adzan? Tolong bedakan yang dijamin FAQ, bersyarat, dan fitur fase berikutnya.
3. Saya belum binding akun dan ingin membayar fidyah lewat Qayala untuk tiga anggota keluarga: 7, 11, dan 13 hari. Sebagai contoh, program yang saya pilih menampilkan rate Rp37.500 per hari dan anggaran saya Rp1.200.000. Hitung nominal masing-masing, total dan sisa anggaran; jangan menganggap rate ini universal. Jelaskan juga apakah saya harus binding dulu, apakah Wakaf dan Kurban sudah bisa diklik pada MVP, dan apakah transaksi gagal boleh dicatat sukses atau otomatis berarti saldo sudah terpotong. Pisahkan perhitungan dari ketentuan yang memang ada di FAQ.

## Batas pengukuran

- Semua hasil input/output/reasoning selain penyesuaian instruksi berasal dari API. Reasoning nol tetap dilaporkan nol. Sampel kecil belum mewakili seluruh trafik FAQ.
- Jawaban dan panjang query dapat berbeda antarmodel meskipun baseline dan pertanyaan identik. Window menyertakan tool call/hasil, sehingga token history lebih besar daripada teks percakapan saja.
- Pengujian memakai use case langsung; HTTP/auth tidak diuji. Bedrock tidak dipanggil. Angka bukan hasil pengukuran Bedrock.
- Tool paralel dinonaktifkan karena percobaan sebelumnya mengalami error parsing tool di Eino. Konfigurasi berurutan sama untuk keempat konfigurasi.

## Reproduksi

```sh
go run ./scripts/spike/qayala-usage --fixture scripts/spike/qayala-usage/fixture-terra-complex.json --models gpt-6-luna,gpt-6.1-terra --history-turns 10 --sequential-tools --repeats 3 --out /tmp/qayala-equal-history
python3 scripts/spike/qayala-usage/report_comparison.py --input /tmp/qayala-equal-history --output docs/qayala
```

Tarif per juta token: [Luna](https://developers.openai.com/api/docs/models/gpt-6-luna) input $0,10/output $0,50; [Terra](https://developers.openai.com/api/docs/models/gpt-6.1-terra) input $1/output $5; [embedding](https://developers.openai.com/api/docs/models/text-embedding-3-small) $0,02.
