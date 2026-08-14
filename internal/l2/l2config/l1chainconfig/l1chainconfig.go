// Package l1chainconfig writes the L1 chain config file op-node needs to
// start against a Kurtosis-devnet L1: op-node only knows the fork schedule
// for well-known L1 chain IDs (mainnet, Sepolia, ...), so for any other
// chain ID (e.g. Kurtosis's default 3151908) it requires an explicit
// --rollup.l1-chain-config file. Kurtosis's own L1 genesis.json can't be
// used directly because op-node's strict JSON decoder rejects the
// terminalTotalDifficultyPassed field it contains, so this package fetches
// that genesis from the running L1 EL container and strips the field.
package l1chainconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ethera-labs/local-testnet/internal/logger"
)

const (
	fileName = "l1-chainconfig.json"

	// elContainerName is the Kurtosis service name of the L1 execution
	// layer node whose genesis chain config we read. Kurtosis suffixes
	// container names with a random hash, so this is matched as a substring.
	elContainerName = "el-1-geth-lighthouse"
)

// candidateGenesisPaths are the locations ethpandaops/ethereum-package has
// used across versions to store the L1 genesis file inside the EL container.
var candidateGenesisPaths = []string{
	"/network-configs/genesis.json",
	"/el-cl-genesis-data/genesis.json",
}

// fallbackChainConfig is used when the L1 genesis can't be read from the
// running container (e.g. a different Kurtosis package layout). It covers
// the fork schedule ssv-mini's ethereum-package params (internal/l1/params.yaml)
// are known to produce.
const fallbackChainConfig = `{
  "chainId": 3151908,
  "homesteadBlock": 0,
  "eip150Block": 0,
  "eip155Block": 0,
  "eip158Block": 0,
  "byzantiumBlock": 0,
  "constantinopleBlock": 0,
  "petersburgBlock": 0,
  "istanbulBlock": 0,
  "berlinBlock": 0,
  "londonBlock": 0,
  "mergeNetsplitBlock": 0,
  "terminalTotalDifficulty": 0,
  "shanghaiTime": 0,
  "cancunTime": 0,
  "pragueTime": 0,
  "depositContractAddress": "0x00000000219ab540356cBB839Cbe05303d7705Fa"
}`

type dockerClient interface {
	FindContainer(ctx context.Context, nameSubstring string) (string, error)
	ReadFile(ctx context.Context, containerID, path string) ([]byte, error)
}

// Generator writes l1-chainconfig.json into L2 chain config directories.
type Generator struct {
	docker dockerClient
	logger *slog.Logger
}

// NewGenerator creates a new l1-chainconfig generator.
func NewGenerator(dockerClient dockerClient) *Generator {
	return &Generator{
		docker: dockerClient,
		logger: logger.Named("l1_chainconfig_generator"),
	}
}

// Generate writes l1-chainconfig.json to configPath, fetching the filtered
// L1 chain config from the running Kurtosis L1 EL node, falling back to a
// known-good static config if that node can't be reached.
func (g *Generator) Generate(ctx context.Context, configPath string) error {
	chainConfig, err := g.fetchChainConfig(ctx)
	if err != nil {
		g.logger.With("err", err.Error()).Warn("could not read L1 genesis from container, using fallback chain config")
		chainConfig = []byte(fallbackChainConfig)
	}

	outputPath := filepath.Join(configPath, fileName)
	if err := os.WriteFile(outputPath, chainConfig, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}

	return nil
}

// fetchChainConfig reads genesis.json from the L1 EL container and returns
// its "config" property with the op-node-incompatible field removed.
func (g *Generator) fetchChainConfig(ctx context.Context) ([]byte, error) {
	containerID, err := g.docker.FindContainer(ctx, elContainerName)
	if err != nil {
		return nil, fmt.Errorf("failed to find %s container: %w", elContainerName, err)
	}
	if containerID == "" {
		return nil, fmt.Errorf("no running %s container found", elContainerName)
	}

	var lastErr error
	for _, path := range candidateGenesisPaths {
		data, err := g.docker.ReadFile(ctx, containerID, path)
		if err != nil {
			lastErr = err
			continue
		}

		var genesis struct {
			Config map[string]any `json:"config"`
		}
		if err := json.Unmarshal(data, &genesis); err != nil {
			return nil, fmt.Errorf("failed to parse genesis.json at %s: %w", path, err)
		}

		delete(genesis.Config, "terminalTotalDifficultyPassed")

		chainConfig, err := json.MarshalIndent(genesis.Config, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to marshal chain config: %w", err)
		}

		return chainConfig, nil
	}

	return nil, fmt.Errorf("genesis.json not found at any known path: %w", lastErr)
}
