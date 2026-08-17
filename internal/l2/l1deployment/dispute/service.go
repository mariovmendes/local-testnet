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
	"github.com/ethereum/go-ethereum/common"
)

//go:embed *.tmpl
var templatesFS embed.FS

// Service handles dispute game factory deployment
type Service struct {
	rootDir      string
	contractsDir string // Path to the ethera-contracts repo root (cloned or local)
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
		contractsDir: etheraContractsDir,
		deployerPK:   cfg.Wallet.PrivateKey,
		cfg:          cfg,
		logger:       logger.Named("dispute_deployer"),
	}
}

type DeploymentContracts struct {
	DisputeGameFactoryAddress    common.Address
	ComposeL2OutputOracleAddress common.Address
}

// Deploy executes the "Phase 1: Deploy Shared Infrastructure" workflow
// (justfile `l1-deploy-shared`) against the ethera-contracts repository and
// returns the deployed L1 contract addresses localnet needs in later phases.
//
// Unlike the legacy L1-settlement workflow, l1-deploy-shared deploys the
// shared dispute-game infrastructure once (not per-network) and has no
// equivalent of the old ComposeL2OutputOracle contract - only
// DisputeGameFactoryAddress is populated. ComposeL2OutputOracleAddress is
// always the zero address; callers that need it (op-succinct) must be
// disabled until the contracts repo grows a replacement.
func (s *Service) Deploy(ctx context.Context) (DeploymentContracts, error) {
	s.logger.Info("starting dispute contracts deployment")

	if _, err := os.Stat(s.contractsDir); os.IsNotExist(err) {
		return DeploymentContracts{}, fmt.Errorf("ethera-contracts directory not found at %s. Make sure the repository is cloned first", s.contractsDir)
	}

	if s.cfg.OPSuccinct.Enabled {
		return DeploymentContracts{}, fmt.Errorf("l2.op-succinct.enabled requires a ComposeL2OutputOracle address, which the current ethera-contracts l1-deploy-shared workflow does not produce")
	}

	s.logger.Info("updating config.json [l1] with shared-infra deployment inputs")
	if err := s.writeComposeConfig(); err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to update config.json: %w", err)
	}

	s.logger.Info("generating .env file")
	if err := s.generateEnvFile(); err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to generate .env file: %w", err)
	}

	s.logger.Info("installing forge dependencies")
	if err := s.runForgeCommand(ctx, "install"); err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to install forge dependencies: %w", err)
	}

	s.logger.Info("deploying shared L1 infrastructure")
	if err := s.deploySharedInfra(ctx); err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to deploy shared L1 infrastructure: %w", err)
	}

	s.logger.Info("parsing config.json [l1.deployed]")
	contracts, err := s.parseDeploymentContracts()
	if err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to parse deployment contracts: %w", err)
	}

	s.logger.With(
		"dispute_game_factory", contracts.DisputeGameFactoryAddress,
	).Info("dispute contracts deployed successfully")

	return contracts, nil
}

// composeL1Config mirrors the `l1` object in ethera-contracts' config.json
// (see script/l1/libraries/ComposeConfig.sol). Deployed is round-tripped
// untouched: it's written by the forge script itself when
// SAVE_DEPLOY_OUTPUT=true and must remain a valid (if placeholder) object
// for vm.writeJson to populate.
type composeL1Config struct {
	Guardian                        string          `json:"guardian"`
	ProxyAdminOwner                 string          `json:"proxyAdminOwner"`
	DefaultAdmin                    string          `json:"defaultAdmin"`
	DepositWhitelistAdmin           string          `json:"depositWhitelistAdmin"`
	AuthorizedProposer              string          `json:"authorizedProposer"`
	SP1Verifier                     string          `json:"sp1Verifier"`
	AggregationVkey                 string          `json:"aggregationVkey"`
	ProofMaturityDelaySeconds       int             `json:"proofMaturityDelaySeconds"`
	DisputeGameFinalityDelaySeconds int             `json:"disputeGameFinalityDelaySeconds"`
	DisputeGameInitBond             string          `json:"disputeGameInitBond"`
	Deployed                        json.RawMessage `json:"deployed"`
}

type composeDeployed struct {
	DisputeGameFactory string `json:"disputeGameFactory"`
}

