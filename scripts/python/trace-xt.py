#!/usr/bin/env python3
"""Reconstruct the full life of every cross-chain transaction from XTFLOW logs.

Both sidecars and both op-rbuilders emit one single-line record per pipeline
stage:

    XTFLOW stage=<stage> instance_id=<id> key=value ...

This script collects those lines (from `docker logs` by default), joins them
with the submissions recorded by `scripts/javascript/worker.js`, and reports
per-instance timelines plus what each unfinished instance is waiting on.

    ./trace-xt.py                       # summary + diagnosis of every instance
    ./trace-xt.py --stuck               # only instances that never finished
    ./trace-xt.py --instance <id>       # full ordered timeline for one instance
    ./trace-xt.py --address 0xabc...    # timelines for one account's XTs
    ./trace-xt.py --logs sidecar-a.log op-rbuilder-a.log
    ./trace-xt.py --selftest
"""

from __future__ import annotations

import argparse
import glob
import json
import re
import subprocess
import sys
from collections import Counter, defaultdict
from dataclasses import dataclass, field

DEFAULT_CONTAINERS = [
    "publisher",
    "sidecar-a",
    "sidecar-b",
    "op-rbuilder-a",
    "op-rbuilder-b",
]
DEFAULT_SUBMISSIONS_GLOB = "scripts/javascript/xt-submissions-w*.jsonl"

# `key=` boundaries; a value runs until the next key or end of line, so values
# containing spaces (error messages) survive intact.
KEY_RE = re.compile(r"(?:^| )([a-z_]+)=")
TS_RE = re.compile(r"(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?Z?)")
# The sidecar's `pretty` formatter colourises output even when piped.
ANSI_RE = re.compile(r"\x1b\[[0-9;]*[a-zA-Z]")


@dataclass
class Event:
    source: str
    ts: str
    stage: str
    fields: dict[str, str]

    def get(self, key: str, default: str = "") -> str:
        return self.fields.get(key, default)

    def __str__(self) -> str:
        extra = " ".join(
            f"{k}={v}" for k, v in self.fields.items() if k != "instance_id"
        )
        return f"{self.ts or '-':<32} {self.source:<14} {self.stage:<26} {extra}"


@dataclass
class Instance:
    instance_id: str
    events: list[Event] = field(default_factory=list)
    submission: dict | None = None

    def stages(self, source_prefix: str = "") -> set[str]:
        return {
            e.stage
            for e in self.events
            if e.source.startswith(source_prefix)
        }

    def first(self, stage: str) -> Event | None:
        return next((e for e in self.events if e.stage == stage), None)

    def all(self, stage: str) -> list[Event]:
        return [e for e in self.events if e.stage == stage]

    def sources(self) -> list[str]:
        return sorted({e.source for e in self.events})


def parse_line(source: str, line: str) -> Event | None:
    """Parse one XTFLOW log line into an Event, or None if it isn't one."""
    line = ANSI_RE.sub("", line)
    marker = line.find("XTFLOW stage=")
    if marker < 0:
        return None
    ts_match = TS_RE.search(line[:marker])
    # The stage always comes first; take it positionally so a record that also
    # carries a `stage`-like field cannot shadow it.
    rest = line[marker + len("XTFLOW stage=") :].rstrip()
    stage, _, body = rest.partition(" ")
    if not stage:
        return None

    keys = list(KEY_RE.finditer(body))
    fields: dict[str, str] = {}
    for i, match in enumerate(keys):
        end = keys[i + 1].start() if i + 1 < len(keys) else len(body)
        fields[match.group(1)] = body[match.end() : end].strip()

    return Event(source, ts_match.group(1) if ts_match else "", stage, fields)


def read_source(source: str, lines) -> list[Event]:
    return [e for e in (parse_line(source, l) for l in lines) if e]


def collect_events(log_files: list[str], containers: list[str], since: str) -> list[Event]:
    events: list[Event] = []
    for path in log_files:
        with open(path, errors="replace") as handle:
            events += read_source(path.rsplit("/", 1)[-1], handle)
    for container in containers:
        cmd = ["docker", "logs", "--timestamps"]
        if since:
            cmd += ["--since", since]
        cmd.append(container)
        proc = subprocess.run(cmd, capture_output=True, text=True, errors="replace")
        if proc.returncode != 0:
            print(f"warning: docker logs {container} failed: {proc.stderr.strip()}",
                  file=sys.stderr)
            continue
        events += read_source(container, (proc.stdout + proc.stderr).splitlines())
    events.sort(key=lambda e: (e.ts, e.source))
    return events


