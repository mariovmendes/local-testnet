package l2runtime

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethera-labs/local-testnet/internal/l2/infra/docker"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/genesis"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/opsuccinct"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/secrets"
	"github.com/ethera-labs/local-testnet/internal/l2/l2runtime/contracts"
	"github.com/ethera-labs/local-testnet/internal/l2/l2runtime/registry"
	"github.com/ethera-labs/local-testnet/internal/l2/l2runtime/services"
	"github.com/ethera-labs/local-testnet/internal/logger"
	"github.com/ethereum/go-ethereum/common"
)

// Orchestrator coordinates Phase 3: L2 runtime operations
//   - Builds Docker images via docker-compose
//   - Starts initial services (publisher, op-reth)
//   - Deploys L2 helper contracts
//   - Restarts dependent services to pick up contract addresses
//   - Starts final services (op-node, batcher, proposer)
type Orchestrator struct {
	rootDir     string
	localnetDir string
	networksDir string
	servicesDir string
	logger      *slog.Logger
}

// NewOrchestrator creates a new Phase 3 orchestrator
func NewOrchestrator(rootDir, localnetDir, networksDir, servicesDir string) *Orchestrator {
	return &Orchestrator{
		rootDir:     rootDir,
		localnetDir: localnetDir,
		networksDir: networksDir,
		servicesDir: servicesDir,
		logger:      logger.Named("l2_runtime_orchestrator"),
	}
}

// overlayPaths holds the filesystem paths of optional docker-compose overlay files.
// An empty string means the overlay is disabled.
type overlayPaths struct {
	altDA       string
	opSuccinct  string
	flashblocks string
	sidecar     string
	bundler     string
	frontend    string
	frontendDev string
	opBesu      string
}

