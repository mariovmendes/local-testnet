#!/usr/bin/env bash
# deploy.sh — full local testnet deployment for a Kurtosis-backed L1.
#
# Usage:
#   ./scripts/deploy.sh          # deploy L2 (start L1 first with make run-l1)
#   ./scripts/deploy.sh --clean  # wipe L2 state and redeploy from scratch
#
# Prerequisites: kurtosis, cast (foundry), docker, go, just, python3
# op-deployer and op-reth are extracted automatically into .localnet/bin/ if not already present.

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

  # Replace any existing port in l1-el-url / l1-cl-url lines with the current port
  python3 - "$CONFIG_FILE" "$L1_EL_PORT" "$L1_CL_PORT" <<'PYEOF'
import sys, re

path, el_port, cl_port = sys.argv[1], sys.argv[2], sys.argv[3]
text = open(path).read()

# Replace l1-el-url line (any host/port)
text = re.sub(
    r'(l1-el-url:\s*)\S+',
    f'l1-el-url: http://127.0.0.1:{el_port}',
    text
)
# Replace l1-cl-url line (any host/port)
text = re.sub(
    r'(l1-cl-url:\s*)\S+',
    f'l1-cl-url: http://127.0.0.1:{cl_port}',
    text
)

open(path, 'w').write(text)
print(f"  l1-el-url -> 127.0.0.1:{el_port}")
print(f"  l1-cl-url -> 127.0.0.1:{cl_port}")
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

# ─── Native binaries ──────────────────────────────────────────────────────────
# Resolves all five OP-stack binaries into .localnet/bin/.
#
#  macOS : op-deployer and op-reth are downloaded from GitHub releases;
#          op-node / op-batcher / op-proposer have no pre-built macOS releases
#          and are compiled from the optimism monorepo (~5 min, first run only).
#  Linux : all five are extracted from Docker images (original behaviour).
#
# All helpers skip silently if the binary already exists or is found on PATH.

read_image_tag() {
  local key="$1" default="$2"
  python3 -c "
try:
    import yaml
    cfg = yaml.safe_load(open('${CONFIG_FILE}'))
    print(cfg['l2']['images']['${key}']['tag'])
except Exception:
    print('${default}')
" 2>/dev/null || echo "$default"
}

# Linux: pull one binary out of a Docker image.
extract_binary() {
  local bin_name="$1"
  local image="$2"
  local container_path="$3"
  local dest="$REPO_ROOT/.localnet/bin/$bin_name"

  if [[ -x "$dest" ]]; then
    ok "$bin_name already in .localnet/bin, skipping."; return
  fi
  if command -v "$bin_name" &>/dev/null; then
    ok "$bin_name found in PATH ($(command -v "$bin_name")), skipping."; return
  fi
  info "Extracting $bin_name from $image ..."
  local cid
  cid=$(docker create "$image" sh 2>/dev/null) \
    || die "Failed to create container from $image. Is the image pullable?"
  docker cp "$cid:$container_path" "$dest" \
    || { docker rm -f "$cid" >/dev/null 2>&1; die "Failed to copy $bin_name from $image"; }
  docker rm -f "$cid" >/dev/null 2>&1
  chmod +x "$dest"
  ok "Extracted $bin_name -> $dest"
}

# macOS: download a GitHub release tarball and place one binary in .localnet/bin/.
# bin_name   — name to save as (e.g. "op-reth" even if tarball contains "reth")
# url        — tarball download URL
# bin_in_tar — filename to search for inside the archive (default: bin_name)
download_github_binary() {
  local bin_name="$1"
  local url="$2"
  local bin_in_tar="${3:-$bin_name}"
  local dest="$REPO_ROOT/.localnet/bin/$bin_name"

  if [[ -x "$dest" ]]; then
    ok "$bin_name already in .localnet/bin, skipping."; return
  fi
  if command -v "$bin_name" &>/dev/null; then
    ok "$bin_name found in PATH ($(command -v "$bin_name")), skipping."; return
  fi

  info "Downloading $bin_name ..."
  local tmpdir
  tmpdir=$(mktemp -d)
  curl -fsSL "$url" -o "$tmpdir/archive.tar.gz" \
    || { rm -rf "$tmpdir"; die "Failed to download $bin_name from $url"; }
  tar xzf "$tmpdir/archive.tar.gz" -C "$tmpdir" \
    || { rm -rf "$tmpdir"; die "Failed to extract archive for $bin_name"; }
  local bin_path
  bin_path=$(find "$tmpdir" -name "$bin_in_tar" -type f | head -1)
  if [[ -z "$bin_path" ]]; then
    rm -rf "$tmpdir"
    die "Binary '$bin_in_tar' not found in archive from $url"
  fi
  cp "$bin_path" "$dest"
  chmod +x "$dest"
  rm -rf "$tmpdir"
  ok "Downloaded $bin_name -> $dest"
}

