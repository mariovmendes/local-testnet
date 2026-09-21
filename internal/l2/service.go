package l2

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethera-labs/local-testnet/internal/l2/blockscout"
	"github.com/ethera-labs/local-testnet/internal/l2/infra/git"
	"github.com/ethera-labs/local-testnet/internal/l2/l1deployment"
	"github.com/ethera-labs/local-testnet/internal/l2/l2runtime/contracts"
	"github.com/ethera-labs/local-testnet/internal/logger"
	"github.com/ethereum/go-ethereum/common"
)

// Service orchestrates the entire L2 deployment process
type (
	cloner interface {
		CloneAll(ctx context.Context, baseDir string, repos []git.Repository) error
		CheckoutBranch(ctx context.Context, repoPath, branch string) error
	}
	l1Orchestrator interface {
		Execute(ctx context.Context, cfg configs.L2) (l1deployment.DeploymentState, error)
	}
	l2ConfigOrchestrator interface {
		Execute(ctx context.Context, cfg configs.L2, state l1deployment.DeploymentState) error
	}
	l2RuntimeOrchestrator interface {
		Execute(ctx context.Context, cfg configs.L2, disputeGameFactory common.Address, composeL2OOAddr common.Address) (map[configs.L2ChainName]map[contracts.ContractName]common.Address, error)
	}
	blockscoutService interface {
		Run(ctx context.Context, rollupConfigs []blockscout.RollupConfig, l1RPCURL string, l1BeaconURL string) error
	}
	outputGenerator interface {
		Generate(context.Context, map[configs.L2ChainName]map[contracts.ContractName]common.Address) error
	}

	Service struct {
		rootDir               string
		cloner                cloner
		l1Orchestrator        l1Orchestrator
		l2ConfigOrchestrator  l2ConfigOrchestrator
		l2RuntimeOrchestrator l2RuntimeOrchestrator
		blockscoutService     blockscoutService
		outputGenerator       outputGenerator
		logger                *slog.Logger
	}
)

// NewService creates a new l2 service
func NewService(
	rootDir string,
	cloner cloner,
	l1Orchestrator l1Orchestrator,
	l2ConfigOrchestrator l2ConfigOrchestrator,
	l2RuntimeOrchestrator l2RuntimeOrchestrator,
	blockscoutService blockscoutService,
	outputGenerator outputGenerator) *Service {
	return &Service{
		rootDir:               rootDir,
		cloner:                cloner,
		l1Orchestrator:        l1Orchestrator,
		l2ConfigOrchestrator:  l2ConfigOrchestrator,
		l2RuntimeOrchestrator: l2RuntimeOrchestrator,
		blockscoutService:     blockscoutService,
		outputGenerator:       outputGenerator,
		logger:                logger.Named("l2_service"),
	}
}

func (s *Service) Deploy(ctx context.Context, cfg configs.L2) error {
	s.logger.Info("starting L2 deployment process")

	if err := s.cloneRepositories(ctx, cfg); err != nil {
		return fmt.Errorf("failed to clone repositories: %w", err)
	}

	s.logger.Info("running phase 1 - L1 deployments")
	deploymentState, err := s.l1Orchestrator.Execute(ctx, cfg)
	if err != nil {
		return fmt.Errorf("phase 1 failed: %w", err)
	}

	s.logger.Info("running phase 2 - L2 config generation", "deployment_state", deploymentState)
	err = s.l2ConfigOrchestrator.Execute(ctx, cfg, deploymentState)
	if err != nil {
		return fmt.Errorf("phase 2 failed: %w", err)
	}

	s.logger.Info("running phase 3 - L2 launch")
	deployedContracts, err := s.l2RuntimeOrchestrator.Execute(ctx, cfg, deploymentState.DisputeGameFactoryAddress, deploymentState.ComposeL2OutputOracleAddress)
	if err != nil {
		return fmt.Errorf("phase 3 failed: %w", err)
	}

	if cfg.Blockscout.Enabled {
		s.logger.Info("blockscout is enabled. Starting Blockscout services")
		chainConfigs, err := generateBlockscoutConfig(cfg, deploymentState)
		if err != nil {
			return fmt.Errorf("failed to generate Blockscout chain configs: %w", err)
		}

		if err := s.blockscoutService.Run(ctx, chainConfigs, cfg.L1ElURL, cfg.L1ClURL); err != nil {
			return fmt.Errorf("failed to start Blockscout service: %w", err)
		}
	} else {
		s.logger.Info("Blockscout is disabled. Skipping Blockscout services")
	}

	s.logger.Info("L2 deployment completed successfully. Generating output file")

	if err := s.outputGenerator.Generate(ctx, deployedContracts); err != nil {
		return fmt.Errorf("failed to generate output file: %w", err)
	}

	s.logger.Info("output file generated successfully")

	return nil
}

