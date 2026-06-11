#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROMETHEUS_CONFIG="$REPO_ROOT/configs/prometheus/config.yaml"
PROMETHEUS_DATA_DIR="${HOME}/.local/share/prometheus"
PROMETHEUS_LOG_DIR="$REPO_ROOT/.localnet/logs"
PROMETHEUS_LOG_FILE="$PROMETHEUS_LOG_DIR/prometheus-host.log"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[33m'
CYAN='\033[36m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'

info() { echo -e "  ${DIM}·${NC}  $*"; }
ok() { echo -e "  ${GREEN}✓${NC}  $*"; }
warn() { echo -e "  ${YELLOW}⚠${NC}  ${YELLOW}$*${NC}"; }
die() { echo -e "\n  ${RED}${BOLD}✗  ERROR:${NC}  $*\n" >&2; exit 1; }
section() { echo -e "\n${BOLD}${CYAN}── $* ${NC}"; }

cleanup() {
	if [[ -n "${PROMETHEUS_PID:-}" ]] && kill -0 "$PROMETHEUS_PID" 2>/dev/null; then
		kill "$PROMETHEUS_PID" >/dev/null 2>&1 || true
	fi
}

trap cleanup ERR INT TERM

check_prereqs() {
	local missing=()
	for cmd in docker make prometheus; do
		command -v "$cmd" &>/dev/null || missing+=("$cmd")
	done
	[[ ${#missing[@]} -eq 0 ]] || die "Missing prerequisites: ${missing[*]}"
}

start_prometheus() {
	section "Starting host Prometheus"
	mkdir -p "$PROMETHEUS_DATA_DIR" "$PROMETHEUS_LOG_DIR"
	nohup prometheus \
		--config.file="$PROMETHEUS_CONFIG" \
		--web.listen-address=:9090 \
		--storage.tsdb.path="$PROMETHEUS_DATA_DIR" \
		>"$PROMETHEUS_LOG_FILE" 2>&1 &
	PROMETHEUS_PID=$!
	sleep 1
	kill -0 "$PROMETHEUS_PID" 2>/dev/null || die "Prometheus failed to start. See $PROMETHEUS_LOG_FILE"
	ok "Prometheus is running on http://localhost:9090"
	info "Logs: $PROMETHEUS_LOG_FILE"
}

start_docker_stack() {
	section "Starting Docker observability services"
	LOCALNET_OBSERVABILITY_SKIP_PROMETHEUS=1 make -C "$REPO_ROOT" run-observability
	ok "Docker observability services started"
}

stop_docker_stack() {
    section "Stopping Docker observability services"
    LOCALNET_OBSERVABILITY_SKIP_PROMETHEUS=1 make -C "$REPO_ROOT" clean-observability
    ok "Docker observability services stopped"
}

main() {
	local do_clean=false
  	for arg in "$@"; do
    	[[ "$arg" == "--clean" ]] && do_clean=true
  	done

	if $do_clean; then
        section "Clean"
        cleanup
        stop_docker_stack
    else
        check_prereqs
        start_prometheus
        start_docker_stack
        warn "Prometheus keeps running in the background (PID: $PROMETHEUS_PID)"
    fi
}

main "$@"
