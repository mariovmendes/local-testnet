#!/usr/bin/env python3
"""Fold the suite's runs/<label>/metrics.json into measurementsbaseline.ods.

Writes a *new sheet* into a *copy* of the workbook rather than overwriting
Sheet1. Sheet1's latency columns carry no units and its own footnote calls one
of the formulas invalid, so silently pasting seconds on top of whatever those
numbers are would produce a table nobody can interpret. The new sheet uses the
same column order with the unit spelled out in the header; copy across by hand
once the units line up.

    ./fill-ods.py                       # runs/ -> measurementsbaseline-filled.ods
    ./fill-ods.py --in-place            # overwrite measurementsbaseline.ods
"""
import argparse
import glob
import json
import math
import os
import shutil
import xml.etree.ElementTree as ET
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", ".."))
RUNS = os.path.join(REPO, "scripts", "javascript", "runs")
ODS = os.path.join(REPO, "measurementsbaseline.ods")

NS = {
    "office": "urn:oasis:names:tc:opendocument:xmlns:office:1.0",
    "table": "urn:oasis:names:tc:opendocument:xmlns:table:1.0",
    "text": "urn:oasis:names:tc:opendocument:xmlns:text:1.0",
}
for p, u in NS.items():
    ET.register_namespace(p, u)


def q(prefix, tag):
    return "{%s}%s" % (NS[prefix], tag)


# (header, key in metrics.json). Same column order as Sheet1.
COLUMNS = [
    ("Ctxs per seconds", None),
    ("Latency (decision) - avg (s)", "decision_avg"),
    ("Latency (decision) - p50 (s)", "decision_p50"),
    ("Latency (decision) - p99 (s)", "decision_p99"),
    ("Latency (inclusion) - avg (s)", "inclusion_avg"),
    ("Latency (inclusion) - p50 (s)", "inclusion_p50"),
    ("Latency (inclusion) - p99 (s)", "inclusion_p99"),
    ("Throughput (Ctxs per second)", "throughput_per_s"),
    ("Submission", "submission_per_s"),
    ("Success", "committed"),
    ("Timeout", "aborted"),
    ("Total", "started"),
    ("Duration in Seconds", "duration_s"),
]


def cell(value):
    c = ET.Element(q("table", "table-cell"))
    if value is None or (isinstance(value, float) and math.isnan(value)):
        return c
    p = ET.SubElement(c, q("text", "p"))
    if isinstance(value, str):
        c.set(q("office", "value-type"), "string")
        p.text = value
    else:
        c.set(q("office", "value-type"), "float")
        c.set(q("office", "value"), repr(float(value)))
        p.text = ("%g" % value) if isinstance(value, int) else "%.4g" % value
    return c


def row(values):
    r = ET.Element(q("table", "table-row"))
    for v in values:
        r.append(cell(v))
    return r


def load_runs(runs_dir):
    out = []
    for path in sorted(glob.glob(os.path.join(runs_dir, "*", "metrics.json"))):
        with open(path) as f:
            out.append(json.load(f))
    # (threads, descending gap) — the sheet's reading order: 0.5s first.
    out.sort(key=lambda d: (d.get("threads", 0), -d.get("interval_ms", 0)))
    return out