// Execute runs Phase 3: Build images, start services, deploy contracts
func (o *Orchestrator) Execute(ctx context.Context, cfg configs.L2, gameFactoryAddr common.Address, composeL2OOAddr common.Address) (map[configs.L2ChainName]map[contracts.ContractName]common.Address, error) {
	o.logger.Info("Phase 3: Starting L2 runtime operations")

	publisherConfig := registry.NewConfigurator()
	if err := publisherConfig.SetupRegistry(o.localnetDir, cfg, gameFactoryAddr); err != nil {
		return nil, fmt.Errorf("failed to setup publisher registry: %w", err)
	}

	dockerPath, err := docker.EnsureComposeFile(o.localnetDir)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare docker file: %w", err)
	}

	envBuilder := docker.NewEnvBuilder(o.rootDir, o.networksDir, o.servicesDir)
	envVars, err := envBuilder.BuildComposeEnv(cfg, gameFactoryAddr, composeL2OOAddr)
	if err != nil {
		return nil, err
	}

	overlays, err := o.resolveOverlayPaths(cfg)
	if err != nil {
		return nil, err
	}

	o.logger.With("env", envVars).Info("environment variables were constructed. Building docker services")
	if err := o.buildComposeServices(ctx, dockerPath, envVars, overlays); err != nil {
		return nil, fmt.Errorf("failed to build docker services: %w", err)
	}

	o.logger.Info("docker services built successfully")
	serviceManager := services.NewManager(o.rootDir, dockerPath)

	if overlays.opBesu != "" {
		o.logger.Info("op-besu enabled, using op-besu as the validator execution client")
		serviceManager.WithOpBesu(overlays.opBesu)
	}
	if overlays.altDA != "" {
		serviceManager.WithAltDA(overlays.altDA)
	}
	if overlays.opSuccinct != "" {
		o.logger.Info("op-succinct enabled, configuring mock-mode validity services")
		serviceManager.WithOPSuccinct(overlays.opSuccinct)
	}
	if overlays.flashblocks != "" {
		o.logger.Info("flashblocks enabled, configuring services to use rollup-boost")
		serviceManager.WithFlashblocks(overlays.flashblocks)

		if cfg.Flashblocks.OpRbuilderImageTag != "" {
			envVars["OP_RBUILDER_IMAGE_TAG"] = cfg.Flashblocks.OpRbuilderImageTag
		}
		if cfg.Flashblocks.RollupBoostImageTag != "" {
			envVars["ROLLUP_BOOST_IMAGE_TAG"] = cfg.Flashblocks.RollupBoostImageTag
		}
	}
	if overlays.sidecar != "" {
		o.logger.Info("sidecar enabled, configuring sidecar services")
		serviceManager.WithSidecar(overlays.sidecar)
	}
	if overlays.bundler != "" {
		o.logger.Info("bundler enabled, configuring ERC-4337 v0.7 ethera-bundler services")
		serviceManager.WithBundler(overlays.bundler)
	}
	if overlays.frontend != "" {
		if overlays.frontendDev != "" {
			o.logger.Info("frontend dev mode enabled, configuring Ethera Labs Console with Vite hot-reload")
		} else {
			o.logger.Info("frontend enabled, configuring Ethera Labs Console")
		}
		serviceManager.WithFrontend(overlays.frontend, overlays.frontendDev)
	}

	if err := o.waitForNetworkFiles(); err != nil {
		return nil, fmt.Errorf("required network files not ready: %w", err)
	}

	if err := serviceManager.StartAll(ctx, envVars); err != nil {
		return nil, fmt.Errorf("failed to start L2 services: %w", err)
	}

	// When flashblocks is enabled, use op-rbuilder RPC ports for contract deployment
	effectiveChainConfigs := cfg.ChainConfigs
	if cfg.Flashblocks.Enabled {
		effectiveChainConfigs = o.getFlashblocksChainConfigs(cfg)
		o.logger.Info("using flashblocks RPC ports for contract deployment",
			"rollup_a_port", effectiveChainConfigs[configs.L2ChainNameRollupA].RPCPort,
			"rollup_b_port", effectiveChainConfigs[configs.L2ChainNameRollupB].RPCPort)
	}

	contractDeployer := contracts.NewDeployer(o.networksDir)
	deployedContracts, err := contractDeployer.Deploy(ctx, effectiveChainConfigs, cfg.CoordinatorPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to deploy contracts: %w", err)
	}

	// envVars was built before contract deployment; refresh the entries that
	// depend on .localnet/networks/<chain>/contracts.json so downstream
	// services (sidecar, bundler, frontend) see the freshly deployed
	// addresses on restart / first build.
	if _, _, err := mailboxAddresses(deployedContracts); err != nil {
		return nil, fmt.Errorf("failed to resolve mailbox addresses: %w", err)
	}
	envBuilder.MergePostDeployEnv(envVars)

	if overlays.sidecar != "" {
		o.logger.Info("restarting sidecar services to apply mailbox configuration")
		if err := o.restartSidecar(ctx, dockerPath, overlays.flashblocks, overlays.sidecar, envVars); err != nil {
			return nil, fmt.Errorf("failed to restart sidecar services after contract deployment: %w", err)
		}
	}

	if overlays.bundler != "" {
		o.logger.Info("restarting bundler services to apply EntryPoint configuration")
		if err := o.restartBundler(ctx, dockerPath, overlays.flashblocks, overlays.bundler, envVars); err != nil {
			return nil, fmt.Errorf("failed to restart bundler services after contract deployment: %w", err)
		}
	}

	if overlays.opSuccinct != "" {
		o.logger.Info("restarting op-succinct services to apply mailbox configuration")
		if err := o.restartOpSuccinct(ctx, dockerPath, overlays.opSuccinct, envVars, deployedContracts); err != nil {
			return nil, fmt.Errorf("failed to restart op-succinct services after contract deployment: %w", err)
		}
	}

	if overlays.frontend != "" {
		o.logger.Info("building and starting Ethera Labs Console")
		// Label the validator EL node in the console as op-besu when that overlay is active.
		if overlays.opBesu != "" {
			envVars["EL_CLIENT_LABEL"] = "op-besu"
		}
		chainContracts := deployedContracts[configs.L2ChainNameRollupA]
		envVars["CONTRACT_BRIDGE_ADDRESS"] = chainContracts[contracts.ContractNameComposeL2ToL2Bridge].Hex()
		envVars["CONTRACT_TOKEN_ADDRESS"] = chainContracts[contracts.ContractNameTestToken].Hex()
		envVars["CONTRACT_CET_FACTORY_ADDRESS"] = chainContracts[contracts.ContractNameCetFactory].Hex()
		envVars["CONTRACT_ETH_LIQUIDITY_ADDRESS"] = chainContracts[contracts.ContractNameComposeETHLiquidity].Hex()

		dockerFiles := []string{dockerPath}
		if overlays.flashblocks != "" {
			dockerFiles = append(dockerFiles, overlays.flashblocks)
		}
		if overlays.sidecar != "" {
			dockerFiles = append(dockerFiles, overlays.sidecar)
		}

		if err := serviceManager.StartFrontend(ctx, dockerFiles, envVars); err != nil {
			return nil, fmt.Errorf("failed to start Ethera Labs Console: %w", err)
		}
	}

	o.logger.Info("Phase 3: L2 runtime operations completed successfully")

	return deployedContracts, nil
}