# macOS: build op-node, op-batcher, op-proposer from the optimism monorepo.
# These tools ship only as Linux Docker images; no pre-built macOS binaries exist.
#
# Clones are cached in .localnet/build/ and reused across clean runs so
# the 5-minute clone+build only happens once per version.
build_op_tools_macos() {
  local dest_dir="$REPO_ROOT/.localnet/bin"
  local build_dir="$REPO_ROOT/.localnet/build"
  mkdir -p "$build_dir"

  local node_ver batcher_ver proposer_ver
  node_ver=$(read_image_tag op-node 1.16.2);         node_ver="${node_ver#v}"
  batcher_ver=$(read_image_tag op-batcher 1.16.2);   batcher_ver="${batcher_ver#v}"
  proposer_ver=$(read_image_tag op-proposer 1.10.0); proposer_ver="${proposer_ver#v}"

  local need_node=false need_batcher=false need_proposer=false
  [[ ! -x "$dest_dir/op-node"     ]] && ! command -v op-node     &>/dev/null && need_node=true
  [[ ! -x "$dest_dir/op-batcher"  ]] && ! command -v op-batcher  &>/dev/null && need_batcher=true
  [[ ! -x "$dest_dir/op-proposer" ]] && ! command -v op-proposer &>/dev/null && need_proposer=true

  if ! $need_node && ! $need_batcher && ! $need_proposer; then
    ok "op-node, op-batcher, op-proposer already present, skipping build."; return
  fi

  # op-node and op-batcher share the monorepo tag; clone once for both.
  if $need_node || $need_batcher; then
    local src_node="$build_dir/optimism-op-node-v${node_ver}"
    if [[ ! -d "$src_node" ]]; then
      warn "Cloning optimism@op-node/v${node_ver} into .localnet/build/ (first run — ~5 min)..."
      git clone --depth=1 --branch "op-node/v${node_ver}" \
        https://github.com/ethereum-optimism/optimism.git "$src_node" \
        || { rm -rf "$src_node"; die "Failed to clone optimism at op-node/v${node_ver}"; }
      ok "Cloned optimism source -> $src_node (cached for future runs)"
    else
      info "Using cached optimism source at $src_node"
    fi

    if $need_node; then
      info "Building op-node..."
      (cd "$src_node" && go build -o "$dest_dir/op-node" ./op-node/cmd) \
        || die "Failed to build op-node"
      chmod +x "$dest_dir/op-node"
      ok "Built op-node -> $dest_dir/op-node"
    fi
    if $need_batcher; then
      info "Building op-batcher..."
      (cd "$src_node" && go build -o "$dest_dir/op-batcher" ./op-batcher/cmd) \
        || die "Failed to build op-batcher"
      chmod +x "$dest_dir/op-batcher"
      ok "Built op-batcher -> $dest_dir/op-batcher"
    fi
  fi

  # op-proposer may be at a different version tag.
  if $need_proposer; then
    local src_proposer="$build_dir/optimism-op-proposer-v${proposer_ver}"
    if [[ ! -d "$src_proposer" ]]; then
      warn "Cloning optimism@op-proposer/v${proposer_ver} into .localnet/build/ (first run — ~3 min)..."
      git clone --depth=1 --branch "op-proposer/v${proposer_ver}" \
        https://github.com/ethereum-optimism/optimism.git "$src_proposer" \
        || { rm -rf "$src_proposer"; die "Failed to clone optimism at op-proposer/v${proposer_ver}"; }
      ok "Cloned optimism source -> $src_proposer (cached for future runs)"
    else
      info "Using cached optimism source at $src_proposer"
    fi

    info "Building op-proposer..."
    (cd "$src_proposer" && go build -o "$dest_dir/op-proposer" ./op-proposer/cmd) \
      || die "Failed to build op-proposer"
    chmod +x "$dest_dir/op-proposer"
    ok "Built op-proposer -> $dest_dir/op-proposer"
  fi
}

