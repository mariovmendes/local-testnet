# Observability Stack

The observability stack provides monitoring, logging, and tracing capabilities for the local testnet.

## What It Does

Deploys a complete Docker-based observability infrastructure with:

- **Grafana**: Visualization dashboards (port 3000)
- **Prometheus**: Metrics collection and storage
- **Loki**: Log aggregation
- **Tempo**: Distributed tracing
- **Alloy**: Telemetry data collection agent

For a host-run Prometheus workflow, use `scripts/deploy-observability.sh`.

## Usage

The observability stack runs independently of the L1 and L2 commands:

```bash
# Start observability stack
make run-observability

# Show running containers
make show-observability

# Stop containers without removing them
make stop-observability

# Remove containers
make clean-observability
```

## Accessing Services

### Grafana

- **URL**: http://localhost:3000
- **Authentication**: Anonymous access enabled (admin role)
- **Pre-configured**: Connected to Prometheus, Loki, and Tempo data sources

### Service Ports

All services use internal Docker networking. Only Grafana is exposed to the host.

If you start Prometheus on the host, it listens on `http://localhost:9090` and
scrapes the publisher at `http://localhost:18081/metrics`.

## Implementation Details

### Architecture

Each service runs as a separate Docker container:

- All containers share the `stack=localnet-observability` label
- Connected via a dedicated Docker network
- Service-specific configurations in `configs/{service-name}/`

### Service Packages

Each observability service has its own package under `internal/observability/`:

- `alloy/` - Telemetry collection
- `grafana/` - Dashboard and visualization
- `loki/` - Log aggregation
- `prometheus/` - Metrics storage
- `tempo/` - Trace storage
- `shared/` - Shared Docker network management

### Data Persistence

By default, data is stored in Docker volumes. Configure volume mounts in service configurations if persistent storage is
needed.

## Configuration Files

Service configurations are located in:

- `configs/alloy/` - Alloy configuration
- `configs/grafana/` - Dashboards and datasource definitions
- `configs/loki/` - Log retention and storage settings
- `configs/prometheus/` - Scrape configs and retention
- `configs/tempo/` - Trace storage configuration

## Troubleshooting

**Issue:** Grafana not accessible at localhost:3000
**Solution:** Check if port 3000 is already in use: `lsof -i :3000`

**Issue:** No metrics/logs visible
**Solution:** Verify services are running: `make show-observability`

**Issue:** Docker network conflicts
**Solution:** Clean up and restart: `make clean-observability && make run-observability`

For more details, see the [main README](../../README.md).
