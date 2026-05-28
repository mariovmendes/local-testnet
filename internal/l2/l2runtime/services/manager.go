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

// StartRbuilder initialises op-rbuilder datadirs for both chains (if not
// already done) and starts op-rbuilder-a and op-rbuilder-b.
func (m *NativeManager) StartRbuilder(ctx context.Context) error {
	for _, chain := range []configs.L2ChainName{
		configs.L2ChainNameRollupA,
		configs.L2ChainNameRollupB,
	} {
		if err := m.builder.InitRbuilder(ctx, chain); err != nil {
			return fmt.Errorf("init rbuilder for %s: %w", chain, err)
		}
	}

	specs, err := m.builder.RbuilderSpecs()
	if err != nil {
		return fmt.Errorf("build rbuilder specs: %w", err)
	}

	m.logger.Info("starting op-rbuilder processes", "count", len(specs))
	return m.sup.Start(ctx, specs)
}

// WaitRbuilderReady polls op-rbuilder HTTP ports until both respond or
// 120 s elapses.
func (m *NativeManager) WaitRbuilderReady(ctx context.Context) error {
	endpoints := []string{
		fmt.Sprintf("127.0.0.1:%d", native.RbuilderAHTTPPort),
		fmt.Sprintf("127.0.0.1:%d", native.RbuilderBHTTPPort),
	}
	m.logger.Info("waiting for op-rbuilder HTTP ports", "endpoints", endpoints)

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
			m.logger.Info("op-rbuilder instances are ready")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("op-rbuilder did not become ready within %s", rethReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rethPollInterval):
		}
	}
}

// StartRollupBoost starts rollup-boost-a and rollup-boost-b.
func (m *NativeManager) StartRollupBoost(ctx context.Context) error {
	specs := m.builder.RollupBoostSpecs()
	m.logger.Info("starting rollup-boost processes", "count", len(specs))
	return m.sup.Start(ctx, specs)
}

// WaitRollupBoostReady polls rollup-boost Engine API ports until both respond.
func (m *NativeManager) WaitRollupBoostReady(ctx context.Context) error {
	endpoints := []string{
		fmt.Sprintf("127.0.0.1:%d", native.RollupBoostAEnginePort),
		fmt.Sprintf("127.0.0.1:%d", native.RollupBoostBEnginePort),
	}
	m.logger.Info("waiting for rollup-boost Engine API ports", "endpoints", endpoints)

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
			m.logger.Info("rollup-boost instances are ready")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rollup-boost did not become ready within %s", rethReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rethPollInterval):
		}
	}
}

// StartPublisher starts the shared Publisher service.
func (m *NativeManager) StartPublisher(ctx context.Context, spec supervisor.ProcessSpec) error {
	m.logger.Info("starting publisher")
	return m.sup.Start(ctx, []supervisor.ProcessSpec{spec})
}

// WaitPublisherReady polls the publisher metrics HTTP port until it responds.
func (m *NativeManager) WaitPublisherReady(ctx context.Context) error {
	ep := fmt.Sprintf("127.0.0.1:%d", native.PublisherMetricsPort)
	m.logger.Info("waiting for publisher metrics port", "endpoint", ep)
	deadline := time.Now().Add(rethReadyTimeout)
	for {
		conn, err := net.DialTimeout("tcp", ep, rethDialTimeout)
		if err == nil {
			conn.Close()
			m.logger.Info("publisher is ready")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("publisher did not become ready within %s", rethReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rethPollInterval):
		}
	}
}

// StartSidecars starts sidecar-a and sidecar-b.
func (m *NativeManager) StartSidecars(ctx context.Context, specs []supervisor.ProcessSpec) error {
	m.logger.Info("starting sidecar processes", "count", len(specs))
	return m.sup.Start(ctx, specs)
}

// WaitSidecarsReady polls the sidecar HTTP ports until both respond.
func (m *NativeManager) WaitSidecarsReady(ctx context.Context) error {
	endpoints := []string{
		fmt.Sprintf("127.0.0.1:%d", native.SidecarAAPIPort),
		fmt.Sprintf("127.0.0.1:%d", native.SidecarBAPIPort),
	}
	m.logger.Info("waiting for sidecar API ports", "endpoints", endpoints)
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
			m.logger.Info("sidecars are ready")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("sidecars did not become ready within %s", rethReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rethPollInterval):
		}
	}
}

// StartFrontend starts the Ethera Labs Console (Vite dev server).
func (m *NativeManager) StartFrontend(ctx context.Context, spec supervisor.ProcessSpec) error {
	m.logger.Info("starting frontend dev server", "dir", spec.Dir)
	return m.sup.Start(ctx, []supervisor.ProcessSpec{spec})
}

// StopAll gracefully shuts down all supervised processes.
func (m *NativeManager) StopAll(ctx context.Context) {
	m.logger.Info("stopping all native L2 processes")
	m.sup.StopAll(ctx)
}