# Build publisher and sidecar from Rust source when Sidecar is enabled.
# Both are in already-cloned repos under .localnet/services/.
build_sidecar_binaries() {
  local dest_dir="$REPO_ROOT/.localnet/bin"
  local services_dir="$REPO_ROOT/.localnet/services"

  # ── publisher ──────────────────────────────────────────────────────────────
  local pub_src="$services_dir/publisher"
  if [[ ! -x "$dest_dir/publisher" ]]; then
    if [[ ! -d "$pub_src" ]]; then
      die "publisher source not found at $pub_src (run make deploy once with sidecar disabled first to clone repos)"
    fi
    warn "Building publisher from source (~5-10 min, cached after first build) ..."
    (cd "$pub_src" && cargo build --locked --release --bin publisher 2>&1) \
      || die "Failed to build publisher"
    cp "$pub_src/target/release/publisher" "$dest_dir/publisher"
    chmod +x "$dest_dir/publisher"
    ok "Built publisher -> $dest_dir/publisher"
  else
    ok "publisher already in .localnet/bin, skipping build."
  fi

  # ── sidecar ────────────────────────────────────────────────────────────────
  local sc_src="$services_dir/sidecar"
  if [[ ! -x "$dest_dir/sidecar" ]]; then
    if [[ ! -d "$sc_src" ]]; then
      die "sidecar source not found at $sc_src"
    fi
    warn "Building sidecar from source (~5-10 min, cached after first build) ..."
    (cd "$sc_src" && cargo build --locked --release --bin sidecar 2>&1) \
      || die "Failed to build sidecar"
    cp "$sc_src/target/release/sidecar" "$dest_dir/sidecar"
    chmod +x "$dest_dir/sidecar"
    ok "Built sidecar -> $dest_dir/sidecar"
  else
    ok "sidecar already in .localnet/bin, skipping build."
  fi
}

