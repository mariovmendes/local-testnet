package deployer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethera-labs/local-testnet/internal/l2/infra/docker"
	"github.com/ethera-labs/local-testnet/internal/l2/path"
	"github.com/ethera-labs/local-testnet/internal/logger"
)

const (
	publicImageName = "us-docker.pkg.dev/oplabs-tools-artifacts/images/op-deployer"

	// KurtosisNetworkMode is the Docker network Kurtosis creates for the L1
	// enclave ("kt-<enclave name>"). op-deployer must join it to reach L1
	// services by container name, since containers can't reach the host's
	// loopback address that Kurtosis binds its ports to.
	KurtosisNetworkMode = "kt-localnet"
)

// Deployer wraps the op-deployer tool
type Deployer struct {
	rootDir         string
	stateDir        string
	imageWithTag    string
	imageEntrypoint string
	networkMode     string
	docker          *docker.Client
	logger          *slog.Logger
}

// NewDeployer creates a new op-deployer wrapper
// imageTag should be the version tag (e.g., "v0.3.3")
// networkMode is the Docker network to join so op-deployer can reach Kurtosis L1 services (see KurtosisNetworkMode).
func NewDeployer(rootDir, stateDir, imageTag, networkMode string, dockerClient *docker.Client) *Deployer {
	return &Deployer{
		rootDir:         rootDir,
		stateDir:        stateDir,
		imageWithTag:    fmt.Sprintf("%s:%s", publicImageName, imageTag),
		imageEntrypoint: "/usr/local/bin/op-deployer",
		networkMode:     networkMode,
		docker:          dockerClient,
		logger:          logger.Named("deployer"),
	}
}

