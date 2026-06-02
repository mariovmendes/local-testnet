# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Overview

Localnet Control Plane — CLI + shell tooling that brings up a full L1+L2
Ethereum testnet on a single macOS machine **without Docker for the L2
runtime**.

- **L1** (`internal/l1`): Kurtosis enclave via `github.com/ssvlabs/ssv-mini`
  Starlark package. Uses Docker for the Kurtosis containers (geth, lighthouse,
  SSV). A custom terminal UI renders Kurtosis progress events as colored output
  (see `internal/l1/service.go`).
- **L2** (`internal/l2`): Two OP Stack rollups (rollup-a / rollup-b) running
  **entirely as native macOS processes** — no Docker. See §Native L2 Stack below.
- **Observability** (`internal/observability`): Optional Grafana/Prometheus/Loki
  stack (still Docker-based).

---

## Quick start

```bash
kurtosis engine start
make run-l1          # starts Kurtosis L1 devnet (~2 min)
make deploy-clean    # wipes state and deploys full L2 stack (~3–30 min first run)
```

### First-run binary build times
All built binaries are cached in `.localnet/bin/` and `.localnet/build/`.

| Binary | Source | Build time |
|--------|--------|-----------|
| op-deployer | GitHub release | ~10s (download) |
| op-reth | GitHub release | ~10s (download) |
| op-node / op-batcher / op-proposer | Go build from optimism monorepo | ~5 min first run |
| op-rbuilder | Rust build from `services/op-rbuilder` | ~15–20 min first run |
| rollup-boost | Rust build from `build/rollup-boost-v0.7.15` | ~5 min first run |
| publisher | Rust build from `services/publisher` | ~5–10 min first run |
| sidecar | Rust build from `services/sidecar` | ~5–10 min first run |
| Expedition (explorer) | npm build from `build/expedition` | ~2 min first run |

---

## Make targets

```
make run-l1          # start Kurtosis L1 devnet (requires Docker)
make deploy          # deploy L2 with existing state (keep chain data)
make deploy-clean    # wipe all L2 state and redeploy from scratch
make clean           # stop everything and remove all state
make stop            # stop all services
make build           # compile the localnet Go binary only
make test            # go test ./...
make lint            # golangci-lint run
```

---

## Native L2 Stack

The L2 runtime uses **native OS processes** supervised by `internal/l2/infra/supervisor`.
No Docker is required after binaries are built.

### Process map (18 total per full deploy)

| Process | Ports | Notes |
|---------|-------|-------|
| op-reth-a/b | 18545/28545 HTTP, 18551/28551 Engine | Canonical execution layer |
| op-rbuilder-a/b | 17545/27545 HTTP, 17552/27552 Engine, 17111/27111 WS | Flashblocks block builder |
| rollup-boost-a/b | 17551/27551 Engine | Engine API multiplexer (op-node → rbuilder + reth) |
| op-node-a/b | 19545/29545 RPC | Sequencer (connects to rollup-boost) |
| op-batcher-a/b | 18548/28548 RPC | Posts batches to L1 (connects to op-rbuilder HTTP) |
| op-proposer-a/b | 18560/28560 RPC | Dispute game proposals |
| publisher | 18080 QUIC, 18081 HTTP | 2PC coordinator for cross-chain XTs |
| sidecar-a/b | 17090/27090 HTTP | Cross-chain coordination layer |
| frontend | 3000 | Ethera Labs Console (Vite dev server) |
| explorer-a/b | 5100/5200 | Expedition block explorer (npx serve) |

### Supervisor (`internal/l2/infra/supervisor`)

- Processes are started with `context.Background()` so they **survive the
  deploy binary exiting** (they become OS orphans kept alive by `Setsid`).
- Stdout/stderr are redirected **directly to log files** (no pipe), so no EPIPE
  kills processes when the parent exits.
- A file-tailing goroutine reads the log file and forwards colored output to
  the terminal; this goroutine dying on parent exit doesn't affect the child.
- `stop_l2_procs` in `scripts/deploy.sh` kills all processes by their full
  binary path (`$REPO_ROOT/.localnet/bin/<name>`) so unrelated system
  processes with the same name are never killed.
- Frontend and explorer are killed by **port** (not name) to avoid collateral
  damage to unrelated npm/node processes.