# Build op-rbuilder and rollup-boost from Rust source when Flashblocks is enabled.
# Both are Rust projects; cargo build is used on all platforms.
# Builds are cached: binaries are only rebuilt when not already in .localnet/bin/.
build_flashblocks_binaries() {
  local dest_dir="$REPO_ROOT/.localnet/bin"
  local build_dir="$REPO_ROOT/.localnet/build"
  local services_dir="$REPO_ROOT/.localnet/services"
  mkdir -p "$build_dir"

  # ── op-rbuilder ──────────────────────────────────────────────────────────
  # Source is already cloned to .localnet/services/op-rbuilder by the localnet
  # binary (service.go clones it when flashblocks.enabled=true). If not present
  # yet (first deploy before service.go has run), clone it here.
  local rbuilder_src="$services_dir/op-rbuilder"
  if [[ ! -x "$dest_dir/op-rbuilder" ]]; then
    if [[ ! -d "$rbuilder_src" ]]; then
      local rbuilder_url rbuilder_branch
      rbuilder_url=$(python3 -c "
import yaml
cfg = yaml.safe_load(open('$CONFIG_FILE'))
print(cfg.get('l2',{}).get('repositories',{}).get('op-rbuilder',{}).get('url','git@github.com:ethera-labs/op-rbuilder.git'))
" 2>/dev/null || echo "git@github.com:ethera-labs/op-rbuilder.git")
      rbuilder_branch=$(python3 -c "
import yaml
cfg = yaml.safe_load(open('$CONFIG_FILE'))
print(cfg.get('l2',{}).get('repositories',{}).get('op-rbuilder',{}).get('branch','stage'))
" 2>/dev/null || echo "stage")
      warn "Cloning op-rbuilder@${rbuilder_branch} (first run) ..."
      mkdir -p "$services_dir"
      git clone --depth=1 --branch "$rbuilder_branch" "$rbuilder_url" "$rbuilder_src" \
        || die "Failed to clone op-rbuilder from $rbuilder_url"
    fi
    warn "Building op-rbuilder from source (~10-20 min, cached after first build) ..."
    (cd "$rbuilder_src" && cargo build --release -p op-rbuilder --bin op-rbuilder 2>&1) \
      || die "Failed to build op-rbuilder"
    cp "$rbuilder_src/target/release/op-rbuilder" "$dest_dir/op-rbuilder"
    chmod +x "$dest_dir/op-rbuilder"
    ok "Built op-rbuilder -> $dest_dir/op-rbuilder"
  else
    ok "op-rbuilder already in .localnet/bin, skipping build."
  fi

  # ── rollup-boost ──────────────────────────────────────────────────────────
  # rollup-boost is a Rust project from github.com/flashbots/rollup-boost.
  # No macOS pre-built binaries exist; must build from source.
  local rb_ver="0.7.15"
  local rb_src="$build_dir/rollup-boost-v${rb_ver}"
  if [[ ! -x "$dest_dir/rollup-boost" ]]; then
    if [[ ! -d "$rb_src" ]]; then
      warn "Cloning rollup-boost v${rb_ver} (first run) ..."
      git clone --depth=1 --branch "rollup-boost/v${rb_ver}" \
        https://github.com/flashbots/rollup-boost.git "$rb_src" \
        || die "Failed to clone rollup-boost v${rb_ver}"
      ok "Cloned rollup-boost -> $rb_src (cached for future runs)"
    else
      info "Using cached rollup-boost source at $rb_src"
    fi
    warn "Building rollup-boost from source (~5-10 min, cached after first build) ..."
    (cd "$rb_src" && cargo build --release --bin rollup-boost 2>&1) \
      || die "Failed to build rollup-boost"
    cp "$rb_src/target/release/rollup-boost" "$dest_dir/rollup-boost"
    chmod +x "$dest_dir/rollup-boost"
    ok "Built rollup-boost -> $dest_dir/rollup-boost"
  else
    ok "rollup-boost already in .localnet/bin, skipping build."
  fi
}

setup_binaries() {
  mkdir -p "$REPO_ROOT/.localnet/bin"

  local os_type arch
  os_type=$(uname -s)
  arch=$(uname -m)

  if [[ "$os_type" == "Darwin" ]]; then
    # Determine arch suffixes for GitHub release filenames.
    local go_arch="amd64"
    local reth_arch="x86_64-apple-darwin"
    if [[ "$arch" == "arm64" ]]; then
      go_arch="arm64"
      reth_arch="aarch64-apple-darwin"
    fi

    local deployer_ver
    deployer_ver=$(read_image_tag op-deployer 0.4.5)
    deployer_ver="${deployer_ver#v}"

    # op-reth v1.10.2 is the last version with a macOS release tarball.
    # v1.11.x only ships as a Linux Docker image (no macOS binary).
    # v1.10.2 is fully compatible with op-node v1.16.2 for sequencing.
    local reth_ver="1.10.2"

    download_github_binary "op-deployer" \
      "https://github.com/ethereum-optimism/optimism/releases/download/op-deployer/v${deployer_ver}/op-deployer-${deployer_ver}-darwin-${go_arch}.tar.gz" \
      "op-deployer"

    download_github_binary "op-reth" \
      "https://github.com/paradigmxyz/reth/releases/download/v${reth_ver}/op-reth-v${reth_ver}-${reth_arch}.tar.gz" \
      "op-reth"

    build_op_tools_macos
  else
    local registry="us-docker.pkg.dev/oplabs-tools-artifacts/images"
    extract_binary "op-deployer" "$registry/op-deployer:$(read_image_tag op-deployer v0.4.5)"  "/usr/local/bin/op-deployer"
    extract_binary "op-reth"     "$registry/op-reth:$(read_image_tag     op-reth     v1.11.5)" "/usr/local/bin/op-reth"
    extract_binary "op-node"     "$registry/op-node:$(read_image_tag     op-node     v1.16.2)" "/usr/local/bin/op-node"
    extract_binary "op-batcher"  "$registry/op-batcher:$(read_image_tag  op-batcher  v1.16.2)" "/usr/local/bin/op-batcher"
    extract_binary "op-proposer" "$registry/op-proposer:$(read_image_tag op-proposer v1.10.0)" "/usr/local/bin/op-proposer"
  fi

  # Build Sidecar binaries (publisher + sidecar) from Rust source when sidecar enabled.
  local sidecar_enabled
  sidecar_enabled=$(python3 -c "
import yaml
cfg = yaml.safe_load(open('$CONFIG_FILE'))
print(str(cfg.get('l2',{}).get('sidecar',{}).get('enabled',False)).lower())
" 2>/dev/null || echo "false")
  if [[ "$sidecar_enabled" == "true" ]]; then
    build_sidecar_binaries
  fi

  # Build Flashblocks binaries (op-rbuilder + rollup-boost) from Rust source
  # when Flashblocks is enabled. Runs on all platforms (macOS and Linux).
  local flashblocks_enabled
  flashblocks_enabled=$(python3 -c "
import yaml
cfg = yaml.safe_load(open('$CONFIG_FILE'))
print(str(cfg.get('l2',{}).get('flashblocks',{}).get('enabled',False)).lower())
" 2>/dev/null || echo "false")
  if [[ "$flashblocks_enabled" == "true" ]]; then
    build_flashblocks_binaries
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

# ─── Stop any running L2 processes (always safe to call) ──────────────────────
stop_l2_procs() {
  warn "Stopping any running L2 native processes..."
  # SIGTERM first, then SIGKILL to guarantee MDBX lock release before new start.
  pkill    -f "npm run dev" 2>/dev/null || true
  pkill    -f "vite"        2>/dev/null || true
  pkill    -f sidecar       2>/dev/null || true
  pkill    -f publisher     2>/dev/null || true
  pkill    -f rollup-boost  2>/dev/null || true
  pkill    -f op-rbuilder   2>/dev/null || true
  pkill    -f op-proposer   2>/dev/null || true
  pkill    -f op-batcher    2>/dev/null || true
  pkill    -f op-node       2>/dev/null || true
  pkill    -f op-reth       2>/dev/null || true
  sleep 1
  pkill -9 -f "npm run dev" 2>/dev/null || true
  pkill -9 -f "vite"        2>/dev/null || true
  pkill -9 -f sidecar       2>/dev/null || true
  pkill -9 -f publisher     2>/dev/null || true
  pkill -9 -f rollup-boost  2>/dev/null || true
  pkill -9 -f op-rbuilder   2>/dev/null || true
  pkill -9 -f op-proposer   2>/dev/null || true
  pkill -9 -f op-batcher    2>/dev/null || true
  pkill -9 -f op-node       2>/dev/null || true
  pkill -9 -f op-reth       2>/dev/null || true
  sleep 1  # let the kernel release file locks after SIGKILL
  # Also stop any lingering Flashblocks Docker containers that may have been
  # left by an earlier Docker-based deployment.
  docker rm -f rollup-boost-a rollup-boost-b op-rbuilder-a op-rbuilder-b 2>/dev/null || true
  ok "L2 processes stopped."
}

# ─── Clean (optional) ─────────────────────────────────────────────────────────
clean_l2() {
  warn "Stopping any running L2 native processes..."
  stop_l2_procs

  warn "Cleaning L2 state (.localnet/state, .localnet/networks, .localnet/data, .localnet/logs)..."
  rm -rf \
    "$REPO_ROOT/.localnet/state" \
    "$REPO_ROOT/.localnet/networks" \
    "$REPO_ROOT/.localnet/data" \
    "$REPO_ROOT/.localnet/logs"
  ok "L2 state cleaned. (binaries in .localnet/bin/ and build cache in .localnet/build/ are preserved)"
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

  setup_binaries
  generate_l1_chainconfig
  deploy_proxy
  fund_wallet
  build_binary
  stop_l2_procs
  run_l2
  wait_for_l2
  print_summary
}

main "$@"
