package l1deployment

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethera-labs/local-testnet/internal/l2/infra/docker"
	"github.com/ethera-labs/local-testnet/internal/l2/infra/filesystem/json"
	"github.com/ethera-labs/local-testnet/internal/l2/l1deployment/deployer"
	"github.com/ethera-labs/local-testnet/internal/l2/l1deployment/dispute"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/crypto"
	"github.com/ethera-labs/local-testnet/internal/logger"
	"github.com/ethereum/go-ethereum/common"
)

/*
Orchestrator coordinates Phase 1: L1 deployment
  - Initializes op-deployer state and writes intent.toml
  - Deploys OP Stack L1 contracts to the L1 chain
  - Outputs state.json with contract addresses
*/
type (
	DeploymentState struct {
		DisputeGameFactoryAddress       common.Address
		ComposeL2OutputOracleAddress    common.Address
		DisputeGameFactoryImplAddressOP common.Address //TODO: Determine the necessity of this variable's usage.
		StartBlocks                     map[configs.L2ChainName]StartBlock
		SystemConfigProxyAddresses      map[configs.L2ChainName]common.Address
	}

	StartBlock struct {
		Hash   string
		Number string
	}

	Orchestrator struct {
		rootDir     string
		stateDir    string
		servicesDir string
		logger      *slog.Logger
	}
)

// NewOrchestrator creates a new Phase 1 orchestrator
func NewOrchestrator(rootDir, stateDir, servicesDir string) *Orchestrator {
	return &Orchestrator{
		rootDir:     rootDir,
		stateDir:    stateDir,
		servicesDir: servicesDir,
		logger:      logger.Named("l1_orchestrator"),
	}
}