def load_submissions(patterns: list[str]) -> list[dict]:
    records = []
    for pattern in patterns:
        for path in sorted(glob.glob(pattern)):
            with open(path, errors="replace") as handle:
                for line in handle:
                    line = line.strip()
                    if line:
                        try:
                            records.append(json.loads(line))
                        except json.JSONDecodeError:
                            pass
    return records


def build_instances(events: list[Event], submissions: list[dict]) -> dict[str, Instance]:
    instances: dict[str, Instance] = {}
    for event in events:
        instance_id = event.get("instance_id")
        if not instance_id or instance_id in ("-", "None"):
            continue
        instances.setdefault(instance_id, Instance(instance_id)).events.append(event)
    for record in submissions:
        instance_id = record.get("instance_id")
        if instance_id:
            instances.setdefault(instance_id, Instance(instance_id)).submission = record
    return instances


def diagnose(instance: Instance) -> tuple[str, str]:
    """Return (verdict, detail) for one instance: where it stopped and why."""
    stages = instance.stages()
    sidecar_sources = {e.source for e in instance.events if "sidecar" in e.source}

    if instance.first("included") and instance.first("terminal"):
        return "done", ""

    # The publisher's SCP timeout fired: it gave up waiting for votes and
    # broadcast Decided(false). Report it ahead of the downstream symptoms,
    # since everything after it is the abort path, not the original fault.
    timeout = instance.first("scp_timeout")
    if timeout:
        detail = (
            f"publisher gave up after {timeout.get('age_ms')}ms "
            f"(timeout {timeout.get('timeout_ms')}ms), "
            f"votes={timeout.get('votes')} of {timeout.get('expected_votes')} chains"
        )
        if not ({"abort_ok", "terminal"} & stages):
            return "timed_out_no_compensation", detail
        if "pool_complete" not in instance.stages("op-rbuilder"):
            return "timed_out_pool_not_cleared", detail
        return "timed_out", detail

    nonce_gap = instance.first("nonce_gap_reject")
    if nonce_gap:
        return "builder_nonce_gap", (
            f"{nonce_gap.get('call')} sender={nonce_gap.get('sender')} "
            f"expected={nonce_gap.get('expected_nonce')} got={nonce_gap.get('got_nonce')}"
        )

    rejected = instance.first("rpc_rejected")
    if rejected:
        return "builder_rejected", f"{rejected.get('call')}: {rejected.get('error')}"

    submit_err = instance.first("builder_submit_err")
    if submit_err:
        return "builder_submit_failed", submit_err.get("error")

    if "start_instance" in stages and len(sidecar_sources) < 2:
        return "one_sided", f"only registered on {sorted(sidecar_sources)}"

    if "builder_submit_ok" in stages and "pool_selected" not in stages:
        blocked = instance.all("pool_blocked")
        detail = (
            f"last blocked: {blocked[-1].get('reason')} "
            f"(entry_nonce={blocked[-1].get('entry_nonce')} "
            f"current_nonce={blocked[-1].get('current_nonce')} "
            f"status={blocked[-1].get('status')})"
            if blocked
            else "never appeared in the builder's executable set"
        )
        return "never_selected_by_builder", detail

    if "xt_executed" in stages and "canonical_included" not in stages:
        return "executed_but_not_canonical", "ran in a flashblock that never became canonical"

    hanging = [e for e in instance.all("canonical_included") if e.get("hanging_count", "0") != "0"]
    if hanging and "pool_complete" not in stages:
        return "entries_left_hanging", hanging[-1].get("hanging")

    if "vote" in stages and "decision" not in stages:
        voted = {e.get("chain") for e in instance.all("vote")}
        return "no_decision", (
            f"chains {sorted(voted)} voted, but no Decided arrived "
            "(and the publisher's SCP timeout did not fire either)"
        )

    if "decision" in stages and not ({"confirm_ok", "abort_ok"} & stages):
        errors = instance.all("confirm_err") + instance.all("abort_err")
        return "no_finalization", errors[-1].get("error") if errors else "confirm/abort never completed"

    if "register_begin" in stages and "vote" not in stages:
        rejects = instance.all("process_reject")
        if rejects:
            return "process_rejected", rejects[-1].get("reason")
        return "waiting_for_messages", "registered but never reached a vote"

    if "start_instance" in stages and "register_begin" not in stages:
        return "never_registered", "StartInstance seen but the chunk processor never picked it up"

    if not stages:
        return "no_logs", "submitted but no XTFLOW record anywhere"

    if "included" not in stages:
        return "not_confirmed_included", "builder never confirmed inclusion back to the sidecar"

    return "incomplete", ",".join(sorted(stages))


