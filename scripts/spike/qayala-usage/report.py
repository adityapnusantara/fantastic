"""Summarize measured repository usage; no API or database access."""
import argparse
import csv
import json
from collections import Counter
from decimal import Decimal as D
from pathlib import Path


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, default=Path("docs/qayala"))
    args = parser.parse_args()
    metadata = json.loads((args.input / "metadata.json").read_text())
    results = json.loads((args.input / "results.json").read_text())
    assert len(results) == metadata["repeats"] * 4
    monthly_interactions = D(33574)
    prices = {"gpt-6-luna": (D("0.1"), D("0.5")), "gpt-6.1-terra": (D(1), D(5))}
    order = [(m, e) for e in ["low", "medium"] for m in prices]
    records, summaries = [], []
    for result in results:
        assert not result.get("error")
        assert result["loaded_history_rows"] == 20
        assert result["summary_until_after"] > result["summary_until_before"]
        assert result["answer"] and result["updated_summary"]
        for index, request in enumerate(result["requests"], 1):
            assert request["http_status"] == 200 and request["status"] == "completed"
            assert request["input_tokens"] > 0
            assert request["cached_input_tokens"] == request["cache_write_tokens"] == 0
            if request["stage"] == "embedding":
                cost = D(request["input_tokens"]) * D(".02") / 1000000
            else:
                assert request["reasoning_effort"] == result["effort"]
                ip, op = prices[result["model"]]
                cost = (D(request["input_tokens"]) * ip + D(request["output_tokens"]) * op) / 1000000
            records.append(dict(case=result["id"], model=result["model"], effort=result["effort"],
                repeat=result["repeat"], request=index, stage=request["stage"], response_id=request["response_id"],
                input_tokens=request["input_tokens"], output_tokens=request["output_tokens"],
                non_reasoning_output_tokens=request["output_tokens"] - request["reasoning_tokens"],
                reasoning_tokens=request["reasoning_tokens"], cached_input_tokens=request["cached_input_tokens"],
                cache_write_tokens=request["cache_write_tokens"], seconds=request["seconds"], cost_usd=f"{cost:.9f}"))
    for model, effort in order:
        group = [r for r in results if r["model"] == model and r["effort"] == effort]
        assert len(group) == metadata["repeats"]
        rows = [r for r in records if r["model"] == model and r["effort"] == effort]
        gen = [r for r in rows if r["stage"] != "embedding"]
        emb = [r for r in rows if r["stage"] == "embedding"]
        mean = lambda field, rs: sum(D(str(r[field])) for r in rs) / len(group)
        per_turn = [sum(D(str(r["reasoning_tokens"])) for r in gen if r["case"] == c["id"]) for c in group]
        cost = mean("cost_usd", rows)
        summary = dict(model=model, effort=effort, repeats=len(group),
            mean_input_tokens=float(mean("input_tokens", gen)),
            mean_output_tokens_including_reasoning=float(mean("output_tokens", gen)),
            mean_non_reasoning_output_tokens=float(mean("non_reasoning_output_tokens", gen)),
            mean_reasoning_tokens=float(mean("reasoning_tokens", gen)),
            min_reasoning_tokens=float(min(per_turn)), max_reasoning_tokens=float(max(per_turn)),
            mean_query_embedding_tokens=float(mean("input_tokens", emb)),
            mean_model_requests=len(gen) / len(group), mean_embedding_requests=len(emb) / len(group),
            mean_cost_per_interaction_usd=f"{cost:.9f}",
            monthly_chat_and_query_embedding_usd=f"{cost * monthly_interactions:.6f}",
            monthly_with_planned_indexing_usd=f"{cost * monthly_interactions + D('.02'):.6f}",
            stages={stage: dict(mean_calls=len([r for r in rows if r["stage"] == stage]) / len(group),
                mean_input=float(mean("input_tokens", [r for r in rows if r["stage"] == stage])),
                mean_output=float(mean("output_tokens", [r for r in rows if r["stage"] == stage])),
                mean_reasoning=float(mean("reasoning_tokens", [r for r in rows if r["stage"] == stage])))
                for stage in ["agent", "summary", "embedding"]})
        summaries.append(summary)
    args.output.mkdir(parents=True, exist_ok=True)
    report_json = dict(metadata=metadata, monthly_interactions=int(monthly_interactions),
        monthly_indexing_budget_usd="0.02", summary=summaries, cases=results)
    (args.output / "repo-benchmark-results.json").write_text(json.dumps(report_json, ensure_ascii=False, indent=2) + "\n")
    with (args.output / "repo-benchmark-usage.csv").open("w") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(records[0]), lineterminator="\n")
        writer.writeheader(); writer.writerows(records)
    root = args.output.resolve()
    counts = Counter(r["stage"] for r in records)
    history_roles = results[0]["history_roles"]
    lines = ["# Qayala — Pengukuran Usage melalui Repository", "",
        "Pengujian 9 November 2026. Seluruh angka input, output, reasoning, dan embedding query di bawah berasal dari usage API pada alur repo; tidak memakai asumsi 13.000/700 token.", "",
        "## Metode", "",
        "- Menjalankan `chat.NewService` dan Eino ADK, tool `search_faq`, embedding OpenAI, pencarian PostgreSQL + pgvector, penyimpanan pesan, serta rolling summary asinkron. HTTP/auth tidak diuji karena bukan pembentuk prompt/token.",
        "- Fixture: FAQ Qayala versi 4, 11 bagian, diindeks memakai pipeline chunking/embedding repo ke database benchmark lokal yang baru. `current_database()` diverifikasi sebelum migrasi/penulisan. Database uji dihapus setelah selesai.",
        "- Prompt dan deskripsi tool disesuaikan untuk Qayala; implementasi retrieval, service chat, repository dan compaction tidak diubah. Tidak ada hasil retrieval tiruan.",
        f"- {metadata['warmup_turns']} turn pemanasan live menghasilkan history dan summary nyata. Setiap pengulangan memakai salinan baseline yang sama dari {metadata['warmup_rows']} row tersimpan, bukan melanjutkan ulangan sebelumnya.",
        f"- Top-k retrieval: {metadata['top_k']}; history_window: 20 row; max_output_tokens: {metadata['max_output_tokens']}. Effort low/medium diverifikasi pada request aktual. Cache dinonaktifkan dan store=false untuk pengukuran.",
        f"- Empat konfigurasi × {metadata['repeats']} ulangan = {len(results)} interaksi terukur; {sum(counts.values())} request: {counts['agent']} Agent, {counts['summary']} summary, {counts['embedding']} embedding query. Warmup dan indexing terpisah dari biaya per interaksi.", "",
        "Pertanyaan yang sama:", "", "> " + metadata["question"], "",
        "## Token aktual dan proyeksi biaya", "",
        "| Model | Effort | Input model/interaksi | Output non-reasoning | Reasoning | Embedding query | Chat + embedding/bulan |", "|---|---|---:|---:|---:|---:|---:|"]
    for r in summaries:
        lines.append(f"| {r['model']} | {r['effort']} | {r['mean_input_tokens']:,.1f} | {r['mean_non_reasoning_output_tokens']:.1f} | {r['mean_reasoning_tokens']:.1f} | {r['mean_query_embedding_tokens']:.1f} | ${D(r['monthly_chat_and_query_embedding_usd']):.2f} |")
    lines += ["", "Angka token adalah rata-rata per interaksi, dijumlahkan dari seluruh panggilan aktual. Output API sudah mencakup reasoning sehingga tidak dihitung dua kali. Proyeksi = 33.574 × rata-rata biaya aktual per interaksi, dengan tarif Standard OpenAI dan tanpa caching.", "",
        "Biaya bulanan tabel mencakup model Agent, update summary, dan embedding query. Indexing dokumen baru belum termasuk: bila tetap memakai alokasi perencanaan 1 juta token/bulan, tambahkan $0,02. Biaya infrastruktur dan pajak belum termasuk.", "",
        "## Rincian per tahap", "", "| Model | Effort | Tahap | Panggilan/interaksi | Input/interaksi | Output termasuk reasoning | Reasoning |", "|---|---|---|---:|---:|---:|---:|"]
    for r in summaries:
        for name, v in r["stages"].items():
            lines.append(f"| {r['model']} | {r['effort']} | {name} | {v['mean_calls']:.1f} | {v['mean_input']:.1f} | {v['mean_output']:.1f} | {v['mean_reasoning']:.1f} |")
    lines += ["", "## Perbedaan dengan rancangan dan batas pengukuran", "",
        f"- Window repo berisi 20 **row mentah**, termasuk tool call/hasil: baseline pengukuran ini berisi {history_roles.get('user',0)} user, {history_roles.get('assistant',0)} assistant, dan {history_roles.get('tool',0)} tool. Assistant mencakup pesan pemanggilan tool, bukan hanya jawaban akhir. Ini belum sama dengan rancangan 20 pesan pengguna/jawaban akhir ≈ 10 pasang.",
        "- Hasil retrieval ikut disimpan dan dikirim pada history. Summary memproses seluruh row yang keluar, termasuk hasil tool, bukan selalu tepat satu pasang tanya-jawab.",
        "- Jumlah tool call dan panjang hasil retrieval mengikuti keputusan Agent. Tidak dipaksa satu pencarian; variasinya tercatat pada hasil per interaksi.",
        "- Satu pertanyaan diulang lima kali untuk setiap konfigurasi. Ini angka nyata untuk workload uji ini, bukan rata-rata representatif seluruh FAQ, bahasa, panjang sesi, atau trafik produksi.",
        "- Respons completed dan summary tersimpan tidak otomatis menjamin kualitas jawaban/summary. Pengukuran ini berfokus pada konsumsi token, bukan evaluasi kualitas menyeluruh.",
        "- Bedrock tidak diuji. Angka ini tidak boleh disebut hasil pengukuran Bedrock.",
        "- Pada sampel ini Terra mencatat 0 token reasoning di low maupun medium meskipun parameter effort pada request sudah diverifikasi. Ini bukan jaminan penggunaan reasoning selalu nol pada pertanyaan lain.",
        "- Uji eksplorasi awal mengalami kesalahan pemilihan database. Hanya 5 percakapan benchmark, agent benchmark, dan 11 dokumen fixture yang dibuat runner telah dihapus dengan pemeriksaan ID/hash. Keberadaan sisa data benchmark di database aplikasi diverifikasi nol. Hasil eksplorasi tersebut dikeluarkan dari laporan ini; suite ini dijalankan ulang setelah verifikasi isolasi sebelum migrasi.", "",
        "## Reproduksi", "", "```sh", "go run ./scripts/spike/qayala-usage --repeats 5 --out /tmp/qayala-repo-usage", "python3 scripts/spike/qayala-usage/report.py --input /tmp/qayala-repo-usage --output docs/qayala", "```", "",
        "Runner membaca konfigurasi dan kredensial lokal dari .env tanpa menuliskannya ke hasil. Memerlukan PostgreSQL lokal dengan izin membuat database; hanya database baru dengan prefix qayala_bench_ yang dibuat/dihapus. Pemanggilan OpenAI dikenai biaya.", "",
        f"[Usage setiap request]({root}/repo-benchmark-usage.csv) · [Hasil JSON]({root}/repo-benchmark-results.json)", "",
        "Tarif input/output per juta token: [Luna](https://developers.openai.com/api/docs/models/gpt-6-luna) $0,10/$0,50; [Terra](https://developers.openai.com/api/docs/models/gpt-6.1-terra) $1/$5; [embedding](https://developers.openai.com/api/docs/models/text-embedding-3-small) $0,02.", ""]
    (args.output / "repo-benchmark.md").write_text("\n".join(lines))
    print(json.dumps(dict(interactions=len(results), request_counts=dict(counts), summary=summaries), indent=2))


if __name__ == "__main__":
    main()
