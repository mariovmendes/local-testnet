package observability

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/docker/docker/client"
	"github.com/ethera-labs/local-testnet/internal/observability/alloy"
	"github.com/ethera-labs/local-testnet/internal/observability/grafana"
	"github.com/ethera-labs/local-testnet/internal/observability/loki"
	"github.com/ethera-labs/local-testnet/internal/observability/prometheus"
	"github.com/ethera-labs/local-testnet/internal/observability/shared"
	"github.com/ethera-labs/local-testnet/internal/observability/tempo"
)

const skipPrometheusEnv = "LOCALNET_OBSERVABILITY_SKIP_PROMETHEUS"

func start(ctx context.Context) error {
	slog.Info("instantiating Docker client")

	cli, err := client.NewClientWithOpts(client.WithAPIVersionNegotiation())
	if err != nil {
		return errors.Join(err, errors.New("failed to instantiate Docker client"))
	}
	defer cli.Close()

	slog.With("network_name", shared.ObservabilityNetworkName).Info("creating new shared Docker network")
	if err = shared.EnsureNetwork(ctx, cli); err != nil {
		return errors.Join(err, errors.New("failed to create a Docker network"))
	}

	if err := grafana.Start(ctx, cli); err != nil {
		return errors.Join(err, errors.New("failed to start Grafana service"))
	}

	if err := loki.Start(ctx, cli); err != nil {
		return errors.Join(err, errors.New("failed to start Loki service"))
	}

	if err := alloy.Start(ctx, cli); err != nil {
		return errors.Join(err, errors.New("failed to start Alloy service"))
	}

	if skipPrometheus() {
		slog.With("env", skipPrometheusEnv).Info("skipping Prometheus container startup")
	} else {
		if err := prometheus.Start(ctx, cli); err != nil {
			return errors.Join(err, errors.New("failed to start Prometheus service"))
		}
	}

	if err := tempo.Start(ctx, cli); err != nil {
		return errors.Join(err, errors.New("failed to start Tempo service"))
	}

	return nil
}

func skipPrometheus() bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(skipPrometheusEnv)))
	switch value {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
