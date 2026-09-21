#!/usr/bin/env bash
# Reads the live L1 Kurtosis enclave (as shown by `make show-l1`) and writes the
# current el-1/cl-1 ports into configs/config.yaml's l1-el-url/l1-cl-url, since
# Kurtosis reassigns random host ports on every `make run-l1`.
set -euo pipefail

ENCLAVE_NAME="${ENCLAVE_NAME:-localnet}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="${CONFIG_FILE:-$REPO_ROOT/configs/config.yaml}"

if ! kurtosis enclave inspect "$ENCLAVE_NAME" >/dev/null 2>&1; then
  echo "error: enclave '$ENCLAVE_NAME' not found or not running (did you run 'make run-l1'?)" >&2
  exit 1
fi

if [[ ! -f "$CONFIG_FILE" ]]; then
  echo "error: config file not found at $CONFIG_FILE" >&2
  exit 1
fi

el_addr="$(kurtosis port print "$ENCLAVE_NAME" el-1-geth-lighthouse rpc --format ip,number)"
cl_addr="$(kurtosis port print "$ENCLAVE_NAME" cl-1-lighthouse-geth http --format ip,number)"

el_url="http://${el_addr}"
cl_url="http://${cl_addr}"

sed -i.bak \
  -e "s#^\(  l1-el-url: \).*#\1${el_url}#" \
  -e "s#^\(  l1-cl-url: \).*#\1${cl_url}#" \
  "$CONFIG_FILE"
rm -f "${CONFIG_FILE}.bak"

echo "l1-el-url -> ${el_url}"
echo "l1-cl-url -> ${cl_url}"
echo "Updated ${CONFIG_FILE}"