// resolveOverlayPaths writes all enabled overlay compose files to localnetDir and returns their paths.
func (o *Orchestrator) resolveOverlayPaths(cfg configs.L2) (overlayPaths, error) {
	var paths overlayPaths
	var err error

	// validator-el=op-besu: swap the op-reth validator EL (rollup-boost L2_URL) for op-besu.
	if cfg.ValidatorEL == configs.ValidatorELOpBesu {
		o.logger.Info("validator-el=op-besu: replacing op-reth validator EL with op-besu")
		paths.opBesu, err = docker.EnsureOpBesuComposeFile(o.localnetDir)
		if err != nil {
			return overlayPaths{}, fmt.Errorf("failed to prepare op-besu docker file: %w", err)
		}
	}

	if cfg.AltDA.Enabled {
		o.logger.Info("altDA enabled, configuring DA server services")
		paths.altDA, err = docker.EnsureAltDAComposeFile(o.localnetDir)
		if err != nil {
			return overlayPaths{}, fmt.Errorf("failed to prepare altDA docker file: %w", err)
		}
	}

	if cfg.OPSuccinct.Enabled {
		paths.opSuccinct, err = docker.EnsureOPSuccinctComposeFile(o.localnetDir)
		if err != nil {
			return overlayPaths{}, fmt.Errorf("failed to prepare op-succinct docker file: %w", err)
		}
	}

	if cfg.Flashblocks.Enabled {
		paths.flashblocks, err = docker.EnsureFlashblocksComposeFile(o.localnetDir)
		if err != nil {
			return overlayPaths{}, fmt.Errorf("failed to prepare flashblocks docker file: %w", err)
		}
	}

	if cfg.Sidecar.Enabled {
		if !cfg.Flashblocks.Enabled {
			return overlayPaths{}, fmt.Errorf("sidecar requires flashblocks to be enabled")
		}
		paths.sidecar, err = docker.EnsureSidecarComposeFile(o.localnetDir)
		if err != nil {
			return overlayPaths{}, fmt.Errorf("failed to prepare sidecar docker file: %w", err)
		}
	}

	if cfg.Bundler.Enabled {
		if !cfg.Flashblocks.Enabled {
			return overlayPaths{}, fmt.Errorf("bundler requires flashblocks to be enabled")
		}
		paths.bundler, err = docker.EnsureBundlerComposeFile(o.localnetDir)
		if err != nil {
			return overlayPaths{}, fmt.Errorf("failed to prepare bundler docker file: %w", err)
		}
	}

	if cfg.Frontend.Active() {
		if !cfg.Flashblocks.Enabled || !cfg.Sidecar.Enabled {
			return overlayPaths{}, fmt.Errorf("frontend requires flashblocks and sidecar to be enabled")
		}
		paths.frontend, err = docker.EnsureFrontendComposeFile(o.localnetDir)
		if err != nil {
			return overlayPaths{}, fmt.Errorf("failed to prepare frontend docker file: %w", err)
		}
		if cfg.Frontend.DevEnabled {
			paths.frontendDev, err = docker.EnsureFrontendDevComposeFile(o.localnetDir)
			if err != nil {
				return overlayPaths{}, fmt.Errorf("failed to prepare frontend dev docker file: %w", err)
			}
		}
	}

	return paths, nil
}

