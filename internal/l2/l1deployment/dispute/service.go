package dispute

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethera-labs/local-testnet/internal/logger"
	"github.com/ethera-labs/local-testnet/internal/logfilter"
	"github.com/ethereum/go-ethereum/common"
)

//go:embed *.tmpl
var templatesFS embed.FS

// Service handles dispute game factory deployment
type Service struct {
	rootDir      string
	contractsDir string // Path to L1-settlement subdir of the ethera-contracts repo (cloned or local)
	deployerPK   string
	cfg          configs.L2
	logger       *slog.Logger
}

// NewService creates a new dispute deployment service.
// etheraContractsDir is the resolved path to the ethera-contracts repository root,
// honoring both clone (.localnet/services/...) and local-path configurations.
func NewService(rootDir, etheraContractsDir string, cfg configs.L2) *Service {
	return &Service{
		rootDir:      rootDir,
		contractsDir: filepath.Join(etheraContractsDir, "L1-settlement"),
		deployerPK:   cfg.Wallet.PrivateKey,
		cfg:          cfg,
		logger:       logger.Named("dispute_deployer"),
	}
}

type DeploymentContracts struct {
	DisputeGameFactoryAddress    common.Address
	ComposeL2OutputOracleAddress common.Address
}

// Deploy executes the dispute-contract deployment workflow and returns the
// deployed L1 contract addresses localnet needs in later phases.
func (s *Service) Deploy(ctx context.Context) (DeploymentContracts, error) {
	s.logger.Info("starting dispute contracts deployment")

	if _, err := os.Stat(s.contractsDir); os.IsNotExist(err) {
		return DeploymentContracts{}, fmt.Errorf("L1-settlement directory not found at %s. Make sure ethera-contracts repository is cloned first", s.contractsDir)
	}

	s.logger.Info("generating networks.toml")
	if err := s.generateNetworksToml(); err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to generate networks.toml: %w", err)
	}

	s.logger.Info("generating .env file")
	if err := s.generateEnvFile(); err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to generate .env file: %w", err)
	}

	s.logger.Info("running just setup")
	if err := s.runJustCommand(ctx, "setup"); err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to run just setup: %w", err)
	}

	s.logger.Info("running just build")
	if err := s.runJustCommand(ctx, "build"); err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to run just build: %w", err)
	}

	s.logger.Info("running just deploy")
	if err := s.runJustCommand(ctx, "deploy-network", s.cfg.Dispute.NetworkName); err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to deploy network '%s': %w", s.cfg.Dispute.NetworkName, err)
	}

	s.logger.Info("parsing deployments.json")
	contracts, err := s.parseDeploymentContracts()
	if err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to parse deployment contracts: %w", err)
	}

	s.logger.With(
		"dispute_game_factory", contracts.DisputeGameFactoryAddress,
		"compose_l2_output_oracle", contracts.ComposeL2OutputOracleAddress,
	).Info("dispute contracts deployed successfully")

	return contracts, nil
}

// generateNetworksToml creates networks.toml from template and config
func (s *Service) generateNetworksToml() error {
	tmplContent, err := templatesFS.ReadFile("networks.tmpl")
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	tmpl, err := template.New("networks").Parse(string(tmplContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	type templateData struct {
		NetworkName                     string
		RpcURL                          string
		ChainID                         int
		ExplorerURL                     string
		ExplorerAPIURL                  string
		VerifierAddress                 string
		OwnerAddress                    string
		ProposerAddress                 string
		AggregationVkey                 string
		GuardianAddress                 string
		ProofMaturityDelaySeconds       int
		DisputeGameFinalityDelaySeconds int
		DisputeGameInitBond             string
		DeployerAddress                 string
	}

	// forge/just scripts run natively on the host, not inside Docker, so
	// host.docker.internal doesn't resolve. Swap it for 127.0.0.1.
	nativeL1URL := strings.ReplaceAll(s.cfg.L1ElURL, "host.docker.internal", "127.0.0.1")

	data := templateData{
		NetworkName:                     s.cfg.Dispute.NetworkName,
		RpcURL:                          nativeL1URL,
		ChainID:                         s.cfg.L1ChainID,
		ExplorerURL:                     s.cfg.Dispute.ExplorerURL,
		ExplorerAPIURL:                  s.cfg.Dispute.ExplorerAPIURL,
		VerifierAddress:                 s.cfg.Dispute.VerifierAddress,
		OwnerAddress:                    s.cfg.Dispute.OwnerAddress,
		ProposerAddress:                 s.cfg.Dispute.ProposerAddress,
		AggregationVkey:                 s.cfg.Dispute.AggregationVkey,
		GuardianAddress:                 s.cfg.Dispute.GuardianAddress,
		ProofMaturityDelaySeconds:       s.cfg.Dispute.ProofMaturityDelaySeconds,
		DisputeGameFinalityDelaySeconds: s.cfg.Dispute.DisputeGameFinalityDelaySeconds,
		DisputeGameInitBond:             s.cfg.Dispute.DisputeGameInitBond,
		// DeployerAddress must be the ProxyAdmin owner: ProxyAdmin.upgradeAndCall is called
		// in the same broadcast (same msg.sender = deployer), so admin != deployer => revert.
		DeployerAddress: s.cfg.Wallet.Address,
	}

	outputPath := filepath.Join(s.contractsDir, "networks.toml")
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create networks.toml: %w", err)
	}
	defer file.Close()

	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}

// generateEnvFile creates .env file from template
func (s *Service) generateEnvFile() error {
	tmplContent, err := templatesFS.ReadFile("env.tmpl")
	if err != nil {
		return fmt.Errorf("failed to read env template file: %w", err)
	}

	tmpl, err := template.New("env").Parse(string(tmplContent))
	if err != nil {
		return fmt.Errorf("failed to parse env template: %w", err)
	}

	data := struct {
		DeployerPrivateKey string
	}{
		DeployerPrivateKey: s.deployerPK,
	}

	envPath := filepath.Join(s.contractsDir, ".env")
	file, err := os.Create(envPath)
	if err != nil {
		return fmt.Errorf("failed to create .env file: %w", err)
	}
	defer file.Close()

	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("failed to execute env template: %w", err)
	}

	if err := os.Chmod(envPath, 0600); err != nil {
		return fmt.Errorf("failed to set .env file permissions: %w", err)
	}

	return nil
}