def print_timeline(instance: Instance) -> None:
    print(f"\n=== instance {instance.instance_id} ===")
    if instance.submission:
        sub = instance.submission
        print(
            f"submitted by worker {sub.get('worker')} seq={sub.get('seq')} "
            f"at {sub.get('sent_at')} ({sub.get('latency_ms')}ms)\n"
            f"  account={sub.get('address')} session={sub.get('session_id')}\n"
            f"  nonces: source={sub.get('source_nonce')} dest={sub.get('dest_nonce')}\n"
            f"  approve={sub.get('approve_tx')}\n  bridge ={sub.get('bridge_tx')}\n"
            f"  receive={sub.get('receive_tx')}"
        )
    verdict, detail = diagnose(instance)
    print(f"verdict: {verdict}" + (f" — {detail}" if detail else ""))
    print("-" * 100)
    for event in instance.events:
        print(event)


def print_summary(instances: dict[str, Instance], submissions: list[dict], stuck_only: bool) -> None:
    verdicts = Counter()
    rows = []
    for instance in instances.values():
        verdict, detail = diagnose(instance)
        verdicts[verdict] += 1
        if stuck_only and verdict == "done":
            continue
        rows.append((instance, verdict, detail))

    failed_submissions = [s for s in submissions if not s.get("instance_id")]

    print(f"submissions recorded: {len(submissions)} "
          f"(failed before an instance_id was assigned: {len(failed_submissions)})")
    print(f"instances seen in logs: {len(instances)}")
    print("\nverdicts:")
    for verdict, count in verdicts.most_common():
        print(f"  {count:>6}  {verdict}")

    if failed_submissions:
        print("\nsubmissions that never got an instance_id:")
        for sub in failed_submissions[:20]:
            print(f"  {sub.get('sent_at')} {sub.get('address')} "
                  f"nonce={sub.get('source_nonce')} error={sub.get('error')}")
        if len(failed_submissions) > 20:
            print(f"  ... and {len(failed_submissions) - 20} more")

    rows.sort(key=lambda row: (row[1], row[0].events[0].ts if row[0].events else ""))
    print(f"\n{'instance':<40} {'verdict':<28} detail")
    print("-" * 130)
    for instance, verdict, detail in rows:
        if verdict == "done" and stuck_only:
            continue
        account = (instance.submission or {}).get("address", "")
        suffix = f"[{account}] " if account else ""
        print(f"{instance.instance_id:<40} {verdict:<28} {suffix}{detail}")

    blocked_senders = defaultdict(set)
    for instance, verdict, _ in rows:
        if verdict == "done":
            continue
        account = (instance.submission or {}).get("address")
        if account:
            blocked_senders[account].add(instance.instance_id)
    multi = {a: ids for a, ids in blocked_senders.items() if len(ids) > 1}
    if multi:
        print("\naccounts with more than one unfinished XT (nonce chain is stalled):")
        for account, ids in sorted(multi.items(), key=lambda kv: -len(kv[1]))[:20]:
            print(f"  {account}  {len(ids)} instances: {', '.join(sorted(ids)[:4])}"
                  f"{' ...' if len(ids) > 4 else ''}")


