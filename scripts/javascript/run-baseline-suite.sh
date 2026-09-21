#!/usr/bin/env bash
# Baseline suite: 20 (threads, accounts, gap) configs, each on a freshly
# redeployed L2 + observability stack, 30min of load, metrics windowed to the
# run and written to runs/<label>/metrics.json.
#
# L1 is never touched — only L2 and observability are torn down between runs.
#
#   ./run-baseline-suite.sh              # all 20
#   ./run-baseline-suite.sh 6 10         # configs 6..10 only (1-indexed, resume)
set -uo pipefail
cd "$(dirname "$0")"

REPO_ROOT="$(cd ../.. && pwd)"
PYDIR="../python"
LOG="run-baseline-suite.log"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/github}"

# threads accounts gap_ms
CONFIGS=(
  "1 1000 500"  "1 1000 400"  "1 1000 300"  "1 1000 200"  "1 1000 100"
  "2 2000 500"  "2 2000 400"  "2 2000 300"  "2 2000 200"  "2 2000 100"
  "5 5000 500"  "5 5000 400"  "5 5000 300"  "5 5000 200"  "5 5000 100"
  "10 10000 500" "10 10000 400" "10 10000 300" "10 10000 200" "10 10000 100"
)

FROM="${1:-1}"
TO="${2:-${#CONFIGS[@]}}"

log() { echo "[$(date '+%F %T')] $*" | tee -a "$LOG"; }

# `make run-l2` clones git@github.com: repos, so a dead agent or an unloaded
# key fails the deploy with an ssh error rather than anything network-shaped.
# Called before every deploy and again on the retry, because the agent does not
# survive a reboot and the key can be dropped by a keychain timeout.
ensure_ssh() {
  if ! ssh-add -l >/dev/null 2>&1; then
    eval "$(ssh-agent -s)" >>"$LOG" 2>&1
  fi
  ssh-add -l 2>/dev/null | grep -q . || ssh-add "$SSH_KEY" >>"$LOG" 2>&1
}

# `make run-l2` returns once compose is up, which is earlier than the chains
# answering RPC and the publisher exporting metrics. Submitting into that gap
# looks like a chain failure, so wait for the three endpoints the run needs.
wait_ready() {
  local deadline=$((SECONDS + 600))
  while [ $SECONDS -lt $deadline ]; do
    if curl -sf -m 3 -o /dev/null -X POST -H 'content-type: application/json' \
         --data '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' \
         http://127.0.0.1:17545 \
       && curl -sf -m 3 -o /dev/null -X POST -H 'content-type: application/json' \
         --data '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' \
         http://127.0.0.1:27545 \
       && curl -sf -m 3 -o /dev/null http://127.0.0.1:18081/metrics; then
      log "stack ready after ${SECONDS}s"
      return 0
    fi
    sleep 5
  done
  log "!! stack not ready after 600s"
  return 1
}

deploy() {
  ensure_ssh
  (cd "$REPO_ROOT" \
    && make clean-l2 && make clean-observability \
    && make run-l2 && make run-observability && make scripts) >>"$LOG" 2>&1
}

for i in $(seq "$FROM" "$TO"); do
  read -r threads accounts gap <<< "${CONFIGS[$((i - 1))]}"
  label="t${threads}_a${accounts}_gap${gap}ms"
  archive="runs/${label}"
  log "=== [$i/${#CONFIGS[@]}] $label ==="

  if ! deploy || ! wait_ready; then
    log "deploy failed for $label — re-adding ssh key and retrying once"
    ensure_ssh
    if ! deploy || ! wait_ready; then
      log "!! deploy failed twice for $label, skipping"
      continue
    fi
  fi

  # These three files are the only knobs the generator reads; `make scripts`
  # above rewrites the contract addresses in the same files, so patch after it.
  sed -i "s/AMOUNT_ACCOUNTS: [0-9]*/AMOUNT_ACCOUNTS: ${accounts}/" create-accounts.js
  sed -i "s/const numWorkers = [0-9]*;/const numWorkers = ${threads};/" generate-parallel-transactions.js
  sed -i "s/INTERVAL: [0-9]*/INTERVAL: ${gap}/" worker.js

  rm -f xt-submissions-w*.jsonl

  log "creating ${accounts} accounts"
  if ! node create-accounts.js >>"$LOG" 2>&1; then
    log "!! create-accounts.js failed for $label, skipping"
    continue
  fi

  log "marking metrics baseline"
  python3 "$PYDIR/xt-metrics.py" mark >>"$LOG" 2>&1

  log "running stress: ${threads} threads, ${gap}ms gap, 30min"
  start=$SECONDS
  node generate-parallel-transactions.js >>"$LOG" 2>&1
  duration=$((SECONDS - start))

  mkdir -p "$archive"
  log "collecting metrics for $label (${duration}s)"
  python3 "$PYDIR/xt-metrics.py" report --label "$label" \
    --json "$archive/metrics.json" --csv "$PYDIR/results.csv" \
    --meta "threads=${threads}" --meta "accounts=${accounts}" \
    --meta "interval_ms=${gap}" --meta "duration_s=${duration}" 2>&1 | tee -a "$LOG"

  mv xt-submissions-w*.jsonl "$archive/" 2>/dev/null
  cp accounts.json "$archive/accounts.json" 2>/dev/null
  log "=== done $label ==="
done

log "=== suite complete: $(ls runs/*/metrics.json 2>/dev/null | wc -l) runs collected ==="
log "next: python3 $PYDIR/fill-ods.py"