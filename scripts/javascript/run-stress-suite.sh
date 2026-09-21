#!/usr/bin/env bash
# Drives the full stress-test suite: for each (workers, accounts, gap) config,
# regenerate fresh accounts, run the stress at that gap for its fixed 30min
# window, and record windowed metrics under a run label.
set -uo pipefail
cd "$(dirname "$0")"

PYDIR="../python"
CSV="$PYDIR/results.csv"
LOG="run-stress-suite.log"

# workers accounts gap_ms
CONFIGS=(
  "2 2000 100"
  "5 5000 500"
  "5 5000 400"
  "5 5000 300"
  "5 5000 200"
  "5 5000 100"
  "10 5000 500"
  "10 5000 400"
  "10 5000 300"
  "10 5000 200"
  "10 5000 100"
)

log() { echo "[$(date '+%F %T')] $*" | tee -a "$LOG"; }

# Configs share a chain unless it is torn down between them, and the state that
# carries over is exactly the state that ruins the next run: an elevated
# basefee that has not decayed, a sidecar still holding stuck instances, and a
# full txpool. On 2026-09-13 that contamination made the *lighter* 500ms config
# score worse than the 100ms one (59% vs 26% aborted) and then failed the nine
# configs after it outright — create-accounts.js could not even fund accounts
# ("txpool is full"). Every row past the first was measuring damage, not load.
# RESET_BETWEEN=0 to skip the teardown when iterating on the harness itself.
RESET_BETWEEN="${RESET_BETWEEN:-1}"
REPO_ROOT="$(cd ../.. && pwd)"

reset_stack() {
  [ "$RESET_BETWEEN" = "1" ] || { log "reset skipped (RESET_BETWEEN=0)"; return 0; }
  log "tearing down and redeploying L2 for a clean chain"
  # `make scripts` re-syncs the contract addresses hardcoded in worker.js, so a
  # redeploy that lands different addresses cannot silently point the run at
  # dead contracts.
  (cd "$REPO_ROOT" && make clean-l2 && make run-l2 && make scripts) >>"$LOG" 2>&1
}

for cfg in "${CONFIGS[@]}"; do
  read -r workers accounts gap <<< "$cfg"
  label="w${workers}_a${accounts}_gap${gap}ms"
  log "=== starting run $label ==="

  if ! reset_stack; then
    log "!! stack reset failed for $label, skipping run"
    continue
  fi

  sed -i "s/AMOUNT_ACCOUNTS: [0-9]*/AMOUNT_ACCOUNTS: ${accounts}/" create-accounts.js
  sed -i "s/const numWorkers = [0-9]*;/const numWorkers = ${workers};/" generate-parallel-transactions.js
  sed -i "s/INTERVAL: [0-9]*/INTERVAL: ${gap}/" worker.js

  rm -f xt-submissions-w*.jsonl

  log "marking metrics baseline"
  python3 "$PYDIR/xt-metrics.py" mark >>"$LOG" 2>&1

  log "generating ${accounts} accounts"
  node create-accounts.js >>"$LOG" 2>&1
  if [ $? -ne 0 ]; then
    log "!! create-accounts.js failed for $label, skipping run"
    continue
  fi

  # Cheap up-front answer to "will anything be refused for gas?". Both refusals
  # ("gas price is less than basefee" and "lack of funds for max fee") are
  # predictable from basefee, balance and the signed gas limits, so there is no
  # reason to burn 30 minutes discovering them.
  if ! node check-gas-headroom.mjs >>"$LOG" 2>&1; then
    log "!! gas headroom check failed for $label — see $LOG; skipping run"
    continue
  fi

  log "running stress ($workers workers, ${gap}ms gap) — fixed 30min window"
  node generate-parallel-transactions.js >>"$LOG" 2>&1

  log "reporting metrics for $label"
  python3 "$PYDIR/xt-metrics.py" report --label "$label" --csv "$CSV" >>"$LOG" 2>&1

  archive="runs/${label}"
  mkdir -p "$archive"
  mv xt-submissions-w*.jsonl "$archive/" 2>/dev/null
  cp accounts.json "$archive/accounts.json" 2>/dev/null

  log "=== finished run $label ==="
done

log "=== suite complete ==="
