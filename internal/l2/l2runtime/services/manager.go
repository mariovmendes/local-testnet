package services

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ethera-labs/local-testnet/internal/l2/infra/docker"
	"github.com/ethera-labs/local-testnet/internal/logger"
)

// Manager manages L2 service lifecycle via docker-compose
type Manager struct {
	rootDir                   string
	dockerFilePath            string
	flashblocksDockerFilePath string
	sidecarDockerFilePath     string
	bundlerDockerFilePath     string
	frontendDockerFilePath    string
	frontendDevDockerFilePath string
	altDADockerFilePath       string
	opSuccinctDockerFilePath  string
	opBesuDockerFilePath      string
	flashblocksEnabled        bool
	sidecarEnabled            bool
	bundlerEnabled            bool
	frontendEnabled           bool
	altDAEnabled              bool
	opSuccinctEnabled         bool
	opBesuEnabled             bool
	logger                    *slog.Logger
}

// NewManager creates a new service manager
func NewManager(rootDir, dockerFilePath string) *Manager {
	return &Manager{
		rootDir:        rootDir,
		dockerFilePath: dockerFilePath,
		logger:         logger.Named("service_manager"),
	}
}

// WithFlashblocks enables flashblocks support with the specified docker file
func (m *Manager) WithFlashblocks(flashblocksDockerFilePath string) *Manager {
	m.flashblocksDockerFilePath = flashblocksDockerFilePath
	m.flashblocksEnabled = true
	return m
}

// WithOpBesu replaces the op-reth validator EL with op-besu via the given overlay file.
func (m *Manager) WithOpBesu(opBesuDockerFilePath string) *Manager {
	m.opBesuDockerFilePath = opBesuDockerFilePath
	m.opBesuEnabled = true
	return m
}

// WithSidecar enables sidecar support with the specified docker file
func (m *Manager) WithSidecar(sidecarDockerFilePath string) *Manager {
	m.sidecarDockerFilePath = sidecarDockerFilePath
	m.sidecarEnabled = true
	return m
}

// WithBundler enables the ERC-4337 v0.7 ethera-bundler service with the specified docker file
func (m *Manager) WithBundler(bundlerDockerFilePath string) *Manager {
	m.bundlerDockerFilePath = bundlerDockerFilePath
	m.bundlerEnabled = true
	return m
}

// WithFrontend enables the Ethera Labs Console. A non-empty
// frontendDevDockerFilePath layers the dev override on top of the base
// frontend compose file so source edits hot-reload via Vite.
func (m *Manager) WithFrontend(frontendDockerFilePath, frontendDevDockerFilePath string) *Manager {
	m.frontendDockerFilePath = frontendDockerFilePath
	m.frontendDevDockerFilePath = frontendDevDockerFilePath
	m.frontendEnabled = true
	return m
}

// WithAltDA enables AltDA mode with the specified docker file
func (m *Manager) WithAltDA(altDADockerFilePath string) *Manager {
	m.altDADockerFilePath = altDADockerFilePath
	m.altDAEnabled = true
	return m
}

// WithOPSuccinct enables op-succinct support with the specified docker file.
func (m *Manager) WithOPSuccinct(opSuccinctDockerFilePath string) *Manager {
	m.opSuccinctDockerFilePath = opSuccinctDockerFilePath
	m.opSuccinctEnabled = true
	return m
}

