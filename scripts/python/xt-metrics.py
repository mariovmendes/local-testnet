#!/usr/bin/env python3
"""Window the publisher's metrics without restarting anything.

Prometheus counters are monotonic on purpose, so "resetting" them is the wrong
handle: you snapshot before a test and subtract after. Per-run avg/p99 come out
of the histogram deltas, which is exactly what Grafana's
rate()/histogram_quantile() would compute over the same window.

    ./xt-metrics.py mark              # before a threshold test
    ./xt-metrics.py report            # after it
    ./xt-metrics.py report --label t=8 --csv results.csv

`mark` is implicit: report always re-marks, so back-to-back runs just alternate
report calls.
"""
import argparse
import csv
import json
import os
import sys
import urllib.request

DEFAULT_URL = "http://127.0.0.1:18081/metrics"
STATE = os.path.join(os.path.dirname(os.path.abspath(__file__)), ".xt-metrics-mark.json")

# (label, metric prefix) for the two histograms we report on.
HISTOGRAMS = [
    ("decision", "publisher_xt_decision_latency_seconds"),
    ("inclusion", "publisher_xt_block_inclusion_latency_seconds"),
]
COUNTERS = [
    ("started", "publisher_xt_started_total"),
    ("committed", "publisher_xt_decided_commit_total"),
    ("aborted", "publisher_xt_decided_abort_total"),
]


def scrape(url):
    """Parse the Prometheus text exposition format into {series: value}.

    Only the flat `name{labels} value` lines matter here; the publisher exports
    no multi-label series, so the label set is kept verbatim as part of the key.
    """
    out = {}
    with urllib.request.urlopen(url, timeout=10) as r:
        body = r.read().decode()
    for line in body.splitlines():
        line = line.strip()
        if not line or line.startswith("#") or line == "# EOF":
            continue
        name, _, value = line.rpartition(" ")
        try:
            out[name] = float(value)
        except ValueError:
            continue
    return out


def buckets(snap, prefix):
    """Cumulative (le, count) pairs for a histogram, sorted by le."""
    pairs = []
    for series, count in snap.items():
        if not series.startswith(prefix + "_bucket{"):
            continue
        le = series.split('le="', 1)[1].split('"', 1)[0]
        pairs.append((float(le), count))
    return sorted(pairs)


def quantile(bkts, total, p):
    """Linear interpolation inside the matching bucket, as histogram_quantile does."""
    if total <= 0:
        return float("nan")
    rank = p * total
    prev_le, prev_c = 0.0, 0.0
    for le, c in bkts:
        if c >= rank:
            if le == float("inf") or c == prev_c:
                return prev_le
            return prev_le + (le - prev_le) * (rank - prev_c) / (c - prev_c)
        prev_le, prev_c = le, c
    return bkts[-1][0] if bkts else float("nan")


def window(before, after):
    """Deltas between two snapshots -> the numbers for this run alone."""
    row = {}
    for label, name in COUNTERS:
        row[label] = int(after.get(name, 0) - before.get(name, 0))
    for label, prefix in HISTOGRAMS:
        d_sum = after.get(prefix + "_sum", 0) - before.get(prefix + "_sum", 0)
        d_count = after.get(prefix + "_count", 0) - before.get(prefix + "_count", 0)
        b0, b1 = buckets(before, prefix), buckets(after, prefix)
        delta = [(le, c - dict(b0).get(le, 0.0)) for le, c in b1]
        row[label + "_count"] = int(d_count)
        row[label + "_avg"] = d_sum / d_count if d_count else float("nan")
        row[label + "_p50"] = quantile(delta, d_count, 0.50)
        row[label + "_p99"] = quantile(delta, d_count, 0.99)
    return row


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("action", choices=["mark", "report"])
    ap.add_argument("--url", default=DEFAULT_URL)
    ap.add_argument("--label", default="", help="tag for the CSV row, e.g. threshold=8")
    ap.add_argument("--csv", help="append the row to this CSV")
    ap.add_argument("--json", help="write the row (plus --meta) to this JSON file")
    ap.add_argument("--meta", action="append", default=[], metavar="K=V",
                    help="extra key=value recorded in --json (threads, interval_ms, duration_s, ...)")
    args = ap.parse_args()

    snap = scrape(args.url)

    if args.action == "mark":
        with open(STATE, "w") as f:
            json.dump(snap, f)
        print(f"marked ({len(snap)} series) -> run your test, then: {sys.argv[0]} report")
        return

    if not os.path.exists(STATE):
        sys.exit(f"no mark found; run `{sys.argv[0]} mark` before the test")
    with open(STATE) as f:
        before = json.load(f)

    row = window(before, snap)
    with open(STATE, "w") as f:  # re-mark so consecutive runs chain
        json.dump(snap, f)

    print(f"\n===== XT METRICS (windowed{': ' + args.label if args.label else ''}) =====")
    print(f"  started / committed / aborted : {row['started']} / {row['committed']} / {row['aborted']}")
    print(f"  latency (decision)  avg / p99 : {row['decision_avg'] * 1000:.1f} ms / {row['decision_p99'] * 1000:.1f} ms")
    print(f"  latency (decision)  p50       : {row['decision_p50'] * 1000:.1f} ms")
    print(f"  latency (inclusion) avg / p99 : {row['inclusion_avg']:.3f} s / {row['inclusion_p99']:.3f} s")
    print(f"  latency (inclusion) p50       : {row['inclusion_p50']:.3f} s")
    print("=" * 46)

    if args.json:
        doc = {"label": args.label, **row}
        for kv in args.meta:
            k, _, v = kv.partition("=")
            try:
                doc[k] = float(v) if "." in v else int(v)
            except ValueError:
                doc[k] = v
        # The .ods columns the sheet actually wants but Prometheus cannot know:
        # offered load is a property of the generator, goodput of the window.
        dur = doc.get("duration_s") or 0
        threads, gap = doc.get("threads") or 0, doc.get("interval_ms") or 0
        doc["submission_per_s"] = threads * 1000.0 / gap if gap else float("nan")
        doc["throughput_per_s"] = row["committed"] / dur if dur else float("nan")
        with open(args.json, "w") as f:
            json.dump(doc, f, indent=2)
        print(f"wrote {args.json}")

    if args.csv:
        row = {"label": args.label, **row}
        new = not os.path.exists(args.csv)
        with open(args.csv, "a", newline="") as f:
            w = csv.DictWriter(f, fieldnames=list(row))
            if new:
                w.writeheader()
            w.writerow(row)
        print(f"appended to {args.csv}")


