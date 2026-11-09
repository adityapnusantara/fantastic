"""Compare both models on one identical memory baseline; project 520-token instructions."""
import argparse
import csv
import json
from decimal import Decimal as D
from pathlib import Path

from report_reasoning import request_cost


def display(value, digits=1):
    return format(D(value), f",.{digits}f").translate(str.maketrans({",": ".", ".": ","}))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, default=Path("docs/qayala"))
    args = parser.parse_args()
    meta = json.loads((args.input / "metadata.json").read_text())
    cases = json.loads((args.input / "results.json").read_text())
    assert meta["database_isolation_verified"] and meta["parallel_tool_calls_disabled"]
    assert meta["history_qa_turns_requested"] == 10
    assert len(cases) == 4 * len(meta["questions"]) * meta["repeats"]
    fingerprints = {c["history_fingerprint"] for c in cases}
    assert len(fingerprints) == 1 and all(fingerprints)
    rows = []
    for c in cases:
        assert not c.get("error") and c["answer"] and c["updated_summary"]
        assert c["history_roles"]["user"] == c["history_final_answers"] == 10
        assert c["summary_until_after"] > c["summary_until_before"]
        for index, r in enumerate(c["requests"], 1):
            assert r["http_status"] == 200 and r["status"] == "completed"
            assert r["input_tokens"] > 0
            assert r["cached_input_tokens"] == r["cache_write_tokens"] == 0
            if r["stage"] != "embedding":
                assert r["reasoning_effort"] == c["effort"]
                assert r["model"] == c["model"]
            rows.append(dict(case=c["id"], model=c["model"], effort=c["effort"],
                question=meta["questions"].index(c["question"]) + 1, repeat=c["repeat"],
                request=index, stage=r["stage"], response_id=r["response_id"],
                input_tokens=r["input_tokens"], output_tokens=r["output_tokens"],
                reasoning_tokens=r["reasoning_tokens"], actual_cost_usd=str(request_cost(r))))
    n, million = D(33574), D(1_000_000)
    observed_instruction, planned_instruction = D(210), D(520)
    comparison = []
    for model in ["gpt-6-luna", "gpt-6.1-terra"]:
        for effort in ["low", "medium"]:
            group = [c for c in cases if c["model"] == model and c["effort"] == effort]
            assert len(group) == len(meta["questions"]) * meta["repeats"]
            requests = [r for c in group for r in c["requests"]]
            mean = lambda stage, field: sum(D(r[field]) for r in requests if r["stage"] == stage) / len(group)
            counts = {stage: D(sum(r["stage"] == stage for r in requests)) / len(group)
                      for stage in ["agent", "summary", "embedding"]}
            rate = D(".1") if model == "gpt-6-luna" else D(1)
            out_rate = rate * 5
            ar, sr = mean("agent", "reasoning_tokens"), mean("summary", "reasoning_tokens")
            raw_agent_input = mean("agent", "input_tokens")
            addition = counts["agent"] * (planned_instruction - observed_instruction)
            tokens = dict(instructions=counts["agent"] * planned_instruction,
                agent_other_input=raw_agent_input - counts["agent"] * observed_instruction,
                agent_total_input=raw_agent_input + addition,
                agent_nonreasoning_output=mean("agent", "output_tokens") - ar,
                summary_input=mean("summary", "input_tokens"),
                summary_nonreasoning_output=mean("summary", "output_tokens") - sr,
                agent_reasoning=ar, summary_reasoning=sr,
                query_embedding=mean("embedding", "input_tokens"))
            costs = dict(agent_input=tokens["agent_total_input"] * n * rate / million,
                agent_nonreasoning_output=tokens["agent_nonreasoning_output"] * n * out_rate / million,
                summary_input=tokens["summary_input"] * n * rate / million,
                summary_nonreasoning_output=tokens["summary_nonreasoning_output"] * n * out_rate / million,
                reasoning=(ar + sr) * n * out_rate / million,
                query_embedding=tokens["query_embedding"] * n * D(".02") / million,
                planned_document_indexing=D(".02"))
            actual_mean_cost = sum(request_cost(r) for r in requests) / len(group)
            expected_projection = actual_mean_cost * n + addition * n * rate / million + D(".02")
            assert abs(sum(costs.values()) - expected_projection) < D("1e-15")
            per_case_reasoning = [sum(r["reasoning_tokens"] for r in c["requests"]) for c in group]
            comparison.append(dict(model=model, effort=effort, interactions=len(group),
                calls={k: str(v) for k, v in counts.items()},
                measured_agent_input=str(raw_agent_input), planned_input_addition=str(addition),
                tokens={k: str(v) for k, v in tokens.items()},
                monthly_cost_usd={k: str(v) for k, v in costs.items()},
                projected_monthly_total_usd=str(sum(costs.values())),
                min_reasoning=min(per_case_reasoning), max_reasoning=max(per_case_reasoning)))
    warmup = json.loads((args.input / "warmup.json").read_text())
    actual_run_cost = (sum(request_cost(r) for c in cases + warmup for r in c["requests"])
                       + sum(request_cost(r) for r in meta["indexing_requests"]))
    output = dict(metadata=meta, common_history_fingerprint=next(iter(fingerprints)),
                  assumptions=dict(monthly_interactions=33574, measured_instruction_tokens_approx=210,
                                   planned_instruction_tokens_per_agent_call=520,
                                   monthly_document_indexing_tokens=1_000_000),
                  comparison=comparison, actual_run_cost_usd=str(actual_run_cost), cases=cases)
    args.output.mkdir(parents=True, exist_ok=True)
    stem = args.output / "equal-history-comparison"
    stem.with_suffix(".json").write_text(json.dumps(output, ensure_ascii=False, indent=2) + "\n")
    with stem.with_suffix(".csv").open("w") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(rows[0]), lineterminator="\n")
        writer.writeheader()
        writer.writerows(rows)
    headers = "| Komponen | Luna low | Luna medium | Terra low | Terra medium |"
    separator = "|---|---:|---:|---:|---:|"
    lines = ["# Qayala — Perbandingan dengan history setara", "",
             "Semua konfigurasi diuji dalam satu suite live melalui Go + Eino, embedding OpenAI dan PostgreSQL + pgvector. "
             "Setiap interaksi memakai 10 pesan pengguna + 10 jawaban akhir, pesan tool yang utuh, serta rolling summary yang sama. "
             "Fingerprint history/summary identik untuk seluruh kasus.", "",
             "Tiga pertanyaan gabungan × tiga pengulangan × empat konfigurasi = 36 interaksi terukur. "
             "Fixture FAQ versi 4, top-k 5, max_output_tokens 2048, tool berurutan, cache dinonaktifkan. "
             "Effort low/medium diverifikasi pada request. Database uji diverifikasi sebelum migrasi/penulisan dan dihapus setelah run.", "",
             "## Asumsi proyeksi", "",
             "- 33.574 interaksi/bulan; tarif Standard tanpa caching.",
             "- Instruksi dalam tes tetap prompt sebelumnya, sekitar 210 token termasuk definisi tool dan format. "
             "Proyeksi menggunakan 520 token per panggilan Agent, sehingga tambahan input = (520 − 210) × jumlah panggilan Agent. "
             "Output dan jumlah panggilan diasumsikan tetap. Angka 520 bukan hasil pengujian prompt baru.",
             "- Embedding query memakai jumlah token terukur. Indexing dokumen dialokasikan 1 juta token/bulan ($0,02).", "",
             "## Rata-rata token per interaksi", "", headers, separator]
    labels = {"instructions": "Instruksi Agent, termasuk definisi tool",
              "agent_other_input": "Input Agent lainnya: history, summary, pertanyaan, retrieval dan format",
              "agent_total_input": "**Total input Agent**",
              "agent_nonreasoning_output": "Output Agent tanpa reasoning: tool call + jawaban",
              "summary_input": "Input update summary",
              "summary_nonreasoning_output": "Output summary tanpa reasoning",
              "agent_reasoning": "Reasoning Agent", "summary_reasoning": "Reasoning summary",
              "query_embedding": "Embedding query"}
    for field, label in labels.items():
        lines.append("| " + label + " | " + " | ".join(display(c["tokens"][field]) for c in comparison) + " |")
    lines += ["", "Input Agent lainnya merupakan selisih total input dan alokasi instruksi. "
              "History dan retrieval belum dihitung terpisah; teks retrieval dikenakan biaya saat masuk ke input model. "
              "Reasoning dipisahkan dari output biasa agar tidak dihitung dua kali.", "",
              "## Breakdown biaya bulanan — USD", "", headers, separator]
    cost_labels = dict(agent_input="Input Agent", agent_nonreasoning_output="Output Agent tanpa reasoning",
                       summary_input="Input update summary", summary_nonreasoning_output="Output summary tanpa reasoning",
                       reasoning="Reasoning Agent + summary", query_embedding="Embedding query",
                       planned_document_indexing="Indexing dokumen: alokasi 1 juta token/bulan")
    for field, label in cost_labels.items():
        lines.append("| " + label + " | " + " | ".join("$" + display(c["monthly_cost_usd"][field], 3) for c in comparison) + " |")
    lines.append("| **Total AI/bulan** | " + " | ".join("**$" + display(c["projected_monthly_total_usd"], 2) + "**" for c in comparison) + " |")
    lines += ["", "Total dihitung sebelum pembulatan. Biaya infrastruktur dan pajak belum termasuk.", "",
              f"Biaya aktual seluruh run, termasuk pemanasan dan indexing awal: ${actual_run_cost:.6f}. "
              "Biaya run ini terpisah dari proyeksi bulanan.", "",
              "## Panggilan dan reasoning", "",
              "| Konfigurasi | Agent/interaksi | Summary/interaksi | Embedding/interaksi | Reasoning minimum–maksimum |",
              "|---|---:|---:|---:|---:|"]
    for c in comparison:
        lines.append(f"| {c['model']} {c['effort']} | {display(c['calls']['agent'])} | "
                     f"{display(c['calls']['summary'])} | {display(c['calls']['embedding'])} | "
                     f"{c['min_reasoning']}–{c['max_reasoning']} |")
    lines += ["", "## Pertanyaan", ""]
    lines += [f"{i + 1}. {q}" for i, q in enumerate(meta["questions"])]
    lines += ["", "## Batas pengukuran", "",
              "- Semua hasil input/output/reasoning selain penyesuaian instruksi berasal dari API. "
              "Reasoning nol tetap dilaporkan nol. Sampel kecil belum mewakili seluruh trafik FAQ.",
              "- Jawaban dan panjang query dapat berbeda antarmodel meskipun baseline dan pertanyaan identik. "
              "Window menyertakan tool call/hasil, sehingga token history lebih besar daripada teks percakapan saja.",
              "- Pengujian memakai use case langsung; HTTP/auth tidak diuji. Bedrock tidak dipanggil. "
              "Angka bukan hasil pengukuran Bedrock.",
              "- Tool paralel dinonaktifkan karena percobaan sebelumnya mengalami error parsing tool di Eino. "
              "Konfigurasi berurutan sama untuk keempat konfigurasi.", "",
              "## Reproduksi", "", "```sh",
              "go run ./scripts/spike/qayala-usage --fixture scripts/spike/qayala-usage/fixture-terra-complex.json --models gpt-6-luna,gpt-6.1-terra --history-turns 10 --sequential-tools --repeats 3 --out /tmp/qayala-equal-history",
              "python3 scripts/spike/qayala-usage/report_comparison.py --input /tmp/qayala-equal-history --output docs/qayala", "```", "",
              "Tarif per juta token: [Luna](https://developers.openai.com/api/docs/models/gpt-6-luna) input $0,10/output $0,50; "
              "[Terra](https://developers.openai.com/api/docs/models/gpt-6.1-terra) input $1/output $5; "
              "[embedding](https://developers.openai.com/api/docs/models/text-embedding-3-small) $0,02.", ""]
    stem.with_suffix(".md").write_text("\n".join(lines))
    print(json.dumps(dict(comparison=comparison, actual_run_cost_usd=str(actual_run_cost)), indent=2))


if __name__ == "__main__":
    main()