def build_sheet(runs, name):
    t = ET.Element(q("table", "table"), {q("table", "name"): name})
    col = ET.SubElement(t, q("table", "table-column"))
    col.set(q("table", "number-columns-repeated"), str(len(COLUMNS)))

    current = None
    for r in runs:
        threads = r.get("threads")
        if threads != current:
            current = threads
            if t.find(q("table", "table-row")) is not None:  # blank line between blocks
                t.append(row([]))
            t.append(row(["%s Thread%s" % (threads, "" if threads == 1 else "s")]))
            t.append(row([h for h, _ in COLUMNS]))
        gap = r.get("interval_ms", 0)
        values = ["1 cTx per %gs" % (gap / 1000.0)]
        values += [r.get(k) for _, k in COLUMNS[1:]]
        t.append(row(values))

    t.append(row([]))
    t.append(row(["", "Synchronous Composability Protocol (Baseline)"]))
    return t


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--runs", default=RUNS)
    ap.add_argument("--ods", default=ODS)
    ap.add_argument("--out", help="output file (default: <ods>-filled.ods)")
    ap.add_argument("--in-place", action="store_true")
    ap.add_argument("--sheet", default="Baseline")
    args = ap.parse_args()

    runs = load_runs(args.runs)
    if not runs:
        raise SystemExit("no metrics.json under %s — run the suite first" % args.runs)
    print("loaded %d runs" % len(runs))

    out = args.ods if args.in_place else (args.out or args.ods.replace(".ods", "-filled.ods"))

    with zipfile.ZipFile(args.ods) as z:
        members = {n: z.read(n) for n in z.namelist()}

    doc = ET.fromstring(members["content.xml"])
    sheets = doc.find(".//" + q("office", "spreadsheet"))
    for existing in sheets.findall(q("table", "table")):
        if existing.get(q("table", "name")) == args.sheet:
            sheets.remove(existing)  # idempotent re-fill
    sheets.append(build_sheet(runs, args.sheet))
    members["content.xml"] = ET.tostring(doc, encoding="UTF-8", xml_declaration=True)

    tmp = out + ".tmp"
    with zipfile.ZipFile(tmp, "w", zipfile.ZIP_DEFLATED) as z:
        # mimetype must be first and stored uncompressed or the file is not an ODS.
        z.writestr(zipfile.ZipInfo("mimetype"), members.pop("mimetype"), zipfile.ZIP_STORED)
        for name, data in members.items():
            z.writestr(name, data)
    shutil.move(tmp, out)
    print("wrote %s (sheet %r)" % (out, args.sheet))


def _selfcheck():
    """The risky parts are the zip layout and cell typing. Round-trip both."""
    import tempfile

    runs = [
        {"threads": 1, "interval_ms": 500, "decision_avg": 0.04, "decision_p50": 0.03,
         "decision_p99": 0.18, "inclusion_avg": 2.0, "inclusion_p50": 1.8,
         "inclusion_p99": 12.4, "throughput_per_s": 2.36, "submission_per_s": 2.0,
         "committed": 4257, "aborted": 31539, "started": 35796, "duration_s": 1800},
        {"threads": 2, "interval_ms": 100, "committed": 1, "aborted": 2, "started": 3,
         "decision_avg": float("nan")},
    ]
    sheet = build_sheet(runs, "T")
    rows = sheet.findall(q("table", "table-row"))
    # header, columns, data, blank, header, columns, data, blank, footnote
    assert rows[0][0][0].text == "1 Thread", ET.tostring(rows[0])
    assert rows[2][0][0].text == "1 cTx per 0.5s"
    assert rows[2][9].get(q("office", "value")) == "4257.0"
    assert len(rows[6][1]) == 0, "NaN must produce an empty cell, not a value"

    with tempfile.TemporaryDirectory() as d:
        if not os.path.exists(ODS):
            print("selfcheck ok (sheet only; %s absent)" % ODS)
            return
        out = os.path.join(d, "o.ods")
        import sys
        sys.argv = ["x", "--runs", d, "--ods", ODS, "--out", out]
        rd = os.path.join(d, "r")
        os.makedirs(rd)
        for i, r in enumerate(runs):
            os.makedirs(os.path.join(rd, str(i)))
            with open(os.path.join(rd, str(i), "metrics.json"), "w") as f:
                json.dump(r, f)
        sys.argv = ["x", "--runs", rd, "--ods", ODS, "--out", out]
        main()
        with zipfile.ZipFile(out) as z:
            assert z.namelist()[0] == "mimetype"
            assert z.getinfo("mimetype").compress_type == zipfile.ZIP_STORED
            names = [t.get(q("table", "name"))
                     for t in ET.fromstring(z.read("content.xml")).iter(q("table", "table"))]
            assert "Sheet1" in names and "Baseline" in names, names
    print("selfcheck ok")


if __name__ == "__main__":
    import sys
    if "--selfcheck" in sys.argv:
        _selfcheck()
    else:
        main()
