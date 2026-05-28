package deployer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethera-labs/local-testnet/internal/logger"
)

// Deployer wraps the op-deployer binary, running it directly on the host.
// Running natively (not in Docker) lets op-deployer reach the Kurtosis L1 at
// 127.0.0.1 without any Docker-network translation.
type Deployer struct {
	rootDir    string
	stateDir   string
	binaryPath string // absolute path to the op-deployer binary
	logger     *slog.Logger
}

// NewDeployer creates a new op-deployer wrapper.
// binaryPath is the path to the op-deployer binary on the host (e.g. from $PATH or .localnet/bin/).
func NewDeployer(rootDir, stateDir, binaryPath string) *Deployer {
	return &Deployer{
		rootDir:    rootDir,
		stateDir:   stateDir,
		binaryPath: binaryPath,
		logger:     logger.Named("deployer"),
	}
}

// Init initializes the op-deployer state directory.
// It is idempotent: if state.json already exists the init is skipped.
func (o *Deployer) Init(ctx context.Context, l1ChainID int, l2Chains map[configs.L2ChainName]configs.Chain) error {
	o.logger.With("state_dir", o.stateDir).Info("initializing deployer state")

	stateFilePath := filepath.Join(o.stateDir, "state.json")
	if _, err := os.Stat(stateFilePath); err == nil {
		o.logger.With("file_name", stateFilePath).Info("state already exists, skipping init")
		return nil
	}

	var chainIDsStr []string
	for _, chainConfig := range l2Chains {
		chainIDsStr = append(chainIDsStr, fmt.Sprintf("%d", chainConfig.ID))
	}

	cmd := exec.CommandContext(ctx, o.binaryPath,
		"init",
		"--intent-type", "custom",
		"--l1-chain-id", strconv.Itoa(l1ChainID),
		"--l2-chain-ids", strings.Join(chainIDsStr, ","),
	)
	cmd.Dir = o.stateDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("HOME=%s", o.stateDir),
		fmt.Sprintf("DEPLOYER_CACHE_DIR=%s/.cache", o.stateDir),
	)

	o.logger.Info("running op-deployer init")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run op-deployer init: %w", err)
	}

	o.logger.Info("deployer state initialized successfully")
	return nil
}

// Apply deploys L1 contracts by running op-deployer apply against the L1 RPC.
// l1RpcURL is used as-is — running natively, 127.0.0.1 reaches Kurtosis directly.
func (o *Deployer) Apply(ctx context.Context, l1RpcURL, deployerPrivateKey, deploymentTarget string) error {
	o.logger.With("deployment_target", deploymentTarget).Info("running deployer apply")

	cmd := exec.CommandContext(ctx, o.binaryPath,
		"apply",
		"--deployment-target", deploymentTarget,
	)
	cmd.Dir = o.stateDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("HOME=%s", o.stateDir),
		fmt.Sprintf("DEPLOYER_CACHE_DIR=%s/.cache", o.stateDir),
		fmt.Sprintf("L1_RPC_URL=%s", l1RpcURL),
		fmt.Sprintf("DEPLOYER_PRIVATE_KEY=%s", deployerPrivateKey),
	)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run op-deployer apply: %w", err)
	}

	o.logger.Info("deployer apply completed successfully")
	return nil
}

// InspectGenesis exports genesis JSON for a chain.
func (o *Deployer) InspectGenesis(ctx context.Context, chainID int) (string, error) {
	cmd := exec.CommandContext(ctx, o.binaryPath,
		"inspect", "genesis",
		fmt.Sprintf("%d", chainID),
	)
	cmd.Dir = o.stateDir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("HOME=%s", o.stateDir),
	)

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to run op-deployer inspect genesis: %w", err)
	}

	return stdout.String(), nil
}

// InspectRollup exports rollup config for a chain to outputPath.
func (o *Deployer) InspectRollup(ctx context.Context, chainID int, outputPath string) error {
	cmd := exec.CommandContext(ctx, o.binaryPath,
		"inspect", "rollup",
		"--outfile", outputPath,
		fmt.Sprintf("%d", chainID),
	)
	cmd.Dir = o.stateDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("HOME=%s", o.stateDir),
	)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run op-deployer inspect rollup: %w", err)
	}

	return nil
}