// writeComposeConfig fills in config.json's `l1` static deployment inputs
// from l2.dispute, leaving `rollups` and `l1.deployed` untouched.
func (s *Service) writeComposeConfig() error {
	configPath := filepath.Join(s.contractsDir, "config.json")

	raw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config.json: %w", err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("failed to parse config.json: %w", err)
	}

	var l1 composeL1Config
	if l1Raw, ok := doc["l1"]; ok {
		if err := json.Unmarshal(l1Raw, &l1); err != nil {
			return fmt.Errorf("failed to parse config.json [l1]: %w", err)
		}
	}

	l1.Guardian = s.cfg.Dispute.GuardianAddress
	l1.ProxyAdminOwner = s.cfg.Dispute.OwnerAddress
	l1.DefaultAdmin = s.cfg.Dispute.OwnerAddress
	l1.DepositWhitelistAdmin = s.cfg.Dispute.OwnerAddress
	l1.AuthorizedProposer = s.cfg.Dispute.ProposerAddress
	l1.SP1Verifier = s.cfg.Dispute.VerifierAddress
	l1.AggregationVkey = s.cfg.Dispute.AggregationVkey
	l1.ProofMaturityDelaySeconds = s.cfg.Dispute.ProofMaturityDelaySeconds
	l1.DisputeGameFinalityDelaySeconds = s.cfg.Dispute.DisputeGameFinalityDelaySeconds
	l1.DisputeGameInitBond = s.cfg.Dispute.DisputeGameInitBond
	if len(l1.Deployed) == 0 {
		l1.Deployed = json.RawMessage("{}")
	}

	l1Bytes, err := json.Marshal(l1)
	if err != nil {
		return fmt.Errorf("failed to marshal config.json [l1]: %w", err)
	}
	doc["l1"] = l1Bytes

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config.json: %w", err)
	}

	if err := os.WriteFile(configPath, out, 0644); err != nil {
		return fmt.Errorf("failed to write config.json: %w", err)
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
		ProxyAdminOwnerKey string
		GuardianKey        string
		RPCURL             string
	}{
		// proxyAdminOwner and guardian are both configured to the same
		// deployer wallet address (l2.dispute.owner-address /
		// l2.dispute.guardian-address); there's no separate key material
		// for them.
		ProxyAdminOwnerKey: s.deployerPK,
		GuardianKey:        s.deployerPK,
		RPCURL:             s.cfg.L1ElURL,
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

// deploySharedInfra runs DeploySharedInfra.s.sol directly with forge script,
// rather than via `just l1-deploy-shared`. The justfile recipe omits --sig,
// and DeploySharedInfra declares both run() and run(DeploySharedInfraInput),
// which forge refuses to disambiguate on its own ("Multiple functions with
// the same name `run` found in the ABI").
func (s *Service) deploySharedInfra(ctx context.Context) error {
	args := []string{
		"script", "script/l1/deploy/DeploySharedInfra.s.sol",
		"--tc", "DeploySharedInfra",
		"--sig", "run()",
		"--rpc-url", s.cfg.L1ElURL,
		"--broadcast",
		"--slow",
	}

	cmd := exec.CommandContext(ctx, "forge", args...)
	cmd.Dir = s.contractsDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"SAVE_DEPLOY_OUTPUT=true",
		"PROXY_ADMIN_OWNER_KEY="+s.deployerPK,
		"GUARDIAN_KEY="+s.deployerPK,
	)

	s.logger.
		With("command", "forge "+strings.Join(args, " ")).
		With("working_dir", s.contractsDir).
		Info("executing forge script")

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("forge script DeploySharedInfra failed in directory %s: %w", s.contractsDir, err)
	}

	return nil
}

// runForgeCommand executes a forge command in the contracts directory
func (s *Service) runForgeCommand(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "forge", args...)
	cmd.Dir = s.contractsDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command 'forge %s' failed in directory %s: %w", strings.Join(args, " "), s.contractsDir, err)
	}

	return nil
}

// parseDeploymentContracts reads the addresses DeploySharedInfra wrote into
// config.json's l1.deployed section.
func (s *Service) parseDeploymentContracts() (DeploymentContracts, error) {
	configPath := filepath.Join(s.contractsDir, "config.json")

	raw, err := os.ReadFile(configPath)
	if err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to read config.json: %w", err)
	}

	var doc struct {
		L1 struct {
			Deployed composeDeployed `json:"deployed"`
		} `json:"l1"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return DeploymentContracts{}, fmt.Errorf("failed to parse config.json: %w", err)
	}

	if doc.L1.Deployed.DisputeGameFactory == "" || common.HexToAddress(doc.L1.Deployed.DisputeGameFactory) == (common.Address{}) {
		return DeploymentContracts{}, fmt.Errorf("l1.deployed.disputeGameFactory is empty in config.json")
	}

	return DeploymentContracts{
		DisputeGameFactoryAddress: common.HexToAddress(doc.L1.Deployed.DisputeGameFactory),
	}, nil
}