def _selfcheck():
    """The only non-trivial logic here is delta + interpolation. Pin both."""
    pre = {
        "publisher_xt_started_total": 100.0,
        "publisher_xt_decided_commit_total": 90.0,
        "publisher_xt_decided_abort_total": 10.0,
        "publisher_xt_decision_latency_seconds_sum": 50.0,
        "publisher_xt_decision_latency_seconds_count": 100.0,
        'publisher_xt_decision_latency_seconds_bucket{le="0.1"}': 100.0,
        'publisher_xt_decision_latency_seconds_bucket{le="1.0"}': 100.0,
    }
    post = {
        "publisher_xt_started_total": 300.0,
        "publisher_xt_decided_commit_total": 280.0,
        "publisher_xt_decided_abort_total": 20.0,
        "publisher_xt_decision_latency_seconds_sum": 90.0,
        "publisher_xt_decision_latency_seconds_count": 300.0,
        'publisher_xt_decision_latency_seconds_bucket{le="0.1"}': 200.0,
        'publisher_xt_decision_latency_seconds_bucket{le="1.0"}': 300.0,
    }
    r = window(pre, post)
    assert (r["started"], r["committed"], r["aborted"]) == (200, 190, 10), r
    # 200 new observations, 40s of new latency -> 0.2s avg (not the 0.3 lifetime avg)
    assert abs(r["decision_avg"] - 0.2) < 1e-9, r["decision_avg"]
    # deltas: 100 in (0,0.1], 100 in (0.1,1.0]; p99 -> rank 198 of 200
    assert abs(r["decision_p99"] - (0.1 + 0.9 * 98 / 100)) < 1e-9, r["decision_p99"]
    # p50 -> rank 100 of 200, exactly the top of the first delta bucket
    assert abs(r["decision_p50"] - 0.1) < 1e-9, r["decision_p50"]
    # the real run from 2026-09-12, straight off the histogram
    real_pre = {"publisher_xt_decision_latency_seconds_sum": 0.0, "publisher_xt_decision_latency_seconds_count": 0.0}
    real_post = {
        "publisher_xt_decision_latency_seconds_sum": 958.5027285290076,
        "publisher_xt_decision_latency_seconds_count": 22500.0,
        'publisher_xt_decision_latency_seconds_bucket{le="0.01"}': 0.0,
        'publisher_xt_decision_latency_seconds_bucket{le="0.05"}': 18453.0,
        'publisher_xt_decision_latency_seconds_bucket{le="0.1"}': 22093.0,
        'publisher_xt_decision_latency_seconds_bucket{le="0.25"}': 22440.0,
        'publisher_xt_decision_latency_seconds_bucket{le="0.5"}': 22500.0,
    }
    r = window(real_pre, real_post)
    assert abs(r["decision_avg"] - 0.0426) < 1e-4, r["decision_avg"]
    assert abs(r["decision_p99"] - 0.1787) < 1e-4, r["decision_p99"]
    print("selfcheck ok")


if __name__ == "__main__":
    if "--selfcheck" in sys.argv:
        _selfcheck()
    else:
        main()
