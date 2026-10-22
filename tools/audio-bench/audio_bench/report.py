"""Score runner results against the manifest and render the evaluation tables.

Usage: python3 -m audio_bench.report --manifest M --results R.jsonl [--split dev] --out report.md
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

from . import manifest as manifest_mod
from .manifest import Sample
from .metrics import ErrorCounts, char_errors, is_hallucination, percentile, preserved, term_hits, word_errors
from .timeline import Job, finish_times

KINDS = ("streaming_final", "offline")


def _rate(errors: ErrorCounts) -> dict:
    return {"errors": errors.errors, "reference": errors.reference, "rate": errors.rate}


def _ratio(num: int, den: int, num_key: str, den_key: str) -> dict:
    return {num_key: num, den_key: den, "rate": (num / den) if den else None}


def _quality(samples: list[Sample], results: dict, kind: str) -> dict:
    wer = cer = mixed = ErrorCounts(0, 0)
    per_speaker: dict[str, ErrorCounts] = {}
    hits = expected = crit_ok = crit_total = 0
    silence_total, hallucinated = 0, []
    for s in samples:
        hyp = results[s.id][kind]
        if "silence" in s.categories:
            silence_total += 1
            if is_hallucination(hyp):
                hallucinated.append(s.id)
            continue
        w = word_errors(s.reference, hyp)
        wer, cer = wer + w, cer + char_errors(s.reference, hyp)
        per_speaker[s.speaker] = per_speaker.get(s.speaker, ErrorCounts(0, 0)) + w
        if "mixed" in s.categories:
            mixed = mixed + w
        for term in s.terms:
            h, e = term_hits(term, s.reference, hyp)
            hits, expected = hits + h, expected + e
        if s.critical:
            crit_total += 1
            crit_ok += all(preserved(c.term, hyp) for c in s.critical)
    return {
        "wer": _rate(wer),
        "wer_mixed": _rate(mixed),
        "cer": _rate(cer),
        "terms": _ratio(hits, expected, "hits", "expected"),
        "critical": _ratio(crit_ok, crit_total, "preserved", "samples"),
        "silence": {"samples": silence_total, "hallucinated": hallucinated},
        "per_speaker": {k: _rate(v) for k, v in sorted(per_speaker.items())},
    }


def _jobs(result: dict, track: int, mode: str, endpoint: float):
    """Partial jobs and final jobs of one sample; final work waits for endpoint silence."""
    partials, finals = [], []
    for u in result["utterances"]:
        for step in u["steps"]:
            partials.append(Job(u["start"] + step["audio_end"], step["compute"], track))
        compute = u["finish"]["compute"] if mode == "streaming" else u["offline"]["compute"]
        finals.append((Job(u["end"] + endpoint, compute, track), u["end"]))
    return partials, finals


def _replay(results: list[dict], mode: str, chunk: float, endpoint: float):
    partials, finals = [], []
    for track, r in enumerate(results):
        p, f = _jobs(r, track, mode, endpoint)
        partials += p
        finals += f
    jobs = partials + [j for j, _ in finals]
    done = finish_times(jobs)
    lag = [chunk + d - j.arrival for j, d in zip(partials, done)]
    final = [d - speech_end for (_, speech_end), d in zip(finals, done[len(partials):])]
    return lag, final


def _latency(results: list[dict], chunk: float, endpoint: float) -> dict:
    lags, stream_final, hybrid_final = [], [], []
    stream_compute = hybrid_compute = audio = 0.0
    for r in results:
        lag, final = _replay([r], "streaming", chunk, endpoint)
        lags += lag
        stream_final += final
        hybrid_final += _replay([r], "hybrid", chunk, endpoint)[1]
        audio += r["audio_sec"]
        steps = sum(s["compute"] for u in r["utterances"] for s in u["steps"])
        stream_compute += steps + sum(u["finish"]["compute"] for u in r["utterances"])
        hybrid_compute += steps + sum(u["offline"]["compute"] for u in r["utterances"])

    pair_lags, pair_stream, pair_hybrid, pairs = [], [], [], 0
    for i in range(0, len(results) - 1, 2):
        pair = results[i : i + 2]
        lag, final = _replay(pair, "streaming", chunk, endpoint)
        pair_lags += lag
        pair_stream += final
        pair_hybrid += _replay(pair, "hybrid", chunk, endpoint)[1]
        pairs += 1
    return {
        "chunk_sec": chunk,
        "endpoint_silence_sec": endpoint,
        "partial_lag_p95": percentile(lags, 95),
        "streaming_final_p95": percentile(stream_final, 95),
        "hybrid_final_p95": percentile(hybrid_final, 95),
        "rtf_streaming": stream_compute / audio if audio else None,
        "rtf_hybrid": hybrid_compute / audio if audio else None,
        "two_tracks": {
            "pairs": pairs,
            "partial_lag_p95": percentile(pair_lags, 95),
            "streaming_final_p95": percentile(pair_stream, 95),
            "hybrid_final_p95": percentile(pair_hybrid, 95),
        },
    }


def _compare(samples: list[Sample], results: dict) -> dict:
    offline_better, streaming_better, term_off, term_str = [], [], [], []
    for s in samples:
        r = results[s.id]
        ws = word_errors(s.reference, r["streaming_final"]).errors
        wo = word_errors(s.reference, r["offline"]).errors
        if wo < ws:
            offline_better.append(s.id)
        elif ws < wo:
            streaming_better.append(s.id)
        hs = sum(term_hits(t, s.reference, r["streaming_final"])[0] for t in s.terms)
        ho = sum(term_hits(t, s.reference, r["offline"])[0] for t in s.terms)
        if ho > hs:
            term_off.append(s.id)
        elif hs > ho:
            term_str.append(s.id)
    return {
        "offline_better": offline_better,
        "streaming_better": streaming_better,
        "term_wins_offline": term_off,
        "term_wins_streaming": term_str,
    }


def summarize(samples: list[Sample], results: dict, chunk_sec: float, endpoint_silence_sec: float) -> dict:
    missing = [s.id for s in samples if s.id not in results]
    if missing:
        raise ValueError(f"no results for samples {missing}")
    ordered = [results[s.id] for s in samples]
    worst = sorted(
        (s for s in samples if "silence" not in s.categories),
        key=lambda s: word_errors(s.reference, results[s.id]["streaming_final"]).rate or 0.0,
        reverse=True,
    )[:10]
    return {
        "samples": len(samples),
        "kinds": {k: _quality(samples, results, k) for k in KINDS},
        "latency": _latency(ordered, chunk_sec, endpoint_silence_sec),
        "streaming_vs_offline": _compare(samples, results),
        "truncated": [s.id for s in samples if results[s.id]["truncated"]],
        "worst": [
            {
                "id": s.id,
                "reference": s.reference,
                "streaming_final": results[s.id]["streaming_final"],
                "offline": results[s.id]["offline"],
            }
            for s in worst
        ],
    }


def _pct(value) -> str:
    return "—" if value is None else f"{value * 100:.1f}%"


def _sec(value) -> str:
    return "—" if value is None else f"{value:.2f} с"


def render_markdown(summary: dict, run: dict) -> str:
    lines = ["# Результат STT benchmark", "", "```json", json.dumps(run, ensure_ascii=False, indent=2), "```", ""]
    lines += [f"Образцов: {summary['samples']}. Усечённый вывод (finish_reason=length): {summary['truncated'] or 'нет'}.", ""]
    lines += ["## Качество", "", "| Метрика | streaming final | offline |", "| --- | --- | --- |"]
    s, o = summary["kinds"]["streaming_final"], summary["kinds"]["offline"]
    lines += [
        f"| WER | {_pct(s['wer']['rate'])} | {_pct(o['wer']['rate'])} |",
        f"| WER mixed RU/EN | {_pct(s['wer_mixed']['rate'])} | {_pct(o['wer_mixed']['rate'])} |",
        f"| CER | {_pct(s['cer']['rate'])} | {_pct(o['cer']['rate'])} |",
        f"| Термины | {_pct(s['terms']['rate'])} ({s['terms']['hits']}/{s['terms']['expected']}) | "
        f"{_pct(o['terms']['rate'])} ({o['terms']['hits']}/{o['terms']['expected']}) |",
        f"| Числа/отрицания/условия | {_pct(s['critical']['rate'])} ({s['critical']['preserved']}/{s['critical']['samples']}) | "
        f"{_pct(o['critical']['rate'])} ({o['critical']['preserved']}/{o['critical']['samples']}) |",
        f"| Выдуманный текст на тишине | {len(s['silence']['hallucinated'])}/{s['silence']['samples']} | "
        f"{len(o['silence']['hallucinated'])}/{o['silence']['samples']} |",
        "",
        "WER по говорящим (streaming final): "
        + ", ".join(f"{k} {_pct(v['rate'])}" for k, v in s["per_speaker"].items()),
        "",
    ]
    lat = summary["latency"]
    two = lat["two_tracks"]
    lines += [
        "## Задержка",
        "",
        f"Replay измеренных decode-шагов на одной последовательной модели; chunk {lat['chunk_sec']} с, "
        f"endpoint silence {lat['endpoint_silence_sec']} с. Partial lag — верхняя граница для слова в начале chunk.",
        "",
        "| Метрика | 1 дорожка | 2 дорожки |",
        "| --- | --- | --- |",
        f"| p95 partial lag | {_sec(lat['partial_lag_p95'])} | {_sec(two['partial_lag_p95'])} |",
        f"| p95 streaming final после речи | {_sec(lat['streaming_final_p95'])} | {_sec(two['streaming_final_p95'])} |",
        f"| p95 hybrid final после речи | {_sec(lat['hybrid_final_p95'])} | {_sec(two['hybrid_final_p95'])} |",
        f"| RTF streaming / hybrid | {lat['rtf_streaming']:.3f} / {lat['rtf_hybrid']:.3f} | пар: {two['pairs']} |"
        if lat["rtf_streaming"] is not None
        else "| RTF | — | — |",
        "",
    ]
    cmp = summary["streaming_vs_offline"]
    lines += [
        "## Streaming final против offline",
        "",
        f"- offline лучше по WER: {len(cmp['offline_better'])} {cmp['offline_better']}",
        f"- streaming лучше по WER: {len(cmp['streaming_better'])} {cmp['streaming_better']}",
        f"- термины лучше в offline: {cmp['term_wins_offline']}; в streaming: {cmp['term_wins_streaming']}",
        "",
        "## Худшие примеры",
        "",
    ]
    for w in summary["worst"]:
        lines += [f"- `{w['id']}`", f"  - эталон: {w['reference']}", f"  - streaming: {w['streaming_final']}", f"  - offline: {w['offline']}"]
    return "\n".join(lines) + "\n"


def read_results(path: Path) -> tuple[dict, dict]:
    run, results = {}, {}
    for line in Path(path).read_text(encoding="utf-8").splitlines():
        record = json.loads(line)
        kind = record.pop("type")
        if kind == "run":
            run = record
        elif kind == "sample":
            results[record.pop("id")] = record
        elif kind == "end":
            run["complete"] = True
    return run, results


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True, type=Path)
    parser.add_argument("--results", required=True, type=Path)
    parser.add_argument("--split", default="dev", choices=("dev", "holdout", "all"))
    parser.add_argument("--endpoint-silence", type=float, default=0.8)
    parser.add_argument("--out", type=Path)
    parser.add_argument("--json", type=Path)
    args = parser.parse_args(argv)

    m = manifest_mod.load(args.manifest)
    leaks = manifest_mod.split_leaks(m)
    if leaks:
        print("split leakage: " + "; ".join(leaks), file=sys.stderr)
        return 1
    run, results = read_results(args.results)
    samples = m.select(args.split)
    summary = summarize(samples, results, run["config"]["chunk_sec"], args.endpoint_silence)
    text = render_markdown(summary, run)
    if args.json:
        args.json.write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
    if args.out:
        args.out.write_text(text, encoding="utf-8")
    else:
        sys.stdout.write(text)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
