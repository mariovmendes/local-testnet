# Tracing a cross-chain transaction end to end

Every stage of the XT pipeline emits one single-line record:

```
XTFLOW stage=<stage> instance_id=<id> key=value ...
```

Sources: `publisher`, `sidecar-a`, `sidecar-b`, `op-rbuilder-a`, `op-rbuilder-b`
(all target `xtflow`, level `info`), plus
`scripts/javascript/xt-submissions-w<N>.jsonl` written by the load generator.
The publisher's `xt_id` is the hex of the instance-id bytes, i.e. the same
string the sidecars log as `instance_id`, so all five sources join on it.

```sh
# summary + verdict for every instance seen
python3 scripts/python/trace-xt.py

# only the ones that never finished
python3 scripts/python/trace-xt.py --stuck

# everything that happened to one instance, across all four containers
python3 scripts/python/trace-xt.py --instance <instance_id>

# all XTs of one funded account, in submission order
python3 scripts/python/trace-xt.py --address 0x...

# raw grep still works
docker logs sidecar-a 2>&1 | grep XTFLOW | grep <instance_id>
```

## Stages

### Publisher

| stage | meaning |
|---|---|
| `xt_prepared` | instance id assigned and `StartInstance` broadcast; `active_xts` is the publisher's in-flight count |
| `vote_in` | a sidecar's vote arrived |
| `decided` | 2PC completed normally (all votes in, or a false vote) |
| `scp_timeout` | **the round timed out**: age, configured timeout, and which chains had voted |
| `timeout_decided` | the resulting `Decided(false)` was broadcast |

### Submission (sidecar A)

| stage | meaning |
|---|---|
| `submit_received` | `POST /xt` arrived; lists every tx as `chain/sender/nonce/selector/hash` |
| `publisher_submit` / `publisher_submit_joined` | XtRequest sent to the publisher, or joined an identical in-flight one |
| `publisher_assigned` | publisher returned the `instance_id` (everything after this is keyed by it) |
| `publisher_assign_timeout` | **10s cap expired** — the only timeout on the submission path |
| `publisher_assign_rejected` / `publisher_submit_failed` | publisher refused / transport error |

### Registration (both sidecars)

| stage | meaning |
|---|---|
| `start_instance` | publisher's `StartInstance` accepted; `includes_local` says whether this chain participates |
| `start_instance_rejected` | stale/future period, stale sequence, or period not initialised |
| `backpressure` | **100 undecided XTs reached** — every new instance is rejected from here on |
| `signal_enqueued` / `signal_failed` | instance handed to (or dropped before) the chunk processor |
| `dispatch` / `dispatch_skip` / `dispatch_noop` / `dispatch_done` | chunk processor picked the instance up; `queued` is the channel backlog |
| `register_begin`, `stage` | chunk classified (`roles=approve+bridge` or `receive`) and moved `Registered → WaitingForMessages` |
| `register_pre_aborted` | an abort decision arrived before anything was submitted |

### Inclusion request (sidecar → builder)

| stage | meaning |
|---|---|
| `builder_submit` → `builder_submit_ok` / `builder_submit_err` | `ethera_submitXt`: the request for inclusion, with the exact txs |
| `builder_followup` → `_ok` / `_err` | `ethera_submitFollowup` (the putInbox for the ACK) |
| `builder_release` → `_ok` / `_err` | `ethera_releaseXt` (sendConfirm / sendAbort / recvConfirm / recvAbort) |
| `put_inbox_ok` / `put_inbox_err` / `put_inbox_build_err` | putInbox built and submitted, or failed (nonce is resynced on failure) |

### Cross-chain messaging

| stage | meaning |
|---|---|
| `mailbox_out` → `mailbox_out_ok` / `mailbox_out_err` | `POST /mailbox` to the peer sidecar (the SEND indication) |
| `mailbox_out_skip` | never sent — simulation failed or produced no outbound message (the peer will wait forever) |
| `mailbox_in` | peer's message received; `mailbox_in_not_advanced` means no chunk was waiting on it |
| `ack_out` → `ack_out_ok` / `ack_out_err` / `ack_out_skip` | `POST /mailbox/ack` back to the origin chain |
| `ack_in` / `ack_in_not_advanced` | ACK received on the origin chain |

### Voting and decision

| stage | meaning |
|---|---|
| `vote` / `vote_err` / `vote_skipped` | local vote sent to the publisher (or peers) |
| `peer_vote_in` | standalone-mode peer vote |
| `decision` | publisher's `Decided`; `decision_duplicate`, `decision_no_chunk_yet`, `decision_raced_ahead` cover the races |
| `confirm_begin` → `confirm_ok` / `confirm_err` | sendConfirm / recvConfirm |
| `abort_begin` → `abort_ok` / `abort_err` | sendAbort / recvAbort (+ removeInbox) |
| `terminal` | chunk's `confirmed_stage` reached `Confirmed`/`Aborted` |
| `included` / `included_unknown` | builder confirmed canonical inclusion back to the sidecar |

