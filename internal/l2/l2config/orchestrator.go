package l2config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethera-labs/local-testnet/internal/l2/infra/filesystem/json"
	"github.com/ethera-labs/local-testnet/internal/l2/l1deployment"
	"github.com/ethera-labs/local-testnet/internal/l2/l1deployment/deployer"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/contracts"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/crypto"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/genesis"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/opsuccinct"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/rollup"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/runtime"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/secrets"
	"github.com/ethera-labs/local-testnet/internal/logger"
)

// Orchestrator coordinates Phase 2: L2 configuration generation
//   - Generates genesis.json for each L2 chain
//   - Generates rollup.json for each L2 chain
//   - Generates JWT secrets and passwords
//   - Extracts L1 contract addresses from state.json
//   - Writes contracts.json for each chain
//   - Builds runtime environment variables for docker-compose
type Orchestrator struct {
	rootDir     string
	localnetDir string
	stateDir    string
	networksDir string
	servicesDir string
	logger      *slog.Logger
}

// NewOrchestrator creates a new Phase 2 orchestrator
func NewOrchestrator(rootDir, localnetDir, stateDir, networksDir, servicesDir string) *Orchestrator {
	return &Orchestrator{
		rootDir:     rootDir,
		localnetDir: localnetDir,
		stateDir:    stateDir,
		networksDir: networksDir,
		servicesDir: servicesDir,
		logger:      logger.Named("l2_config_orchestrator"),
	}
}

// Execute runs Phase 2: Generate all L2 configuration files
func (o *Orchestrator) Execute(ctx context.Context, cfg configs.L2, deploymentState l1deployment.DeploymentState) error {
	o.logger.Info("Phase 2: Starting L2 configuration generation")

	opDeployerBin, err := resolveBinaryL2Config(o.rootDir, "op-deployer")
	if err != nil {
		return fmt.Errorf("failed to locate op-deployer binary: %w", err)
	}
	opRethBin, err := resolveBinaryL2Config(o.rootDir, "op-reth")
	if err != nil {
		return fmt.Errorf("failed to locate op-reth binary: %w", err)
	}

	var (
		writer = json.NewWriter()

		opDeployer    = deployer.NewDeployer(o.rootDir, o.stateDir, opDeployerBin)
		genesisGen    = genesis.NewGenerator(opDeployer, writer, o.localnetDir, opRethBin)
		rollupGen     = rollup.NewGenerator(json.NewReader(), opDeployer, writer, o.localnetDir)
		secretsGen    = secrets.NewGenerator(writer)
		contractsGen  = contracts.NewGenerator(writer)
		opSuccinctGen = opsuccinct.NewGenerator()
		runtimeGen    = runtime.NewGenerator()
	)

	for chainName, chainConfig := range cfg.ChainConfigs {
		configPath := filepath.Join(o.networksDir, string(chainName))

		logger := o.logger.With("chain_name", chainName).With("chain_id", chainConfig.ID)
		logger.Info("generating l2 chain configuration")

		startBlock, ok := deploymentState.StartBlocks[chainName]
		if !ok {
			return fmt.Errorf("start block not found for chain %s", chainName)
		}

		sequencerAddress, err := crypto.AddressFromPrivateKey(cfg.CoordinatorPrivateKey)
		if err != nil {
			return fmt.Errorf("failed to derive sequencer address from coordinator PK for chain %d: %w", chainConfig.ID, err)
		}

		logger.Info("generating genesis file")
		genesisHash, err := genesisGen.Generate(
			ctx,
			chainConfig.ID,
			configPath,
			cfg.Wallet.Address,
			sequencerAddress,
			cfg.GenesisBalanceWei,
		)
		if err != nil {
			return fmt.Errorf("failed to generate genesis for chain %d: %w", chainConfig.ID, err)
		}

		err = rollupGen.Generate(ctx, chainConfig.ID, configPath, genesisHash, startBlock.Hash, startBlock.Number)
		if err != nil {
			return fmt.Errorf("failed to generate rollup for chain %d: %w", chainConfig.ID, err)
		}

		err = secretsGen.GenerateJWT(configPath)
		if err != nil {
			return fmt.Errorf("failed to generate JWT for chain %d: %w", chainConfig.ID, err)
		}

		if err := secretsGen.GeneratePassword(configPath); err != nil {
			return fmt.Errorf("failed to generate password for chain %d: %w", chainConfig.ID, err)
		}

		if err := contractsGen.GeneratePlaceholders(configPath, chainConfig.ID); err != nil {
			return fmt.Errorf("failed to generate contract placeholders for chain %d: %w", chainConfig.ID, err)
		}

		// TODO: `runtime.env` is passed to the OP Proposer service, so presumably it should take the OP DisputeGameFactoryAddress
		// rather than our own implementation of it.
		if err := runtimeGen.Generate(deploymentState.DisputeGameFactoryImplAddressOP, configPath); err != nil {
			return fmt.Errorf("failed to generate runtime file, %w", err)
		}

		if cfg.OPSuccinct.Enabled {
			if err := opSuccinctGen.Generate(
				deploymentState.ComposeL2OutputOracleAddress,
				deploymentState.DisputeGameFactoryAddress,
				configPath,
			); err != nil {
				return fmt.Errorf("failed to generate op-succinct file: %w", err)
			}
		}
	}

	o.logger.Info("Phase 2: L2 configuration generation completed successfully")

	return nil
}

// resolveBinaryL2Config locates a named binary: first in .localnet/bin/, then in PATH.
func resolveBinaryL2Config(rootDir, name string) (string, error) {
	localBin := filepath.Join(rootDir, ".localnet", "bin", name)
	if _, err := os.Stat(localBin); err == nil {
		return localBin, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%q not found in %s or PATH", name, filepath.Join(rootDir, ".localnet/bin"))
	}
	return p, nil
}
