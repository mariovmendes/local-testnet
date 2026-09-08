# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Localnet Control Plane is a CLI tool for managing local L1 and L2 Ethereum test networks. It orchestrates:

- **L1 Network** (`internal/l1`): Uses Kurtosis to deploy Ethereum execution/consensus clients and SSV nodes via the
  `github.com/ssvlabs/ssv-mini` Starlark package.
- **L2 Network** (`internal/l2`): Builds and runs a pair of OP Stack rollups (rollup-a, rollup-b) plus the Ethera Labs
  publisher, optional flashblocks, sidecars, AltDA, op-succinct, and a frontend console. The L2 stack is driven by
  docker-compose overlays under `internal/l2/infra/docker/`.
- **Observability Stack** (`internal/observability`): Docker-based monitoring infrastructure (Grafana, Prometheus, Loki,
  Tempo, Alloy).

## Build and Run Commands

- `make build` - compile the binary to `cmd/localnet/bin/localnet`.
- `make run-l1` / `make run-l2` / `make run-observability` - bring up a single subsystem.
- `make show-l1` / `make show-l2` / `make show-observability` - inspect running services.
- `make stop-*` - stop a subsystem without removing its state.
- `make clean-*` - stop and remove all generated state for a subsystem.
- `make test` - `go test ./...`.
- `make lint` - `golangci-lint run -v ./...`.
- `make docker-build` - build the localnet CLI as a Docker image (see `build/DOCKER.md`).

## Architecture

### CLI Entry Point

`cmd/localnet/main.go` wires Cobra subcommands and loads `config.yaml` via Viper. Configuration is bound to
`configs.Values` (see `configs/config.go`).

### L1 (`internal/l1`)

- `cmd.go` registers the `l1` subcommand.
- `service.go` creates the `localnet` enclave and runs the `github.com/ssvlabs/ssv-mini` package using parameters
  embedded from `params.yaml`.

### L2 (`internal/l2`)

- `cmd.go`/`flags.go` wire CLI flags onto Viper keys under `l2.*`.
- `service.go` runs the three deployment phases:
    1. **L1 deployment** (`l1deployment/`) - `op-deployer` runs the OP Stack contracts on L1 (system config, dispute
       game factory, etc.).
    2. **L2 config generation** (`l2config/`) - produces per-chain `genesis.json`, `rollup.json`, `runtime.env`,
       `opsuccinct.env`, dispute env templates, and registry TOML files under `.localnet/networks/<chain>/`.
    3. **L2 runtime** (`l2runtime/`) - deploys L2 contracts on each rollup (`UniversalBridgeMailbox`, `CetFactory`,
       `ComposeETHLiquidity`, `ComposeL2ToL2Bridge`, `MockL2ERC20`), then starts containers via docker-compose.
- `infra/docker/` holds the canonical compose files: `docker-compose.yml` (core, includes the always-on
  `localnet-health` container that exposes `GET /api/services` to the Ethera Labs Console),
  `docker-compose.flashblocks.yml`, `docker-compose.sidecar.yml`, `docker-compose.altda.yml`,
  `docker-compose.opsuccinct.yml`, `docker-compose.frontend.yml` (+ dev override).
- `cmd/localnet-health/` is the Go binary that backs the health container. It mounts the docker socket
  read-only and reports container status (`up`/`starting`/`down`/`missing`) for every catalogued service.
- `infra/git/` clones source-built repositories into `.localnet/services/<name>` when `repositories.<name>.url` is set.

### Observability (`internal/observability`)

- One package per service under `alloy/`, `grafana/`, `loki/`, `prometheus/`, `tempo/`, plus `shared/` for the Docker
  network.
- Containers share the `stack=localnet-observability` label.
- Service configs are mounted from `configs/{service-name}/`.

## Configuration

- `configs/config.example.yaml` documents every supported field with comments.
- `configs/config.yaml` is the user's local config (gitignored; copied to the binary directory by `make build`).
- L2 fields are validated by `(*L2).Validate` in `configs/config.go` - adding a new YAML key without updating that
  struct silently drops it.

## Contracts

L2 contracts are precompiled and embedded under `internal/l2/l2runtime/contracts/compiled/contracts.json`. Only the
contracts listed in `internal/l2/l2runtime/contracts/contract.go` are recognised - there is no fallback for legacy
`Mailbox` or `PingPong` contracts. Regenerate the embedded artefact with `make run-l2-compile`.

## Debugging

When a service misbehaves, triage from the highest, cheapest level first and only then go
deeper:

1. **Provenance / freshness** - the `:dev` images are built from `local-path` source repos
   (`sidecar/`, `op-rbuilder/`, `publisher/`, `contracts/`) pinned to a `branch:` in
   `configs/config.yaml`. Check the running image build date against the source `git log`, and
   whether each repo is on the expected branch and not behind upstream (`git rev-list --left-right
   --count @{u}...HEAD`). A stale checkout / wrong branch is a common failure mode.
2. **Configuration / wiring** - confirm `configs/config.yaml` (and the binary-dir copy) point at the
   right branches, ports, and addresses; that the service is enabled and its dependencies are too.
3. **Protocol internals** - only after 1-2 are clean, drop into calldata/trace/contract/source
   forensics.

## Implementation Notes

- Errors are wrapped with context using `errors.Join(err, errors.New("..."))` or `fmt.Errorf("...: %w", err)`.
- All package logging goes through `internal/logger`; prefer `slog` and structured fields over format strings.
- Tests live alongside their packages as `_test.go` files; tests that touch the filesystem must use `t.TempDir`.