// Init initializes the op-deployer state
func (o *Deployer) Init(ctx context.Context, l1ChainID int, l2Chains map[configs.L2ChainName]configs.Chain) error {
	o.logger.
		With("state_dir", o.stateDir).
		Info("initializing deployer state. Ensuring image exists")

	if err := o.ensureImage(ctx); err != nil {
		return fmt.Errorf("failed to ensure op-deployer image: %w", err)
	}

	stateFile := filepath.Join(o.stateDir, stateFile)
	if _, err := os.Stat(stateFile); err == nil {
		o.logger.
			With("file_name", stateFile).
			Info("state already exists, skipping init")

		return nil
	}

	// When running in Docker, we need to use the host's path for volume mounts
	// Otherwise Docker daemon won't recognize the path
	absStateDir, err := path.GetHostPath(o.stateDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	var chainIDsStr []string
	for _, chainConfig := range l2Chains {
		chainIDsStr = append(chainIDsStr, fmt.Sprintf("%d", chainConfig.ID))
	}

	o.logger.Info("running docker image")
	_, err = o.docker.Run(ctx, docker.RunOptions{
		Image:      o.imageWithTag,
		Entrypoint: []string{o.imageEntrypoint},
		Cmd: []string{
			"init",
			"--intent-type", "custom",
			"--l1-chain-id", strconv.Itoa(l1ChainID),
			"--l2-chain-ids", strings.Join(chainIDsStr, ","),
		},
		Env: []string{
			"HOME=/work",
			"DEPLOYER_CACHE_DIR=/work/.cache",
		},
		Volumes: map[string]string{
			absStateDir: "/work",
		},
		WorkDir:     "/work",
		User:        fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		NetworkMode: o.networkMode,
		AutoRemove:  true,
		StreamLogs:  true,
	})

	if err != nil {
		return fmt.Errorf("failed to run op-deployer init: %w", err)
	}

	o.logger.Info("deployer state initialized successfully")

	return nil
}

// EnsureImage ensures the op-deployer public image exists
func (o *Deployer) ensureImage(ctx context.Context) error {
	logger := o.logger.With("image", o.imageWithTag)

	exists, err := o.docker.ImageExists(ctx, o.imageWithTag)
	if err != nil {
		return fmt.Errorf("failed to check image: '%s' existence: %w", o.imageWithTag, err)
	}

	if exists {
		logger.Info("image already exists")
		return nil
	}

	logger.Info("pulling public image")
	if err := o.docker.PullImage(ctx, o.imageWithTag); err != nil {
		return fmt.Errorf("failed to pull image: '%s', %w", o.imageWithTag, err)
	}

	logger.Info("image pulled successfully")
	return nil
}

// Apply runs op-deployer apply to deploy L1 contracts
func (o *Deployer) Apply(ctx context.Context, l1RpcURL, deployerPrivateKey, deploymentTarget string) error {
	o.logger.
		With("deployment_target", deploymentTarget).
		Info("running deployer apply")

	absStateDir, err := path.GetHostPath(o.stateDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// op-deployer runs inside the kt-localnet Docker network. Kurtosis binds its
	// EL port to 127.0.0.1 on the host, which is unreachable from inside Docker
	// (127.0.0.1 resolves to the container itself; host.docker.internal resolves
	// to the Docker bridge gateway, also not Kurtosis's loopback port).
	// Replace all host-side addresses with the Kurtosis container name so the
	// request stays on the Docker network.
	dockerL1URL := strings.NewReplacer(
		"host.docker.internal", "el-1-geth-lighthouse",
		"127.0.0.1", "el-1-geth-lighthouse",
		"localhost", "el-1-geth-lighthouse",
	).Replace(l1RpcURL)
	// Replace whatever host port was configured with the container-internal port 8545.
	if strings.Contains(dockerL1URL, "el-1-geth-lighthouse:") {
		if colonIdx := strings.LastIndex(dockerL1URL, ":"); colonIdx != -1 {
			dockerL1URL = dockerL1URL[:colonIdx] + ":8545"
		}
	}

	_, err = o.docker.Run(ctx, docker.RunOptions{
		Image:      o.imageWithTag,
		Entrypoint: []string{o.imageEntrypoint},
		Cmd: []string{
			"apply",
			"--deployment-target", deploymentTarget,
		},
		Env: []string{
			"HOME=/work",
			"DEPLOYER_CACHE_DIR=/work/.cache",
			fmt.Sprintf("L1_RPC_URL=%s", dockerL1URL),
			fmt.Sprintf("DEPLOYER_PRIVATE_KEY=%s", deployerPrivateKey),
		},
		Volumes: map[string]string{
			absStateDir: "/work",
		},
		WorkDir:     "/work",
		User:        fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		NetworkMode: o.networkMode,
		AutoRemove:  true,
		StreamLogs:  true,
	})

	if err != nil {
		return fmt.Errorf("failed to run op-deployer apply: %w", err)
	}

	o.logger.Info("deployer apply completed successfully")

	return nil
}

// InspectGenesis exports genesis JSON for a chain
func (o *Deployer) InspectGenesis(ctx context.Context, chainID int) (string, error) {
	absStateDir, err := path.GetHostPath(o.stateDir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	output, err := o.docker.Run(ctx, docker.RunOptions{
		Image:      o.imageWithTag,
		Entrypoint: []string{o.imageEntrypoint},
		Cmd: []string{
			"inspect",
			"genesis",
			fmt.Sprintf("%d", chainID),
		},
		Env: []string{
			"HOME=/work",
		},
		Volumes: map[string]string{
			absStateDir: "/work",
		},
		WorkDir:    "/work",
		User:       fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		AutoRemove: true,
		CaptureOut: true,
	})

	if err != nil {
		return "", fmt.Errorf("failed to run op-deployer inspect genesis: %w", err)
	}

	return output, nil
}

// InspectRollup exports rollup config for a chain
func (o *Deployer) InspectRollup(ctx context.Context, chainID int, outputPath string) error {
	absStateDir, err := path.GetHostPath(o.stateDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	outputDir := filepath.Dir(outputPath)
	absOutputDir, err := path.GetHostPath(outputDir)
	if err != nil {
		return fmt.Errorf("failed to get host path for output dir: %w", err)
	}
	outputFile := filepath.Base(outputPath)

	_, err = o.docker.Run(ctx, docker.RunOptions{
		Image:      o.imageWithTag,
		Entrypoint: []string{o.imageEntrypoint},
		Cmd: []string{
			"inspect",
			"rollup",
			"--outfile", fmt.Sprintf("/output/%s", outputFile),
			fmt.Sprintf("%d", chainID),
		},
		Env: []string{
			"HOME=/work",
		},
		Volumes: map[string]string{
			absStateDir:  "/work",
			absOutputDir: "/output",
		},
		WorkDir:    "/work",
		User:       fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		AutoRemove: true,
	})

	if err != nil {
		return fmt.Errorf("failed to run op-deployer inspect rollup: %w", err)
	}

	return nil
}