func (o *Orchestrator) waitForNetworkFiles() error {
	type fileSpec struct {
		path  string
		label string
	}
	files := []fileSpec{
		{
			path:  filepath.Join(o.networksDir, string(configs.L2ChainNameRollupA), genesis.GenesisFileName),
			label: "rollup-a genesis",
		},
		{
			path:  filepath.Join(o.networksDir, string(configs.L2ChainNameRollupA), secrets.JWTFileName),
			label: "rollup-a jwt",
		},
		{
			path:  filepath.Join(o.networksDir, string(configs.L2ChainNameRollupB), genesis.GenesisFileName),
			label: "rollup-b genesis",
		},
		{
			path:  filepath.Join(o.networksDir, string(configs.L2ChainNameRollupB), secrets.JWTFileName),
			label: "rollup-b jwt",
		},
	}

	deadline := time.Now().Add(120 * time.Second)
	for {
		missing := make([]fileSpec, 0, len(files))
		for _, f := range files {
			info, err := os.Stat(f.path)
			if err != nil || info.Size() == 0 {
				missing = append(missing, f)
			}
		}

		if len(missing) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			parts := make([]string, 0, len(missing))
			for _, f := range missing {
				parts = append(parts, fmt.Sprintf("%s(%s)", f.label, f.path))
			}
			return fmt.Errorf("missing files: %s", strings.Join(parts, " "))
		}

		time.Sleep(1 * time.Second)
	}
}

func (o *Orchestrator) restartOpSuccinct(ctx context.Context, dockerFilePath, opSuccinctDockerPath string, env map[string]string, deployedContracts map[configs.L2ChainName]map[contracts.ContractName]common.Address) error {
	mailboxA, mailboxB, err := mailboxAddresses(deployedContracts)
	if err != nil {
		return err
	}

	opsuccinctGenerator := opsuccinct.NewGenerator()
	if err := opsuccinctGenerator.SetMailboxAddress(mailboxA, filepath.Join(o.networksDir, string(configs.L2ChainNameRollupA))); err != nil {
		return fmt.Errorf("failed to update rollup-a opsuccinct env: %w", err)
	}
	if err := opsuccinctGenerator.SetMailboxAddress(mailboxB, filepath.Join(o.networksDir, string(configs.L2ChainNameRollupB))); err != nil {
		return fmt.Errorf("failed to update rollup-b opsuccinct env: %w", err)
	}

	services := []string{"op-succinct-a", "op-succinct-b"}
	// Keep op-node overlays intact.
	if err := docker.ComposeRestartNoDepsMultiFile(ctx, []string{dockerFilePath, opSuccinctDockerPath}, env, services...); err != nil {
		return fmt.Errorf("failed to restart op-succinct: %w", err)
	}

	return nil
}

func (o *Orchestrator) restartSidecar(ctx context.Context, dockerFilePath, flashblocksDockerPath, sidecarDockerPath string, env map[string]string) error {
	if sidecarDockerPath == "" {
		return fmt.Errorf("sidecar docker file path is empty")
	}

	dockerFiles := []string{dockerFilePath}
	if flashblocksDockerPath != "" {
		dockerFiles = append(dockerFiles, flashblocksDockerPath)
	}
	dockerFiles = append(dockerFiles, sidecarDockerPath)

	services := []string{"sidecar-a", "sidecar-b"}
	if err := docker.ComposeRestartNoDepsMultiFile(ctx, dockerFiles, env, services...); err != nil {
		return fmt.Errorf("failed to restart sidecar: %w", err)
	}

	return nil
}

