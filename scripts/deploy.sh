#!/usr/bin/env bash
# deploy.sh — full local testnet deployment for a Kurtosis-backed L1.
#
# Usage:
#   ./scripts/deploy.sh          # deploy L2 (start L1 first with make run-l1)
#   ./scripts/deploy.sh --clean  # wipe L2 state and redeploy from scratch
#
# Prerequisites: kurtosis, cast (foundry), docker, go, just, python3

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="$REPO_ROOT/configs/config.yaml"
ENCLAVE_NAME="localnet"

WALLET_ADDRESS="0x006050aCB7E46D4243E43373aD24182754bE3DA0"
WALLET_PK="0x28d962c1dc95c4ba72b03a53bbed92020869e82880126dc537066169ccedcc8a"
# Sender of the canonical pre-signed deterministic-deployer tx (nonce 0 → 0x4e59b...)
PROXY_SENDER="0x3fAB184622Dc19b6109349B94811493BF2a45362"
DETERMINISTIC_DEPLOYER="0x4e59b44847b379578588920ca78fbf26c0b4956c"
# Raw tx: deploys Nick Johnson's CREATE2 factory; signed with v=27, r=s=0x22..22
PROXY_RAW_TX="0xf8a58085174876e800830186a08080b853604580600e600039806000f350fe7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe03601600081602082378035828234f58015156039578182fd5b8082525050506014600cf31ba02222222222222222222222222222222222222222222222222222222222222222a02222222222222222222222222222222222222222222222222222222222222222"
# Kurtosis mnemonic — accounts are pre-funded with ~1 billion ETH in genesis
KURTOSIS_MNEMONIC="giant issue aisle success illegal bike spike question tent bar rely arctic volcano long crawl hungry vocal artwork sniff fantasy very lucky have athlete"

# ─── colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${CYAN}[deploy]${NC} $*"; }
ok()    { echo -e "${GREEN}[deploy]${NC} $*"; }
warn()  { echo -e "${YELLOW}[deploy]${NC} $*"; }
die()   { echo -e "${RED}[deploy] ERROR:${NC} $*" >&2; exit 1; }