// StartAll starts all L2 services
func (m *Manager) StartAll(ctx context.Context, env map[string]string) error {
	services := []string{
		"localnet-health",
		"publisher",
		"validator-el-a",
		"validator-el-b",
	}

	dockerFiles := []string{m.dockerFilePath}

	// op-besu override must come right after the base file so it replaces the
	// validator-el-a/validator-el-b service definitions (no other overlay touches them).
	if m.opBesuEnabled && m.opBesuDockerFilePath != "" {
		dockerFiles = append(dockerFiles, m.opBesuDockerFilePath)
	}

	if m.altDAEnabled && m.altDADockerFilePath != "" {
		dockerFiles = append(dockerFiles, m.altDADockerFilePath)
		services = append(services,
			"op-alt-da-a",
			"op-alt-da-b",
		)
	}

	if m.opSuccinctEnabled && m.opSuccinctDockerFilePath != "" {
		dockerFiles = append(dockerFiles, m.opSuccinctDockerFilePath)
		services = append(services,
			"op-succinct-postgres",
			"op-succinct-a",
			"op-succinct-b",
		)
	}

	services = append(services,
		"op-node-a",
		"op-node-b",
		"op-batcher-a",
		"op-batcher-b",
		"op-proposer-a",
		"op-proposer-b",
	)

	if m.flashblocksEnabled && m.flashblocksDockerFilePath != "" {
		dockerFiles = append(dockerFiles, m.flashblocksDockerFilePath)
		services = append(services,
			"op-rbuilder-a",
			"op-rbuilder-b",
			"rollup-boost-a",
			"rollup-boost-b",
		)
	}

	if m.sidecarEnabled && m.sidecarDockerFilePath != "" {
		dockerFiles = append(dockerFiles, m.sidecarDockerFilePath)
		services = append(services,
			"sidecar-a",
			"sidecar-b",
		)
	}

	if m.bundlerEnabled && m.bundlerDockerFilePath != "" {
		dockerFiles = append(dockerFiles, m.bundlerDockerFilePath)
		services = append(services,
			"bundler-a",
			"bundler-b",
		)
	}

	// Frontend (ethera-console) is started separately after contract deployment

	if len(dockerFiles) > 1 {
		m.logger.With("services", services, "flashblocks", m.flashblocksEnabled, "sidecar", m.sidecarEnabled, "bundler", m.bundlerEnabled, "altDA", m.altDAEnabled, "op_succinct", m.opSuccinctEnabled).Info("starting L2 services")

		if err := docker.ComposeUpMultiFile(ctx, dockerFiles, env, services...); err != nil {
			return fmt.Errorf("failed to start services: %w", err)
		}
	} else {
		m.logger.With("services", services).Info("starting L2 services")

		if err := docker.ComposeUp(ctx, m.dockerFilePath, env, services...); err != nil {
			return fmt.Errorf("failed to start services: %w", err)
		}
	}

	m.logger.Info("L2 services started successfully")
	return nil
}

// StartFlashblocks starts flashblocks services (op-rbuilder and rollup-boost)
func (m *Manager) StartFlashblocks(ctx context.Context, env map[string]string) error {
	if !m.flashblocksEnabled || m.flashblocksDockerFilePath == "" {
		return fmt.Errorf("flashblocks not enabled or docker file not set")
	}

	services := []string{
		"op-rbuilder-a",
		"op-rbuilder-b",
		"rollup-boost-a",
		"rollup-boost-b",
	}

	m.logger.With("services", services).Info("starting flashblocks services")

	if err := docker.ComposeUpMultiFile(ctx, []string{m.dockerFilePath, m.flashblocksDockerFilePath}, env, services...); err != nil {
		return fmt.Errorf("failed to start flashblocks services: %w", err)
	}

	m.logger.Info("flashblocks services started successfully")
	return nil
}

// StartFrontend builds and starts the Ethera Labs Console. Must be called after contract deployment
// so that env contains deployed bridge, token, and CET factory addresses.
func (m *Manager) StartFrontend(ctx context.Context, dockerFiles []string, env map[string]string) error {
	if !m.frontendEnabled || m.frontendDockerFilePath == "" {
		return fmt.Errorf("frontend not enabled or docker file not set")
	}

	allFiles := append(dockerFiles, m.frontendDockerFilePath)
	if m.frontendDevDockerFilePath != "" {
		allFiles = append(allFiles, m.frontendDevDockerFilePath)
		m.logger.Info("building and starting Ethera Labs Console (dev mode)")
	} else {
		m.logger.Info("building and starting Ethera Labs Console")
	}

	if err := docker.ComposeBuildMultiFile(ctx, allFiles, env, "ethera-console"); err != nil {
		return fmt.Errorf("failed to build ethera-console: %w", err)
	}

	if err := docker.ComposeUpMultiFile(ctx, allFiles, env, "ethera-console"); err != nil {
		return fmt.Errorf("failed to start ethera-console: %w", err)
	}

	m.logger.Info("Ethera Labs Console started successfully")
	return nil
}