// runJustCommand executes a just command in the contracts directory
func (s *Service) runJustCommand(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "just", args...)
	cmd.Dir = s.contractsDir
	// MOCK_MODE is consumed by scripts/deploy.sh in the ethera-contracts repo
	// (deploy-network target): when true, it deploys a MockVerifier and uses
	// it in place of NETWORK_VERIFIER_ADDRESS. Passed explicitly (rather than
	// relying on shell inheritance) so the Go-level --mock-mode flag is
	// authoritative regardless of how this process was launched.
	cmd.Env = append(os.Environ(), fmt.Sprintf("MOCK_MODE=%t", s.cfg.MockMode))
	forgeOut := logfilter.NewForgeWriter(os.Stdout)
	cmd.Stdout = forgeOut
	cmd.Stderr = forgeOut

	s.logger.
		With("command", fmt.Sprintf("just %s", strings.Join(args, " "))).
		With("working_dir", s.contractsDir).
		Info("executing just command")

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command 'just %s' failed in directory %s: %w", strings.Join(args, " "), s.contractsDir, err)
	}

	return nil
}

// parseDeploymentContracts reads the contract deployment outputs produced by
// ethera-contracts and extracts the proxy addresses localnet needs later.
func (s *Service) parseDeploymentContracts() (DeploymentContracts, error) {
	deploymentsPath := filepath.Join(s.contractsDir, "deployments.json")

	if data, err := os.ReadFile(deploymentsPath); err == nil {
		var deployments map[string]struct {
			ComposeL2OutputOracle struct {
				Proxy string `json:"proxy"`
			} `json:"ComposeL2OutputOracle"`
			DisputeGameFactory struct {
				Proxy string `json:"proxy"`
			} `json:"DisputeGameFactory"`
		}

		if err := json.Unmarshal(data, &deployments); err == nil {
			if network, ok := deployments[s.cfg.Dispute.NetworkName]; ok {
				if network.DisputeGameFactory.Proxy == "" {
					return DeploymentContracts{}, fmt.Errorf("DisputeGameFactory proxy address is empty")
				}
				return DeploymentContracts{
					DisputeGameFactoryAddress:    common.HexToAddress(network.DisputeGameFactory.Proxy),
					ComposeL2OutputOracleAddress: common.HexToAddress(network.ComposeL2OutputOracle.Proxy),
				}, nil
			}
		}
	}

	// Fallback to ethera deployment layout: deployments/ethera/<network>.json
	etheraPath := filepath.Join(s.contractsDir, "deployments", "compose", s.cfg.Dispute.NetworkName+".json")
	data, err := os.ReadFile(etheraPath)
	if err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to read deployments.json or compose deployments: %w", err)
	}

	var etheraDeployments map[string]struct {
		Contracts struct {
			ComposeL2OutputOracle struct {
				ProxyAddress string `json:"proxyAddress"`
			} `json:"ComposeL2OutputOracle"`
			DisputeGameFactory struct {
				ProxyAddress string `json:"proxyAddress"`
			} `json:"DisputeGameFactory"`
		} `json:"contracts"`
	}
	if err := json.Unmarshal(data, &etheraDeployments); err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to parse compose deployments file: %w", err)
	}

	network, ok := etheraDeployments[s.cfg.Dispute.NetworkName]
	if !ok {
		return DeploymentContracts{}, fmt.Errorf("%s deployment not found in compose deployments file", s.cfg.Dispute.NetworkName)
	}

	if network.Contracts.DisputeGameFactory.ProxyAddress == "" {
		return DeploymentContracts{}, fmt.Errorf("DisputeGameFactory proxy address is empty")
	}

	return DeploymentContracts{
		DisputeGameFactoryAddress:    common.HexToAddress(network.Contracts.DisputeGameFactory.ProxyAddress),
		ComposeL2OutputOracleAddress: common.HexToAddress(network.Contracts.ComposeL2OutputOracle.ProxyAddress),
	}, nil
}