# ─── prerequisites ────────────────────────────────────────────────────────────
check_prereqs() {
  local missing=()
  for cmd in kurtosis cast docker go just python3; do
    command -v "$cmd" &>/dev/null || missing+=("$cmd")
  done
  [[ ${#missing[@]} -eq 0 ]] || die "Missing prerequisites: ${missing[*]}"
  ok "All prerequisites found."
}

# ─── Docker socket (macOS Docker Desktop uses a non-standard path) ─────────────
setup_docker() {
  if [[ -S "$HOME/.docker/run/docker.sock" ]]; then
    export DOCKER_HOST="unix://$HOME/.docker/run/docker.sock"
    info "Using DOCKER_HOST=$DOCKER_HOST"
  elif [[ -S /var/run/docker.sock ]]; then
    export DOCKER_HOST="unix:///var/run/docker.sock"
    info "Using DOCKER_HOST=$DOCKER_HOST"
  else
    die "Docker socket not found. Is Docker Desktop running?"
  fi
  docker info &>/dev/null || die "Cannot connect to Docker daemon."
}

# ─── Kurtosis L1 ──────────────────────────────────────────────────────────────
get_l1_ports() {
  info "Fetching L1 ports from Kurtosis enclave '$ENCLAVE_NAME'..."

  local inspect
  inspect=$(kurtosis enclave inspect "$ENCLAVE_NAME" 2>/dev/null) \
    || die "Kurtosis enclave '$ENCLAVE_NAME' not found. Run 'make run-l1' first."

  # EL RPC: ports appear on their own indented lines (separate from the service name line)
  L1_EL_PORT=$(echo "$inspect" \
    | grep -E 'rpc: 8545/tcp -> 127\.0\.0\.1:' \
    | head -1 \
    | grep -oE '127\.0\.0\.1:[0-9]+' \
    | cut -d: -f2)

  # CL HTTP: port is on the cl-1-lighthouse-geth line; URL may have http:// prefix
  L1_CL_PORT=$(echo "$inspect" \
    | grep "cl-1-lighthouse-geth" \
    | grep "4000/tcp" \
    | grep -oE '127\.0\.0\.1:[0-9]+' \
    | cut -d: -f2 \
    | head -1)

  [[ -n "$L1_EL_PORT" ]] || die "Could not find EL port (8545). Is the enclave healthy?"
  [[ -n "$L1_CL_PORT" ]] || die "Could not find CL port (4000). Is the enclave healthy?"

  L1_EL_URL="http://127.0.0.1:$L1_EL_PORT"
  L1_CL_URL="http://127.0.0.1:$L1_CL_PORT"

  ok "L1 EL: $L1_EL_URL"
  ok "L1 CL: $L1_CL_URL"
}

verify_l1() {
  info "Verifying L1 is responsive..."
  local chain_id
  chain_id=$(cast chain-id --rpc-url "$L1_EL_URL" 2>/dev/null) \
    || die "L1 RPC at $L1_EL_URL is not responding."
  [[ "$chain_id" == "3151908" ]] \
    || warn "Unexpected chain ID: $chain_id (expected 3151908)"
  ok "L1 chain ID: $chain_id"
}

# ─── config.yaml ──────────────────────────────────────────────────────────────
update_config() {
  info "Updating config.yaml with current L1 ports..."

  # Replace FILL_EL_PORT / FILL_CL_PORT placeholders AND previously set ports
  python3 - "$CONFIG_FILE" "$L1_EL_PORT" "$L1_CL_PORT" <<'PYEOF'
import sys, re

path, el_port, cl_port = sys.argv[1], sys.argv[2], sys.argv[3]
text = open(path).read()

# Replace l1-el-url line
text = re.sub(
    r'(l1-el-url:\s*)http://host\.docker\.internal:\S+',
    f'l1-el-url: http://host.docker.internal:{el_port}',
    text
)
# Replace l1-cl-url line
text = re.sub(
    r'(l1-cl-url:\s*)http://host\.docker\.internal:\S+',
    f'l1-cl-url: http://host.docker.internal:{cl_port}',
    text
)

open(path, 'w').write(text)
print(f"  l1-el-url -> host.docker.internal:{el_port}")
print(f"  l1-cl-url -> host.docker.internal:{cl_port}")
PYEOF

  ok "config.yaml updated."
}

# ─── l1-chainconfig.json ──────────────────────────────────────────────────────
# op-node v1.16+ rejects terminalTotalDifficultyPassed from Kurtosis genesis.
# We generate a filtered version with only the fields op-node accepts.
generate_l1_chainconfig() {
  info "Generating l1-chainconfig.json for both rollups..."

  # Try to get the exact genesis config from the Kurtosis geth container.
  # If that fails, fall back to a known-good hardcoded version.
  local chain_config=""

  local geth_container
  geth_container=$(docker ps --format "{{.Names}}" \
    | grep "el-1-geth-lighthouse" | head -1 2>/dev/null || true)

  if [[ -n "$geth_container" ]]; then
    info "Reading genesis config from container: $geth_container"
    # Kurtosis places genesis at /network-configs/genesis.json
    chain_config=$(docker exec "$geth_container" \
      cat /network-configs/genesis.json 2>/dev/null \
      | python3 -c "
import json, sys
try:
    g = json.load(sys.stdin)
    c = g.get('config', {})
    # Remove the field op-node v1.16 rejects
    c.pop('terminalTotalDifficultyPassed', None)
    print(json.dumps(c, indent=2))
except Exception:
    pass
" 2>/dev/null || true)
  fi

  if [[ -z "$chain_config" ]]; then
    warn "Could not read genesis from container. Using hardcoded Kurtosis chain config."
    # blobSchedule is required by op-node v1.16+ when Prague is active.
    chain_config='{
  "chainId": 3151908,
  "homesteadBlock": 0,
  "eip150Block": 0,
  "eip155Block": 0,
  "eip158Block": 0,
  "byzantiumBlock": 0,
  "constantinopleBlock": 0,
  "petersburgBlock": 0,
  "istanbulBlock": 0,
  "berlinBlock": 0,
  "londonBlock": 0,
  "mergeNetsplitBlock": 0,
  "terminalTotalDifficulty": 0,
  "shanghaiTime": 0,
  "cancunTime": 0,
  "pragueTime": 0,
  "blobSchedule": {
    "cancun": {"target": 3, "max": 6, "baseFeeUpdateFraction": 3338477},
    "prague": {"target": 6, "max": 9, "baseFeeUpdateFraction": 5007716}
  },
  "depositContractAddress": "0x00000000219ab540356cBB839Cbe05303d7705Fa"
}'
  fi

  for chain in rollup-a rollup-b; do
    local dir="$REPO_ROOT/.localnet/networks/$chain"
    mkdir -p "$dir"
    printf '%s' "$chain_config" > "$dir/l1-chainconfig.json"
    ok "Wrote $dir/l1-chainconfig.json"
  done
}

# ─── Deterministic Deployment Proxy ───────────────────────────────────────────
deploy_proxy() {
  info "Checking for Deterministic Deployment Proxy at $DETERMINISTIC_DEPLOYER..."

  local code
  code=$(cast code "$DETERMINISTIC_DEPLOYER" --rpc-url "$L1_EL_URL" 2>/dev/null || true)

  if [[ "$code" != "0x" && -n "$code" ]]; then
    ok "Proxy already deployed. Skipping."
    return
  fi

  info "Proxy not found. Deploying..."

  # Fund the proxy sender from Kurtosis mnemonic account 0
  local funder_pk
  funder_pk=$(cast wallet private-key \
    --mnemonic "$KURTOSIS_MNEMONIC" \
    --mnemonic-index 0 2>/dev/null) \
    || die "Failed to derive Kurtosis mnemonic key. Is 'cast' (foundry) installed?"

  local sender_balance
  sender_balance=$(cast balance "$PROXY_SENDER" --rpc-url "$L1_EL_URL" 2>/dev/null || echo "0")

  if [[ "$sender_balance" == "0" || "$sender_balance" == "0x0" ]]; then
    info "Funding proxy sender $PROXY_SENDER with 0.1 ETH..."
    cast send \
      --private-key "$funder_pk" \
      --rpc-url "$L1_EL_URL" \
      "$PROXY_SENDER" \
      --value 100000000000000000 \
      --quiet
    ok "Proxy sender funded."
  fi

  info "Broadcasting pre-signed proxy deployment tx..."
  cast publish --rpc-url "$L1_EL_URL" "$PROXY_RAW_TX" >/dev/null
  ok "Deterministic Deployment Proxy deployed at $DETERMINISTIC_DEPLOYER"
}

# ─── Deployer wallet funding ───────────────────────────────────────────────────
fund_wallet() {
  info "Checking deployer wallet balance ($WALLET_ADDRESS)..."

  local balance
  balance=$(cast balance "$WALLET_ADDRESS" --ether --rpc-url "$L1_EL_URL" 2>/dev/null || echo "0")
  # Check if balance is less than 10 ETH (simple string comparison for 0 or small values)
  local balance_wei
  balance_wei=$(cast balance "$WALLET_ADDRESS" --rpc-url "$L1_EL_URL" 2>/dev/null || echo "0")

  # Fund if balance is below 10 ETH (10000000000000000000 wei)
  if python3 -c "import sys; sys.exit(0 if int('$balance_wei') < 10**19 else 1)" 2>/dev/null; then
    info "Funding deployer wallet with 1000 ETH from Kurtosis mnemonic..."
    local funder_pk
    funder_pk=$(cast wallet private-key \
      --mnemonic "$KURTOSIS_MNEMONIC" \
      --mnemonic-index 0 2>/dev/null) \
      || die "Failed to derive Kurtosis mnemonic key."
    cast send \
      --private-key "$funder_pk" \
      --rpc-url "$L1_EL_URL" \
      "$WALLET_ADDRESS" \
      --value 1000ether \
      --quiet
    ok "Deployer wallet funded."
  else
    ok "Deployer wallet has sufficient balance ($balance ETH)."
  fi
}

# ─── Build ─────────────────────────────────────────────────────────────────────
build_binary() {
  info "Building localnet binary..."
  cd "$REPO_ROOT"
  go build -o ./cmd/localnet/bin/localnet ./cmd/localnet \
    || die "Build failed."
  cp "$CONFIG_FILE" ./cmd/localnet/bin/config.yaml
  ok "Binary built: cmd/localnet/bin/localnet"
}

# ─── Clean (optional) ─────────────────────────────────────────────────────────
clean_l2() {
  warn "Cleaning L2 state (.localnet/state, .localnet/networks, Docker volumes)..."
  rm -rf "$REPO_ROOT/.localnet/state" "$REPO_ROOT/.localnet/networks"
  # Stop and remove L2 containers FIRST so volumes are no longer in use
  docker ps -a --filter "label=stack=localnet-l2" --format "{{.Names}}" \
    | xargs -r docker rm -f \
    || true
  docker volume ls --format "{{.Name}}" \
    | grep -E "^localnet_" \
    | xargs -r docker volume rm \
    || true
  ok "L2 state cleaned."
}

# ─── L2 launch ────────────────────────────────────────────────────────────────
run_l2() {
  info "Starting L2 deployment (this takes a few minutes)..."
  cd "$REPO_ROOT"
  DOCKER_HOST="$DOCKER_HOST" \
    ./cmd/localnet/bin/localnet l2 2>&1 | tee /tmp/localnet-l2-deploy.log
  ok "L2 deployment completed."
}

# ─── Post-launch check ─────────────────────────────────────────────────────────
wait_for_l2() {
  info "Waiting for L2 chains to produce blocks..."
  local attempt=0
  local max=30

  for chain_port in 18545 28545; do
    attempt=0
    while [[ $attempt -lt $max ]]; do
      local bn
      bn=$(cast block-number --rpc-url "http://127.0.0.1:$chain_port" 2>/dev/null || echo "")
      if [[ -n "$bn" && "$bn" -gt 0 ]]; then
        ok "Chain on port $chain_port is producing blocks (block $bn)."
        break
      fi
      sleep 3
      ((attempt++))
    done
    [[ $attempt -lt $max ]] || warn "Chain on port $chain_port did not produce blocks within timeout."
  done
}

print_summary() {
  echo ""
  echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
  echo -e "${GREEN}  Local testnet is running!${NC}"
  echo -e "${GREEN}═══════════════════════════════════════════════════${NC}"
  echo ""
  echo "  L1 (Kurtosis devnet)"
  echo "    EL RPC:           $L1_EL_URL"
  echo "    CL HTTP:          $L1_CL_URL"
  echo ""
  echo "  L2 Rollup-A (chain 77777)"
  echo "    Standard RPC:     http://127.0.0.1:18545"
  echo "    Flashblocks RPC:  http://127.0.0.1:17545"
  echo "    Sidecar API:      http://127.0.0.1:17090"
  echo ""
  echo "  L2 Rollup-B (chain 88888)"
  echo "    Standard RPC:     http://127.0.0.1:28545"
  echo "    Flashblocks RPC:  http://127.0.0.1:27545"
  echo "    Sidecar API:      http://127.0.0.1:27090"
  echo ""
  echo "  Publisher:          http://127.0.0.1:18080"
  echo ""
  echo "  Useful commands:"
  echo "    make show-l2       # list running containers"
  echo "    make stop-l2       # stop L2 stack"
  echo "    make clean-l2      # wipe L2 state"
  echo ""
}

# ─── main ─────────────────────────────────────────────────────────────────────
main() {
  local do_clean=false
  for arg in "$@"; do
    [[ "$arg" == "--clean" ]] && do_clean=true
  done

  check_prereqs
  setup_docker
  get_l1_ports
  verify_l1
  update_config

  if $do_clean; then
    clean_l2
  fi

  generate_l1_chainconfig
  deploy_proxy
  fund_wallet
  build_binary
  run_l2
  wait_for_l2
  print_summary
}

main "$@"
