"""Compute a session-weighted average; writes JSON/CSV only, never Markdown."""
import argparse
import csv
import json
from decimal import Decimal as D
from pathlib import Path

from report_reasoning import request_cost


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, default=Path("docs/qayala"))
    args = parser.parse_args()
    meta = json.loads((args.input / "session-metadata.json").read_text())
    cases = json.loads((args.input / "session-results.json").read_text())
    upper = json.loads((args.output / "equal-history-comparison.json").read_text())
    lengths = meta["session_lengths"]
    weights = [D(str(w)) for w in meta["session_weights"]]
    mean_length = sum(D(length) * weight for length, weight in zip(lengths, weights))
    assert sum(weights) == 1 and mean_length == 6
    assert meta["database_isolation_verified"] and meta["empty_start"]
    assert len(cases) == len(meta["models"]) * 2 * meta["repeats"] * 15
    rows, results = [], []
    cases.sort(key=lambda c: (c["model"], c["effort"], c["repeat"], c["session_turn"]))
    for c in cases:
        t = c["session_turn"]
        expected_history = min(t - 1, 10)
        assert not c.get("error") and c["answer"]
        assert c["history_roles"].get("user", 0) == c["history_final_answers"] == expected_history
        assert c["question"] == meta["session_question_sequences"][c["repeat"] - 1][t - 1]
        summary_calls = sum(r["stage"] == "summary" for r in c["requests"])
        if t <= 10:
            assert summary_calls == 0 and c["summary_until_before"] == c["summary_until_after"] == 0
        else:
            assert summary_calls == 1 and c["summary_until_after"] > c["summary_until_before"]
        if t == 11:
            assert c["summary_until_before"] == 0
        if t >= 12:
            assert c["summary_until_before"] > 0
        prefix_weight = sum(weight for length, weight in zip(lengths, weights) if t <= length)
        for index, r in enumerate(c["requests"], 1):
            assert r["http_status"] == 200 and r["status"] == "completed"
            assert r["input_tokens"] > 0
            assert r["cached_input_tokens"] == r["cache_write_tokens"] == 0
            if r["stage"] != "embedding":
                assert r["reasoning_effort"] == c["effort"] and r["model"] == c["model"]
            rows.append(dict(case=c["id"], model=c["model"], effort=c["effort"], repeat=c["repeat"],
                turn=t, question=c["question"], prefix_weight=str(prefix_weight),
                request=index, stage=r["stage"], response_id=r["response_id"],
                input_tokens=r["input_tokens"], output_tokens=r["output_tokens"],
                reasoning_tokens=r["reasoning_tokens"], actual_cost_usd=str(request_cost(r))))
    for model in ["gpt-6-luna", "gpt-6.1-terra"]:
        for effort in ["low", "medium"]:
            group = [c for c in cases if c["model"] == model and c["effort"] == effort]
            assert len(group) == meta["repeats"] * 15
            assert len({c["conversation_id"] for c in group}) == meta["repeats"]
            for repeat in range(1, meta["repeats"] + 1):
                assert [c["session_turn"] for c in group if c["repeat"] == repeat] == list(range(1, 16))
            weighted = [(r, sum(weight for length, weight in zip(lengths, weights) if c["session_turn"] <= length))
                        for c in group for r in c["requests"]]
            divisor = D(meta["repeats"]) * mean_length
            mean = lambda stage, field: sum(D(r[field]) * w for r, w in weighted if r["stage"] == stage) / divisor
            calls = {stage: sum(w for r, w in weighted if r["stage"] == stage) / divisor
                     for stage in ["agent", "summary", "embedding"]}
            rate = D(".1") if model == "gpt-6-luna" else D(1)
            op = rate * 5
            ar, sr = mean("agent", "reasoning_tokens"), mean("summary", "reasoning_tokens")
            tokens = dict(instructions=calls["agent"] * 520,
                agent_other_input=mean("agent", "input_tokens") - calls["agent"] * 210,
                agent_input=mean("agent", "input_tokens") + calls["agent"] * 310,
                agent_output_nonreasoning=mean("agent", "output_tokens") - ar,
                summary_input=mean("summary", "input_tokens"),
                summary_output_nonreasoning=mean("summary", "output_tokens") - sr,
                reasoning_agent=ar, reasoning_summary=sr,
                query_embedding=mean("embedding", "input_tokens"))
            costs = dict(agent_input=tokens["agent_input"] * rate,
                agent_nonreasoning_output=tokens["agent_output_nonreasoning"] * op,
                summary_input=tokens["summary_input"] * rate,
                summary_nonreasoning_output=tokens["summary_output_nonreasoning"] * op,
                reasoning=(ar + sr) * op, query_embedding=tokens["query_embedding"] * D(".02"))
            monthly = {k: v * 33574 / 1_000_000 for k, v in costs.items()}
            monthly["planned_document_indexing"] = D(".02")
            average_total = sum(monthly.values())
            prefix_costs = {}
            for length in lengths:
                prefix = [r for c in group if c["session_turn"] <= length for r in c["requests"]]
                projected_cost = sum(request_cost(r) + (D(310) * rate / 1_000_000 if r["stage"] == "agent" else 0) for r in prefix)
                prefix_costs[length] = projected_cost / meta["repeats"]
            independent_average = sum(prefix_costs[length] * weight for length, weight in zip(lengths, weights)) / mean_length
            assert abs(average_total - (independent_average * 33574 + D(".02"))) < D("1e-15")
            baseline = next(c for c in upper["comparison"] if c["model"] == model and c["effort"] == effort)
            upper_total = D(baseline["projected_monthly_total_usd"])
            results.append(dict(model=model, effort=effort,
                calls_per_interaction={k: str(v) for k, v in calls.items()},
                tokens_per_interaction={k: str(v) for k, v in tokens.items()},
                monthly_cost_usd={k: str(v) for k, v in monthly.items()},
                average_monthly_total_usd=str(average_total), upper_monthly_total_usd=str(upper_total),
                reduction_percent=str((upper_total - average_total) / upper_total * 100),
                average_cost_per_interaction_usd=str(independent_average),
                mean_session_cost_usd={str(k): str(v) for k, v in prefix_costs.items()}))
    run_cost = (sum(request_cost(r) for c in cases for r in c["requests"])
                + sum(request_cost(r) for r in meta["indexing_requests"]))
    output = dict(metadata=meta, assumption="25% of sessions for each length; synthetic workload, not production average",
        derivation="session-weighted total prefix cost / session-weighted total questions; three sequences reused as prefixes",
        planned_instruction_tokens_per_agent_call=520, monthly_interactions=33574,
        mean_questions_per_session=str(mean_length), expected_monthly_sessions=str(D(33574) / mean_length),
        expected_summary_updates_per_interaction=str(D(5) / 24),
        actual_run_cost_usd=str(run_cost), results=results, cases=cases)
    args.output.mkdir(parents=True, exist_ok=True)
    stem = args.output / "session-average"
    stem.with_suffix(".json").write_text(json.dumps(output, ensure_ascii=False, indent=2) + "\n")
    with stem.with_suffix(".csv").open("w") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(rows[0]), lineterminator="\n")
        writer.writeheader()
        writer.writerows(rows)
    print(json.dumps(dict(results=results, actual_run_cost_usd=str(run_cost)), indent=2))


if __name__ == "__main__":
    main()
