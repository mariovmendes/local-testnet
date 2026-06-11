<p align="center"><img src="https://framerusercontent.com/images/9FedKxMYLZKR9fxBCYj90z78.png?scale-down-to=512&width=893&height=363" alt="SSV Network"></p>

<a href="https://discord.com/invite/ssvnetworkofficial"><img src="https://img.shields.io/badge/discord-%23ssvlabs-8A2BE2.svg" alt="Discord" /></a>

## ✨ Introduction

Local testnet is a CLI tool for managing local L1 and L2 Ethereum test networks. It provides a complete local
development environment for testing Ethereum applications with multiple L2 rollups.

## 📋 Prerequisites

- Docker and Docker Compose
- Go 1.26+
- Kurtosis 1.15.2+ (for L1 network)
- Foundry/Forge (for L2 commands)
- [just](https://github.com/casey/just) (for L2 commands)
- jq (for L2 commands)

## ⚙️ How to Build

```bash
# Clone the repo
git clone https://github.com/ethera-labs/local-testnet.git

# Navigate
cd local-testnet

# Build the binary
make build
```

The binary will be available at `cmd/localnet/bin/localnet`.

## 🚀 Commands

The tool provides three main commands, each managing a different part of the local network:

### L1 Network (`localnet l1`)

Manages the Layer 1 Ethereum test network using Kurtosis. Deploys execution and consensus clients along with SSV nodes.

**📖 [Read L1 Documentation](internal/l1/README.md)**

### L2 Network (`localnet l2`)

Manages Layer 2 rollup networks. Orchestrates a three-phase deployment process for multiple OP Stack rollups.

L2 can also run in AltDA mode, which routes batch data through per-rollup DA servers instead of posting calldata to
L1. For long-lived environments, combine this with `l2.alt-da.skip-l1-deploy=true` and preprovisioned challenge
contract addresses.

Source-built L2 services are configured through `l2.repositories`.

**📖 [Read L2 Documentation](internal/l2/README.md)**

### Observability (`localnet observability`)

Manages the observability stack for monitoring and debugging. Deploys Grafana, Prometheus, Loki, Tempo, and Alloy.

For a host-run Prometheus workflow, use `scripts/deploy-observability.sh`.

**📖 [Read Observability Documentation](internal/observability/README.md)**

### Ethera Labs Console (`frontend`)

Web UI for testing cross-chain transactions. Requires L2 with flashblocks and sidecar enabled. Use `--frontend-enabled`
to start with L2, or run locally with `make run-frontend`.

**📖 [Read Frontend Documentation](frontend/README.md)** | **📖 [Port Reference](docs/ports.md)**

## 🔧 Usage

```bash
# Build and run (based on what's enabled in config.yaml)
make run

# Or run specific components:
make run-l1              # Start L1 network
make run-l2              # Start L2 networks
make run-l2 L2_ARGS="--alt-da-enabled"  # Start L2 with per-rollup AltDA servers
make run-l2 L2_ARGS="--op-succinct-enabled"  # Start L2 with mock-mode op-succinct validity proposers
make run-l2-compile      # Compile L2 contracts from publisher repo
make run-observability   # Start observability stack
make run-l2 L2_ARGS="--flashblocks-enabled --blockscout-enabled --sidecar-enabled --frontend-enabled"  # Full stack with Ethera Labs Console
make run-frontend       # Run Ethera Labs Console locally (requires L2 + flashblocks + sidecar already running)

# Inspect running services:
make show-l1             # Show Kurtosis enclave
make show-l2             # Show L2 docker containers
make show-observability  # Show observability containers

# Stop services (preserves configs):
make stop                # Stop all components
make stop-l1             # Stop L1 (Kurtosis)
make stop-l2             # Stop L2 (Docker containers)
make stop-observability  # Stop observability stack

# Clean up (removes configs):
make clean               # Clean all components
make clean-l1            # Clean L1 (Kurtosis)
make clean-l2            # Clean L2 (docker containers + generated files)
make clean-l2-full       # Clean L2 including locally built Docker images
make clean-observability # Clean observability stack
```

Configuration is managed via `configs/config.yaml`. Use `configs/config.example.yaml` or
`configs/config.sepolia.yaml` as a starting point. AltDA-specific settings live under `l2.alt-da`;
op-succinct settings live under `l2.op-succinct`.

## 📜 Viewing Logs

Each component has its own logging approach:

- **L1**: Uses Kurtosis - see [L1 Documentation](internal/l1/README.md#viewing-logs)
- **L2**: Uses Docker containers - see [L2 Documentation](internal/l2/README.md#viewing-logs)
- **Observability**: Access Grafana at http://localhost:3000 for dashboards and Loki log aggregation

## ⏹️ Stopping Services

Stop commands preserve generated state and configuration:

```bash
# Stop all services (preserves configs and state)
make stop

# Or stop specific components:
make stop-l1             # Stop L1 (Kurtosis)
make stop-l2             # Stop L2 (Docker containers)
make stop-observability  # Stop observability stack
```

Clean commands remove generated state, containers, and other local artifacts:

```bash
# Clean all components
make clean

# Or clean specific components:
make clean-l1            # Clean L1 (Kurtosis)
make clean-l2            # Clean L2 (docker containers + generated files)
make clean-l2-full       # Clean L2 including locally built Docker images
make clean-observability # Clean observability stack
```

## 🐳 Docker Usage

The tool can be run in Docker, which is useful for CI/CD or environments where dependencies are difficult to install.

**📖 [Read Docker Documentation](build/DOCKER.md)**

## License

Repository is distributed under [GPL-3.0](LICENSE).