func generateBlockscoutConfig(cfg configs.L2, deploymentState l1deployment.DeploymentState) ([]blockscout.RollupConfig, error) {
	chainOrder := []configs.L2ChainName{
		configs.L2ChainNameRollupA,
		configs.L2ChainNameRollupB,
	}
	chainConfigs := make([]blockscout.RollupConfig, 0, len(chainOrder))

	for _, chainName := range chainOrder {
		config, ok := cfg.ChainConfigs[chainName]
		if !ok {
			return nil, fmt.Errorf("chain config not found for %s", chainName)
		}

		var hostName string
		switch chainName {
		case configs.L2ChainNameRollupA:
			hostName = "validator-el-a"
		case configs.L2ChainNameRollupB:
			hostName = "validator-el-b"
		default:
			return nil, fmt.Errorf("unknown chain name: %s", chainName)
		}

		systemConfigAddr, ok := deploymentState.SystemConfigProxyAddresses[chainName]
		if !ok {
			return nil, fmt.Errorf("SystemConfigProxy address not found for chain %s", chainName)
		}

		chainConfigs = append(chainConfigs, blockscout.RollupConfig{
			ID:                    config.ID,
			Name:                  chainName,
			ELHostName:            hostName,
			RPCPort:               8545,
			WSPort:                8546,
			SystemConfigProxyAddr: systemConfigAddr,
		})
	}

	return chainConfigs, nil
}

// cloneRepositories clones all required git repositories
func (s *Service) cloneRepositories(ctx context.Context, cfg configs.L2) error {
	s.logger.Info("cloning required repositories")

	repos := make([]git.Repository, 0, len(cfg.Repositories))

	for name, repo := range cfg.Repositories {
		if name == configs.RepositoryNameOpRbuilder && !cfg.Flashblocks.Enabled {
			continue
		}
		if name == configs.RepositoryNameSidecar && !cfg.Sidecar.Enabled {
			continue
		}
		if name == configs.RepositoryNameOPSuccinct && !cfg.OPSuccinct.Enabled {
			s.logger.With("name", name).Info("ignoring op-succinct repository config because op-succinct mode is disabled")
			continue
		}
		if (name == configs.RepositoryNameBundler || name == configs.RepositoryNameAccountAbstraction) && !cfg.Bundler.Enabled {
			s.logger.With("name", name).Info("ignoring bundler repository config because bundler mode is disabled")
			continue
		}

		if repo.URL != "" {
			repos = append(repos, git.Repository{
				Name: string(name),
				URL:  repo.URL,
				Ref:  repo.Branch,
			})
			continue
		}

		if repo.LocalPath != "" {
			absPath, err := filepath.Abs(repo.LocalPath)
			if err != nil {
				return fmt.Errorf("failed to resolve absolute path for local repository %s: %w", name, err)
			}
			s.logger.With("name", name, "local_path", repo.LocalPath, "resolved_path", absPath).Info("using local repository path; skipping clone")
			if repo.Branch != "" {
				if err := s.cloner.CheckoutBranch(ctx, absPath, repo.Branch); err != nil {
					return fmt.Errorf("local repository %s: %w", name, err)
				}
			}
			continue
		}

		return fmt.Errorf("repository %s has neither URL nor local-path set", name)
	}

	l2Dir := filepath.Join(s.rootDir, localnetDirName, servicesDirName)
	if err := s.cloner.CloneAll(ctx, l2Dir, repos); err != nil {
		return fmt.Errorf("failed to clone repositories: %w", err)
	}

	s.logger.Info("repositories cloned successfully")

	return nil
}