// restartBundler restarts the ethera-bundler containers with the post-deployment
// env so they pick up the deployed EntryPoint address and the
// EntryPointSimulations runtime bytecode.
func (o *Orchestrator) restartBundler(ctx context.Context, dockerFilePath, flashblocksDockerPath, bundlerDockerPath string, env map[string]string) error {
	if bundlerDockerPath == "" {
		return fmt.Errorf("bundler docker file path is empty")
	}

	dockerFiles := []string{dockerFilePath}
	if flashblocksDockerPath != "" {
		dockerFiles = append(dockerFiles, flashblocksDockerPath)
	}
	dockerFiles = append(dockerFiles, bundlerDockerPath)

	services := []string{"bundler-a", "bundler-b"}
	if err := docker.ComposeRestartNoDepsMultiFile(ctx, dockerFiles, env, services...); err != nil {
		return fmt.Errorf("failed to restart bundler: %w", err)
	}

	return nil
}

func mailboxAddresses(deployedContracts map[configs.L2ChainName]map[contracts.ContractName]common.Address) (common.Address, common.Address, error) {
	mailboxA := deployedContracts[configs.L2ChainNameRollupA][contracts.ContractNameUniversalBridgeMailbox]
	mailboxB := deployedContracts[configs.L2ChainNameRollupB][contracts.ContractNameUniversalBridgeMailbox]
	if mailboxA == (common.Address{}) || mailboxB == (common.Address{}) {
		return common.Address{}, common.Address{}, fmt.Errorf("mailbox addresses not found in deployed contracts")
	}
	return mailboxA, mailboxB, nil
}

// buildComposeServices builds all enabled service images via docker-compose.
func (o *Orchestrator) buildComposeServices(ctx context.Context, dockerFilePath string, env map[string]string, overlays overlayPaths) error {
	services := []string{
		"publisher",
		"localnet-health",
	}

	dockerFiles := []string{dockerFilePath}

	if overlays.altDA != "" {
		dockerFiles = append(dockerFiles, overlays.altDA)
		services = append(services, "op-alt-da-a", "op-alt-da-b")
	}

	if overlays.opSuccinct != "" {
		dockerFiles = append(dockerFiles, overlays.opSuccinct)
		services = append(services, "op-succinct-a", "op-succinct-b")
	}

	// Sidecar requires flashblocks, so add flashblocks docker file first.
	if overlays.sidecar != "" {
		if overlays.flashblocks != "" {
			dockerFiles = append(dockerFiles, overlays.flashblocks)
		}
		dockerFiles = append(dockerFiles, overlays.sidecar)
		services = append(services, "op-rbuilder-a", "op-rbuilder-b", "sidecar-a", "sidecar-b")
	}

	// Bundler also requires flashblocks, so reuse the same overlay ordering.
	if overlays.bundler != "" {
		if overlays.flashblocks != "" && !slices.Contains(dockerFiles, overlays.flashblocks) {
			dockerFiles = append(dockerFiles, overlays.flashblocks)
		}
		dockerFiles = append(dockerFiles, overlays.bundler)
		services = append(services, "bundler-a", "bundler-b")
	}

	if len(dockerFiles) > 1 {
		if err := docker.ComposeBuildMultiFile(ctx, dockerFiles, env, services...); err != nil {
			return fmt.Errorf("failed to build docker services: %w", err)
		}
	} else {
		if err := docker.ComposeBuild(ctx, dockerFilePath, env, services...); err != nil {
			return fmt.Errorf("failed to build docker services: %w", err)
		}
	}

	return nil
}

// getFlashblocksChainConfigs returns chain configs with op-rbuilder RPC ports.
func (o *Orchestrator) getFlashblocksChainConfigs(cfg configs.L2) map[configs.L2ChainName]configs.Chain {
	result := make(map[configs.L2ChainName]configs.Chain)

	for chainName, chainCfg := range cfg.ChainConfigs {
		modifiedCfg := chainCfg
		switch chainName {
		case configs.L2ChainNameRollupA:
			if cfg.Flashblocks.RollupARPCPort > 0 {
				modifiedCfg.RPCPort = cfg.Flashblocks.RollupARPCPort
			}
		case configs.L2ChainNameRollupB:
			if cfg.Flashblocks.RollupBRPCPort > 0 {
				modifiedCfg.RPCPort = cfg.Flashblocks.RollupBRPCPort
			}
		}
		result[chainName] = modifiedCfg
	}

	return result
}
