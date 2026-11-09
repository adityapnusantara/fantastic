"""Report an expanded Terra reasoning run without modifying prior benchmarks."""
import argparse
import csv
import json
import math
from collections import Counter
from decimal import Decimal as D
from pathlib import Path


def request_cost(request):
    if request["stage"] == "embedding":
        return D(request["input_tokens"]) * D(".02") / 1_000_000
    rate = D(".1") if request["model"] == "gpt-6-luna" else D(1)
    return (D(request["input_tokens"]) * rate
            + D(request["output_tokens"]) * rate * 5) / 1_000_000


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, default=Path("docs/qayala"))
    parser.add_argument("--excluded-run", type=Path, action="append", default=[])
    args = parser.parse_args()
    meta = json.loads((args.input / "metadata.json").read_text())
    cases = json.loads((args.input / "results.json").read_text())
    warmup = json.loads((args.input / "warmup.json").read_text())
    assert meta["database_isolation_verified"]
    assert meta["history_qa_turns_requested"] == 10
    assert len(cases) == len(meta["questions"]) * meta["repeats"] * 2
    rows = []
    for case in cases:
        assert not case.get("error") and case["answer"] and case["updated_summary"]
        assert case["history_roles"]["user"] == case["history_final_answers"] == 10
        assert case["summary_until_after"] > case["summary_until_before"]
        for number, request in enumerate(case["requests"], 1):
            assert request["http_status"] == 200 and request["status"] == "completed"
            assert request["input_tokens"] > 0
            assert request["cached_input_tokens"] == request["cache_write_tokens"] == 0
            if request["stage"] != "embedding":
                assert request["reasoning_effort"] == case["effort"]
                assert request["model"] == case["model"]
            rows.append(dict(case=case["id"], question=meta["questions"].index(case["question"]) + 1,
                model=case["model"], effort=case["effort"], repeat=case["repeat"],
                request=number, stage=request["stage"], response_id=request["response_id"],
                input_tokens=request["input_tokens"], output_tokens=request["output_tokens"],
                reasoning_tokens=request["reasoning_tokens"],
                non_reasoning_output_tokens=request["output_tokens"] - request["reasoning_tokens"],
                function_calls=request.get("function_calls", 0),
                function_argument_bytes=request.get("function_argument_bytes", 0),
                argument_delta_bytes=request.get("argument_delta_bytes", 0),
                cost_usd=str(request_cost(request))))

    def statistics(group):
        count = len(group)
        requests = [request for case in group for request in case["requests"]]
        models = [r for r in requests if r["stage"] != "embedding"]
        per_turn = sorted(sum(r["reasoning_tokens"] for r in c["requests"]) for c in group)
        average = lambda rs, field: sum(D(r[field]) for r in rs) / count
        cost = sum(request_cost(r) for r in requests) / count
        return dict(interactions=count, mean_reasoning=str(sum(D(v) for v in per_turn) / count),
            min_reasoning=per_turn[0], max_reasoning=per_turn[-1],
            p95_nearest_rank=per_turn[math.ceil(.95 * count) - 1],
            nonzero_interactions=sum(v > 0 for v in per_turn),
            mean_input=str(average(models, "input_tokens")),
            mean_output_including_reasoning=str(average(models, "output_tokens")),
            mean_query_embedding=str(average([r for r in requests if r["stage"] == "embedding"], "input_tokens")),
            mean_cost_usd=str(cost), monthly_with_indexing_usd=str(cost * 33574 + D(".02")),
            mean_calls={stage: str(D(sum(r["stage"] == stage for r in requests)) / count)
                        for stage in ["agent", "summary", "embedding"]},
            stages={stage: dict(mean_input=str(average([r for r in requests if r["stage"] == stage], "input_tokens")),
                               mean_output=str(average([r for r in requests if r["stage"] == stage], "output_tokens")),
                               mean_reasoning=str(average([r for r in requests if r["stage"] == stage], "reasoning_tokens")))
                    for stage in ["agent", "summary", "embedding"]})

    summaries = {effort: statistics([c for c in cases if c["effort"] == effort])
                 for effort in ["low", "medium"]}
    by_question = {f"q{index + 1}-{effort}": statistics([c for c in cases if c["effort"] == effort and c["question"] == question])
                   for index, question in enumerate(meta["questions"]) for effort in ["low", "medium"]}
    measured_cost = sum(request_cost(r) for c in cases for r in c["requests"])
    setup_cost = (sum(request_cost(r) for c in warmup for r in c["requests"])
                  + sum(request_cost(r) for r in meta["indexing_requests"]))
    excluded = []
    for path in args.excluded_run:
        failed = json.loads((path / "results.json").read_text())
        previous_warmup = json.loads((path / "warmup.json").read_text())
        observed_cost = sum(request_cost(r) for c in failed + previous_warmup for r in c["requests"])
        excluded.append(dict(cases=failed, observed_cost_excluding_indexing_usd=str(observed_cost),
                             warmup_turns=len(previous_warmup)))
    args.output.mkdir(parents=True, exist_ok=True)
    prefix = args.output / "terra-reasoning-retest"
    payload = dict(metadata=meta, summaries=summaries, by_question=by_question,
                   measured_suite_cost_usd=str(measured_cost), setup_cost_usd=str(setup_cost),
                   total_run_cost_usd=str(measured_cost + setup_cost), cases=cases, warmup=warmup,
                   excluded_runs=excluded)
    prefix.with_suffix(".json").write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n")
    with prefix.with_suffix(".csv").open("w") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(rows[0]), lineterminator="\n")
        writer.writeheader()
        writer.writerows(rows)
    lines = ["# Qayala — Tes ulang reasoning Terra", "",
        "Pengujian live OpenAI melalui Go + Eino, search_faq, embedding dan PostgreSQL + pgvector. "
        "Tiga pertanyaan gabungan × tiga pengulangan × dua effort = 18 interaksi terukur. "
        "Hasil ini terpisah dari benchmark sebelumnya.", "",
        "## Metode", "",
        f"- Model: gpt-6.1-terra, effort low dan medium diverifikasi pada request aktual. "
        f"Cache dinonaktifkan; max_output_tokens={meta['max_output_tokens']}.",
        "- Setiap interaksi memakai salinan baseline yang sama: 10 user + 10 jawaban akhir assistant, "
        "beserta pesan tool call/hasil yang utuh, dan satu rolling summary. "
        f"Total window aktual {meta['history_window_rows']} row. Batas history dan jumlah jawaban diverifikasi.",
        "- 11 turn pemanasan live menggunakan Luna low. Dokumen FAQ yang sama, versi 4, diindeks di database uji baru. "
        "current_database() diverifikasi sebelum migrasi dan penulisan.",
        "- Instruksi Agent tetap sama dengan benchmark sebelumnya (teks inti 105 token, "
        "sekitar 210 token termasuk definisi tool dan format). Asumsi 520 token tidak diterapkan dalam tes ini.",
        "- Jumlah panggilan tool mengikuti Agent. Tidak ada instruksi meminta chain-of-thought "
        "atau menambah reasoning secara artifisial. HTTP/auth tidak diuji.", "",
        "## Reasoning per interaksi", "",
        "Reasoning dijumlahkan dari semua panggilan Agent dan update summary dalam satu interaksi.", "",
        "| Effort | Interaksi | Rata-rata | Minimum–maksimum | P95 sampel | Interaksi > 0 |",
        "|---|---:|---:|---:|---:|---:|"]
    for effort, s in summaries.items():
        lines.append(f"| {effort} | {s['interactions']} | {D(s['mean_reasoning']):.2f} | "
                     f"{s['min_reasoning']}–{s['max_reasoning']} | {s['p95_nearest_rank']} | {s['nonzero_interactions']} |")
    lines += ["", "P95 memakai nearest-rank; dengan 9 interaksi per effort, nilainya sama dengan maksimum sampel. "
              "Jumlah sampel ini belum cukup untuk menetapkan P95 produksi.", "", "## Per pertanyaan", "",
              "| Pertanyaan | Effort | Rata-rata reasoning | Minimum–maksimum |", "|---|---|---:|---:|"]
    for label, s in by_question.items():
        q, effort = label.split("-", 1)
        lines.append(f"| {q} | {effort} | {D(s['mean_reasoning']):.2f} | {s['min_reasoning']}–{s['max_reasoning']} |")
    lines += ["", "## Usage dan biaya aktual", "",
              "| Effort | Input/interaksi | Output termasuk reasoning | Embedding query | AI/interaksi | AI/bulan + indexing |",
              "|---|---:|---:|---:|---:|---:|"]
    for effort, s in summaries.items():
        lines.append(f"| {effort} | {D(s['mean_input']):.2f} | {D(s['mean_output_including_reasoning']):.2f} | "
                     f"{D(s['mean_query_embedding']):.2f} | ${D(s['mean_cost_usd']):.6f} | ${D(s['monthly_with_indexing_usd']):.2f} |")
    lines += ["", "Proyeksi bulanan memakai 33.574 interaksi dan alokasi indexing 1 juta token ($0,02). "
              "Output API sudah mencakup reasoning; tidak dihitung dua kali. "
              "Pemanasan dan indexing sekali jalan tidak dimasukkan ke rata-rata biaya chat.", "",
              f"Biaya 18 interaksi terukur: ${measured_cost:.6f}; pemanasan + indexing: ${setup_cost:.6f}; total run: ${measured_cost + setup_cost:.6f}.", "",
              "## Tahap model", "", "| Effort | Tahap | Panggilan/interaksi | Input | Output termasuk reasoning | Reasoning |",
              "|---|---|---:|---:|---:|---:|"]
    for effort, s in summaries.items():
        for stage, v in s["stages"].items():
            lines.append(f"| {effort} | {stage} | {D(s['mean_calls'][stage]):.2f} | {D(v['mean_input']):.2f} | "
                         f"{D(v['mean_output']):.2f} | {D(v['mean_reasoning']):.2f} |")
    lines += ["", "## Pertanyaan yang diuji", ""]
    lines += [f"{i + 1}. {q}" for i, q in enumerate(meta["questions"])]
    lines += ["", "## Batas interpretasi", "",
              f"- parallel_tool_calls_disabled={meta['parallel_tool_calls_disabled']}. "
              "Tool search_faq dapat dipanggil beberapa kali secara berurutan. "
              "Konfigurasi ini dicatat karena berbeda dari default benchmark sebelumnya.",
              "- Pertanyaan, panjang history, dan jumlah jawaban berubah dari tes sebelumnya. Selisih angka tidak bisa dianggap "
              "efek perubahan effort saja. Low dan medium pada run ini menggunakan workload dan baseline yang sama.",
              "- Ini sampel FAQ Indonesia, bukan distribusi trafik produksi atau pengukuran Bedrock.",
              "- Receipt API completed bukan evaluasi kualitas jawaban menyeluruh. Data jawaban dan summary disimpan untuk pemeriksaan.",
              "- Angka nol tetap dilaporkan apabila usage API menyatakan nol. Pengukuran tidak diubah menjadi asumsi positif.",
              "- Biaya infrastruktur dan pajak belum termasuk. Database uji dihapus oleh runner saat keluar; status cleanup diperiksa di output run.", "",
              "## Reproduksi", "", "```sh",
              "go run ./scripts/spike/qayala-usage --fixture scripts/spike/qayala-usage/fixture-terra-complex.json --models gpt-6.1-terra --history-turns 10 --sequential-tools --repeats 3 --out /tmp/qayala-terra-complex",
              "python3 scripts/spike/qayala-usage/report_reasoning.py --input /tmp/qayala-terra-complex --output docs/qayala", "```", "",
              "Tarif per juta token: [Terra](https://developers.openai.com/api/docs/models/gpt-6.1-terra) input $1/output $5; "
              "[Luna](https://developers.openai.com/api/docs/models/gpt-6-luna) input $0,10/output $0,50 untuk pemanasan; "
              "[embedding](https://developers.openai.com/api/docs/models/text-embedding-3-small) $0,02.", ""]
    if excluded:
        lines += ["## Percobaan yang dikeluarkan", "",
                  "Percobaan awal berhenti pada kasus pertama karena Eino gagal membaca argumen function tool (EOF). "
                  "Seluruh hasil run tersebut dikeluarkan dari statistik reasoning. Database uji dihapus sebelum run berikutnya. "
                  "Request yang sudah terkirim tetap memiliki biaya.", ""]
        for i, item in enumerate(excluded, 1):
            lines.append(f"- Run {i}: biaya request tercatat, termasuk {item['warmup_turns']} turn pemanasan, "
                         f"${D(item['observed_cost_excluding_indexing_usd']):.6f}; "
                         "indexing awal tidak terekam di metadata run yang berhenti dan belum termasuk angka ini.")
        lines.append("")
    prefix.with_suffix(".md").write_text("\n".join(lines))
    print(json.dumps(dict(summaries=summaries, by_question=by_question,
                         request_counts=dict(Counter(r["stage"] for r in rows)),
                         total_run_cost_usd=str(measured_cost + setup_cost)), indent=2))


if __name__ == "__main__":
    main()