// Execute runs Phase 1: Deploy L1 contracts and dispute contracts
// Returns deployment state with DisputeGameFactory address
func (o *Orchestrator) Execute(ctx context.Context, cfg configs.L2) (DeploymentState, error) {
	o.logger.Info("Phase 1: Starting L1 deployment")

	var deploymentState DeploymentState
	stateManager := deployer.NewStateManager(o.stateDir, json.NewReader())

	o.logger.Info("ensuring state directory created")
	if err := stateManager.EnsureStateDir(); err != nil {
		return deploymentState, fmt.Errorf("failed to ensure state directory: %w", err)
	}

	opDeployerBin, err := resolveBinary(o.rootDir, "op-deployer")
	if err != nil {
		return deploymentState, fmt.Errorf("failed to locate op-deployer binary: %w", err)
	}

	o.logger.Info("instantiating Deployer")
	opDeployer := deployer.NewDeployer(o.rootDir, o.stateDir, opDeployerBin)

	o.logger.Info("initializing Deployer")
	if err := opDeployer.Init(ctx, cfg.L1ChainID, cfg.ChainConfigs); err != nil {
		return deploymentState, fmt.Errorf("failed to initialize op-deployer state: %w", err)
	}

	o.logger.Info("converting coordinator PK to address")
	coordinatorAddress, err := crypto.AddressFromPrivateKey(cfg.CoordinatorPrivateKey)
	if err != nil {
		return deploymentState, fmt.Errorf("failed to derive coordinator address: %w", err)
	}

	o.logger.Info("generating intent file")
	intentWriter := deployer.NewIntentWriter(o.stateDir, json.NewWriter())
	if err := intentWriter.WriteIntent(
		cfg.Wallet.Address,
		coordinatorAddress,
		cfg.L1ChainID,
		cfg.ChainConfigs,
		cfg.AltDA,
	); err != nil {
		return deploymentState, fmt.Errorf("failed to write intent: %w", err)
	}

	if err := opDeployer.Apply(ctx, cfg.L1ElURL, cfg.Wallet.PrivateKey, cfg.DeploymentTarget); err != nil {
		return deploymentState, fmt.Errorf("failed to deploy L1 contracts: %w", err)
	}

	opState, err := stateManager.Load()
	if err != nil {
		return deploymentState, fmt.Errorf("failed to load OP deployment state: %w", err)
	}
	if cfg.AltDA.Enabled {
		altDAConfig, patchState, err := resolveAltDAState(cfg.AltDA, opState)
		if err != nil {
			return deploymentState, err
		}
		if patchState {
			if err := stateManager.PatchAltDAState(altDAConfig); err != nil {
				return deploymentState, fmt.Errorf("failed to patch AltDA state: %w", err)
			}
			opState, err = stateManager.Load()
			if err != nil {
				return deploymentState, fmt.Errorf("failed to reload patched OP deployment state: %w", err)
			}
		}
	}

	o.logger.Info("deploying dispute contracts")
	envBuilder := docker.NewEnvBuilder(o.rootDir, "", o.servicesDir)
	etheraContractsDir, err := envBuilder.ResolveRepoPath(
		cfg.Repositories[configs.RepositoryNameEtheraContracts],
		configs.RepositoryNameEtheraContracts,
	)
	if err != nil {
		return deploymentState, fmt.Errorf("failed to resolve ethera-contracts path: %w", err)
	}
	disputeService := dispute.NewService(o.rootDir, etheraContractsDir, cfg)
	disputeContracts, err := disputeService.Deploy(ctx)
	if err != nil {
		return deploymentState, fmt.Errorf("failed to deploy dispute contracts: %w", err)
	}

	o.logger.With(
		"game_factory_address", disputeContracts.DisputeGameFactoryAddress,
		"compose_l2_output_oracle_address", disputeContracts.ComposeL2OutputOracleAddress,
	).Info("Phase 1: L1 deployment completed successfully")

	startBlocks := make(map[configs.L2ChainName]StartBlock)
	systemConfigProxyAddresses := make(map[configs.L2ChainName]common.Address)
	for _, opChain := range opState.OpChainDeployments {
		chainIDInt, err := strconv.ParseInt(opChain.ID, 0, 64)
		if err != nil {
			return deploymentState, fmt.Errorf("failed to parse chain ID %s: %w", opChain.ID, err)
		}

		for chainName, chainConfig := range cfg.ChainConfigs {
			if int64(chainConfig.ID) == chainIDInt {
				startBlocks[chainName] = StartBlock{
					Hash:   opChain.StartBlock.Hash,
					Number: opChain.StartBlock.Number,
				}
				systemConfigProxyAddresses[chainName] = common.HexToAddress(opChain.SystemConfigProxy)
				break
			}
		}
	}

	deploymentState = DeploymentState{
		DisputeGameFactoryAddress:       disputeContracts.DisputeGameFactoryAddress,
		ComposeL2OutputOracleAddress:    disputeContracts.ComposeL2OutputOracleAddress,
		DisputeGameFactoryImplAddressOP: common.HexToAddress(opState.ImplementationsDeployment.DisputeGameFactoryImplAddress),
		StartBlocks:                     startBlocks,
		SystemConfigProxyAddresses:      systemConfigProxyAddresses,
	}

	return deploymentState, nil
}

// resolveBinary locates a named binary: first in .localnet/bin/, then in PATH.
func resolveBinary(rootDir, name string) (string, error) {
	localBin := filepath.Join(rootDir, ".localnet", "bin", name)
	if _, err := os.Stat(localBin); err == nil {
		return localBin, nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%q not found in %s or PATH", name, filepath.Join(rootDir, ".localnet/bin"))
	}
	return path, nil
}

func resolveAltDAState(altDA configs.AltDAConfig, opState *deployer.OPDeploymentState) (configs.AltDAConfig, bool, error) {
	if altDA.SkipL1Deploy && altDA.ChallengeProxyAddress != "" && altDA.ChallengeImplAddress != "" {
		return altDA, true, nil
	}
	if altDA.SkipL1Deploy {
		return altDA, false, nil
	}

	for _, chain := range opState.OpChainDeployments {
		proxy := common.HexToAddress(chain.AltDAChallengeProxy)
		impl := common.HexToAddress(chain.AltDAChallengeImpl)
		if proxy == (common.Address{}) || impl == (common.Address{}) {
			continue
		}
		altDA.ChallengeProxyAddress = proxy.Hex()
		altDA.ChallengeImplAddress = impl.Hex()
		return altDA, true, nil
	}

	return altDA, false, fmt.Errorf("AltDA is enabled but no deployed AltDA challenge contracts were found in state.json after op-deployer apply")
}