### Builder (op-rbuilder)

| stage | meaning |
|---|---|
| `rpc_in` → `rpc_ok` / `rpc_rejected` | XT control RPC received and accepted/refused (with the pool's reason) |
| `nonce_gap_reject` | the sidecar's nonce disagrees with the builder's — the XT never enters the pool |
| `pool_selected` / `pool_deferred` / `pool_blocked` | chosen for a flashblock, postponed for gas/DA, or not executable (with `entry_nonce` vs `current_nonce`) |
| `xt_executed`, `xt_tx_ok`, `xt_tx_reverted`, `xt_tx_failed` | per-instance and per-transaction execution outcome |
| `canonical_block`, `canonical_included` | canonical confirmation; **`hanging_count`/`hanging` list the entries that were *not* included and are still holding their sender's nonce** |
| `pool_complete` / `pool_abort` / `gate_restored` | instance removed, aborted, or re-gated after an abandoned flashblock |
| `pool_inflight` / `pool_dump` | one line per instance still held by the pool, emitted on every canonical block |
| `pool_tx_held` / `pool_tx_nonce_reserved` | a mempool tx skipped because of the XT gate or a reserved nonce |

### Watchdog (sidecar, every 10s)

| stage | meaning |
|---|---|
| `state_dump` | `pending`, `undecided` vs `max_pending=100`, chunk counts per stage, mailbox backlog |
| `stuck` | one line per XT undecided/unconfirmed for >20s, with its chunk stage, votes and mailbox count |

## Timeouts

The consensus round **is** time-bounded, by the publisher:

- **`CONSENSUS_TIMEOUT` (20s in this localnet, `docker-compose.yml`)** — the
  publisher's `reaper_loop` ticks every 1s and, for every `active_xt` older
  than the timeout, removes it and broadcasts `Decided(false)`
  (`publisher/crates/coordinator/src/coordinator.rs`, `reap_timed_out`). Logged
  as `scp_timeout` (with the votes it had collected) followed by
  `timeout_decided`. The sidecars see it as an ordinary `decision decision=false`
  and run the abort/compensation path.
- **`CONSENSUS_PERIOD_DURATION` (60s)** — on each `StartPeriod`, sidecars abort
  any still-undecided XT from a previous period, call `ethera_abortXt` on the
  builder for it, and send an abort vote to the publisher
  (`handle_start_period`).
- **`CONSENSUS_PROOF_WINDOW` (600s)** — proof collection expiry triggers a
  `Rollback`, which aborts pending XTs at the builder too.

Other, narrower bounds: **10s** for publisher `instance_id` assignment
(`publisher_assign_timeout`), **2s** per builder control RPC
(`builder_submit_err`), **10s** for the load generator's own `POST /xt`,
and **300s** sidecar cleanup, which only evicts **decided** XTs.

`circ_timeout_ms` in the sidecar config is inert: its only consumer
(`wait_for_dependencies`) is dead code in the interleaved flow.

### What the timeout does *not* clean up

The sidecar's chunk pipeline has no timer of its own — it relies entirely on
the publisher's `Decided(false)`. Two gaps follow, and both are visible in
these logs:

1. **The builder is never told.** In publisher mode `on_decision` does not call
   `ethera_abortXt` (the `apply_builder_command` call is commented out in
   `handlers/decision.rs`), and `handle_start_period` skips XTs that already
   have a decision — which a timed-out XT does. So a timed-out instance stays
   in the builder's `XtPool`: it only leaves via `mark_included` once *every*
   entry is Included, so if its original `Locked` approve/bridge entries never
   execute, the instance and its sender-nonce reservations live forever
   (`pool_inflight` on every canonical block, `pool_tx_held` /
   `pool_tx_nonce_reserved` for that account's later transactions). That is the
   `timed_out_pool_not_cleared` verdict.
2. **Compensation can silently not run.** `abort_xt` only compensates for
   `confirmed_stage` of `Registered` (sender) or `WaitingForMessages`; other
   stages fall through to `terminal` with no on-chain effect. `abort_err` and a
   missing `abort_ok` show this — verdict `timed_out_no_compensation`.

If instances pile up undecided *without* `scp_timeout` firing, the publisher
itself is the suspect (its own 100-XT `MAX_ACTIVE_XTS` cap is currently
commented out, so `xt_prepared active_xts=` is the number to watch).