### Log filtering (`internal/logfilter`)

External subprocess output is filtered before being shown:
- `NewDeployerWriter`: prettifies `t=... lvl=... msg=...` go-ethereum format;
  suppresses callframe/Fault/Revert chain-assertion noise.
- `NewForgeWriter`: suppresses forge/just gas estimates, ETHERSCAN warnings,
  separator lines; highlights Step X/3 headers and deployed addresses.
- `TransformProcessLine`: strips redundant timestamps from supervisor process
  lines (go-ethereum and reth/tracing formats both normalised to
  `LEVEL  message  key=val`).

---

## Configuration

- `configs/config.yaml` — user config (committed in this repo on branch
  `feat/local-testnet-fixes`). L1 ports are auto-updated by `scripts/deploy.sh`.
- `configs/config.go` — Go struct; adding a YAML key without updating this
  silently drops it.
- Key flags (all under `l2.*`):
  - `flashblocks.enabled: true` — enables op-rbuilder + rollup-boost
  - `sidecar.enabled: true` — enables publisher + sidecar-a/b
  - `frontend.enabled: true` — starts Ethera Labs Console
  - `blockscout.enabled: false` — Blockscout is Docker-only and disabled

---

## Deploy script (`scripts/deploy.sh`)

Phases run in order:

1. **Preflight** — check prereqs, read Kurtosis L1 ports, update config.yaml
2. **Clean** (deploy-clean only) — wipe `.localnet/{state,networks,data,logs}`
3. **Binaries** — download/build all native binaries (cached)
4. **L1 config** — generate chainconfig JSON, check proxy + wallet
5. **Build** — compile the localnet Go binary
6. **Launch** — kill stale processes, run `localnet l2`

The Go binary (`cmd/localnet`) runs the three L2 phases:
1. L1 contract deployment via `op-deployer`
2. L2 config generation (genesis, JWT, rollup config)
3. Native process startup (supervised)

---

## L1 terminal UI (`internal/l1/service.go`)

`make run-l1` shows a custom colored UI instead of raw JSON logs:
- Kurtosis `VITE_CONFIG_JSON`-style progress events → human-readable banner + step counter
- `info` messages (Step X/5) → green `▶` milestones
- Callframe/Fault/Revert warnings → suppressed (expected chain-assertion noise)
- "⭐ us on GitHub" message → suppressed
- `slog` is set to `LevelError` before the UI starts so JSON config dumps don't leak

---

## Block explorer (Expedition)

Expedition (`github.com/xops/expedition`) is used instead of Blockscout because:
- Blockscout requires Docker (ruled out)
- Otterscan v2 requires Erigon's private `erigon_*` RPC namespace (op-reth doesn't have it)
- Expedition works with standard `eth_*` JSON-RPC only

Two instances serve the pre-built static files via `npm exec serve`:
- `explorer-a` → port 5100 (chain A / rollup-a)
- `explorer-b` → port 5200 (chain B / rollup-b)

On first deploy, `scripts/deploy.sh` runs `npm run build` in the Expedition
directory (cached in `.localnet/build/expedition/build/`). The build requires
`NODE_OPTIONS=--openssl-legacy-provider` because react-scripts v3 uses OpenSSL
APIs incompatible with Node 17+.

---

## Adding new native services

1. Add port constants to `internal/l2/l2runtime/native/services.go`
2. Add a `ProcessSpec` builder method on `Builder`
3. Add `Start<Service>` / `Wait<Service>Ready` to `internal/l2/l2runtime/services/manager.go`
4. Wire startup in `internal/l2/l2runtime/orchestrator.go`
5. Add binary build to `scripts/deploy.sh` `setup_binaries` section
6. Kill by port or full binary path in `stop_l2_procs`
7. Add URL to `print_summary`

---

## Implementation conventions

- Errors: `errors.Join(err, errors.New("..."))` or `fmt.Errorf("...: %w", err)`.
- Logging: `internal/logger` → `slog` with structured fields. Terminal UI
  (`InitializeTerminal`) replaces JSON slog for interactive commands.
- Tests: alongside packages as `_test.go`; use `t.TempDir()` for filesystem.
- No Docker for L2 runtime. Docker is used only for Kurtosis (L1) and
  optionally for Observability.
