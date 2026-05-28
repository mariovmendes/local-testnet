package services

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethera-labs/local-testnet/internal/l2/infra/supervisor"
	"github.com/ethera-labs/local-testnet/internal/l2/l2runtime/native"
	"github.com/ethera-labs/local-testnet/internal/logger"
)

const (
	rethReadyTimeout = 120 * time.Second
	rethPollInterval = 2 * time.Second
	rethDialTimeout  = 1 * time.Second
)

// NativeManager manages the OP stack L2 runtime as native host processes.
type NativeManager struct {
	builder *native.Builder
	sup     *supervisor.Supervisor
	logger  *slog.Logger
}

// NewNativeManager creates a NativeManager backed by the given Builder and
// Supervisor.
func NewNativeManager(builder *native.Builder, sup *supervisor.Supervisor) *NativeManager {
	return &NativeManager{
		builder: builder,
		sup:     sup,
		logger:  logger.Named("native_service_manager"),
	}
}

// StartReth initialises op-reth datadirs for both chains (if not already
// done) and then starts op-reth-a and op-reth-b as supervised processes.
func (m *NativeManager) StartReth(ctx context.Context) error {
	for _, chain := range []configs.L2ChainName{
		configs.L2ChainNameRollupA,
		configs.L2ChainNameRollupB,
	} {
		if err := m.builder.InitReth(ctx, chain); err != nil {
			return fmt.Errorf("init reth for %s: %w", chain, err)
		}
	}

	specs, err := m.builder.RethSpecs()
	if err != nil {
		return fmt.Errorf("build reth specs: %w", err)
	}

	m.logger.Info("starting op-reth processes", "count", len(specs))
	return m.sup.Start(ctx, specs)
}

// WaitRethReady polls the HTTP ports of op-reth-a (18545) and op-reth-b
// (28545) until both accept TCP connections or the 120-second deadline
// is exceeded.
func (m *NativeManager) WaitRethReady(ctx context.Context) error {
	endpoints := []string{
		fmt.Sprintf("127.0.0.1:%d", native.RethAHTTPPort),
		fmt.Sprintf("127.0.0.1:%d", native.RethBHTTPPort),
	}

	m.logger.Info("waiting for op-reth HTTP ports", "endpoints", endpoints, "timeout", rethReadyTimeout)

	deadline := time.Now().Add(rethReadyTimeout)
	for {
		allReady := true
		for _, ep := range endpoints {
			conn, err := net.DialTimeout("tcp", ep, rethDialTimeout)
			if err != nil {
				allReady = false
				break
			}
			conn.Close()
		}
		if allReady {
			m.logger.Info("op-reth instances are ready")
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("op-reth did not become ready within %s", rethReadyTimeout)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rethPollInterval):
		}
	}
}

// StartNodeBatcherProposer starts op-node, op-batcher, and op-proposer for
// both chains (6 processes total) via the supervisor.
func (m *NativeManager) StartNodeBatcherProposer(ctx context.Context) error {
	specs, err := m.builder.NodeBatcherProposerSpecs()
	if err != nil {
		return fmt.Errorf("build node/batcher/proposer specs: %w", err)
	}

	m.logger.Info("starting op-node, op-batcher, op-proposer processes", "count", len(specs))
	return m.sup.Start(ctx, specs)
}

// StopAll gracefully shuts down all supervised processes.
func (m *NativeManager) StopAll(ctx context.Context) {
	m.logger.Info("stopping all native L2 processes")
	m.sup.StopAll(ctx)
}