def selftest() -> None:
    line = ("2026-09-05T10:00:00.123456Z  INFO xtflow: XTFLOW stage=builder_submit "
            "instance_id=ab12 chain=77777 tx_count=2 txs=77777/0xaa/3/0x095ea7b3/0xdead")
    event = parse_line("sidecar-a", line)
    assert event is not None
    assert event.stage == "builder_submit", event.stage
    assert event.get("instance_id") == "ab12"
    assert event.get("tx_count") == "2"
    assert event.ts.startswith("2026-09-05T10:00:00")

    # An error value with spaces must not be split into further keys.
    err = parse_line(
        "op-rbuilder-a",
        "XTFLOW stage=rpc_rejected instance_id=ab12 call=ethera_submitXt "
        "error=nonce gap for 0xabc: expected 3, got 4",
    )
    assert err is not None
    assert err.get("call") == "ethera_submitXt"
    assert err.get("error") == "nonce gap for 0xabc: expected 3, got 4", err.get("error")

    assert parse_line("x", "unrelated log line") is None

    coloured = parse_line(
        "sidecar-a",
        "\x1b[2m2026-09-05T10:00:00.1Z\x1b[0m \x1b[32m INFO\x1b[0m xtflow: "
        "\x1b[1mXTFLOW stage=vote instance_id=ab12 vote=true\x1b[0m",
    )
    assert coloured is not None and coloured.stage == "vote"
    assert coloured.get("vote") == "true", repr(coloured.get("vote"))

    # A record carrying its own chunk stage must not shadow the record stage.
    stuck = parse_line(
        "sidecar-a",
        "XTFLOW stage=stuck instance_id=ab12 age_ms=19800 chunk_stage=WaitingForMessages "
        "decision=None local_vote=None",
    )
    assert stuck is not None and stuck.stage == "stuck", stuck.stage
    assert stuck.get("chunk_stage") == "WaitingForMessages"

    stuck = Instance("ab12")
    stuck.events = [
        parse_line("sidecar-a", "XTFLOW stage=start_instance instance_id=ab12"),
        parse_line("sidecar-b", "XTFLOW stage=start_instance instance_id=ab12"),
        parse_line("sidecar-a", "XTFLOW stage=register_begin instance_id=ab12"),
        parse_line("sidecar-a", "XTFLOW stage=builder_submit_ok instance_id=ab12"),
        parse_line("op-rbuilder-a",
                   "XTFLOW stage=pool_blocked instance_id=ab12 status=locked "
                   "entry_nonce=5 current_nonce=Some(4) reason=nonce does not match"),
    ]
    verdict, detail = diagnose(stuck)
    assert verdict == "never_selected_by_builder", verdict
    assert "nonce does not match" in detail, detail

    timed_out = Instance("ef56")
    timed_out.events = [
        parse_line("publisher", "XTFLOW stage=xt_prepared instance_id=ef56 chains=2"),
        parse_line("sidecar-a", "XTFLOW stage=vote instance_id=ef56 chain=77777 vote=true"),
        parse_line("publisher",
                   "XTFLOW stage=scp_timeout instance_id=ef56 age_ms=20001 timeout_ms=20000 "
                   "votes=77777:true expected_votes=2"),
        parse_line("sidecar-a", "XTFLOW stage=decision instance_id=ef56 decision=false"),
    ]
    verdict, detail = diagnose(timed_out)
    assert verdict == "timed_out_no_compensation", verdict
    assert "20001ms" in detail and "77777:true" in detail, detail

    done = Instance("cd34")
    done.events = [
        parse_line("sidecar-a", "XTFLOW stage=included instance_id=cd34"),
        parse_line("sidecar-a", "XTFLOW stage=terminal instance_id=cd34 confirmed_stage=Confirmed"),
    ]
    assert diagnose(done)[0] == "done"
    print("selftest ok")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--logs", nargs="*", default=[],
                        help="log files to read instead of/in addition to docker logs")
    parser.add_argument("--containers", nargs="*", default=None,
                        help=f"docker containers to read (default: {' '.join(DEFAULT_CONTAINERS)})")
    parser.add_argument("--submissions", nargs="*", default=[DEFAULT_SUBMISSIONS_GLOB],
                        help="glob(s) for worker.js submission JSONL files")
    parser.add_argument("--since", default="",
                        help="passed to `docker logs --since` (e.g. 30m)")
    parser.add_argument("--instance", help="print the full timeline of one instance")
    parser.add_argument("--address", help="print timelines for one account's instances")
    parser.add_argument("--stage", help="print every event of one stage across all instances")
    parser.add_argument("--stuck", action="store_true", help="only report unfinished instances")
    parser.add_argument("--selftest", action="store_true", help="run parser/diagnosis checks")
    args = parser.parse_args()

    if args.selftest:
        selftest()
        return 0

    containers = args.containers if args.containers is not None else (
        [] if args.logs else DEFAULT_CONTAINERS
    )
    events = collect_events(args.logs, containers, args.since)
    if not events:
        print("no XTFLOW lines found — is the instrumented build running?", file=sys.stderr)
        return 1

    submissions = load_submissions(args.submissions)
    instances = build_instances(events, submissions)

    if args.stage:
        for event in events:
            if event.stage == args.stage:
                print(f"{event.ts:<32} {event.source:<14} "
                      f"{event.get('instance_id', '-'):<40} "
                      + " ".join(f"{k}={v}" for k, v in event.fields.items()
                                 if k != "instance_id"))
        return 0

    if args.instance:
        instance = instances.get(args.instance)
        if not instance:
            print(f"no records for instance {args.instance}", file=sys.stderr)
            return 1
        print_timeline(instance)
        return 0

    if args.address:
        matches = [i for i in instances.values()
                   if (i.submission or {}).get("address", "").lower() == args.address.lower()]
        if not matches:
            print(f"no submissions recorded for {args.address}", file=sys.stderr)
            return 1
        for instance in sorted(matches, key=lambda i: (i.submission or {}).get("seq", 0)):
            print_timeline(instance)
        return 0

    print_summary(instances, submissions, args.stuck)
    return 0


if __name__ == "__main__":
    sys.exit(main())
