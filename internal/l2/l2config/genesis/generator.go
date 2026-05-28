package genesis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/ethera-labs/local-testnet/internal/l2/infra/filesystem"
	"github.com/ethera-labs/local-testnet/internal/logger"
)

const GenesisFileName = "genesis.json"

var (
	ansiRe        = regexp.MustCompile("\x1b\\[[0-9;]*[mGKHF]")
	genesisHashRe = regexp.MustCompile(`Genesis block written.*?(0x[0-9a-fA-F]{64})`)
)

// Generator generates genesis.json files for L2 chains
type (
	deployer interface {
		InspectGenesis(ctx context.Context, chainID int) (string, error)
	}

	Generator struct {
		deployer        deployer
		writer          filesystem.Writer
		localnetDir     string
		opRethBinaryPath string
		logger          *slog.Logger
	}
)

// NewGenerator creates a new genesis generator.
// opRethBinaryPath is the path to the op-reth binary on the host.
func NewGenerator(deployer deployer, writer filesystem.Writer, localnetDir, opRethBinaryPath string) *Generator {
	return &Generator{
		deployer:        deployer,
		writer:          writer,
		localnetDir:     localnetDir,
		opRethBinaryPath: opRethBinaryPath,
		logger:          logger.Named("genesis_generator"),
	}
}

// Generate generates genesis config for a chain
func (g *Generator) Generate(ctx context.Context, chainID int, path string, walletAddress, sequencerAddress, genesisBalanceWei string) (string, error) {
	logger := g.logger.With("chain_id", chainID)

	logger.Info("inspecting genesis")
	output, err := g.deployer.InspectGenesis(ctx, chainID)
	if err != nil {
		return "", fmt.Errorf("failed to inspect genesis: %w", err)
	}

	logger.With("output_len", len(output)).Info("unmarshalling output")
	var genesis map[string]any
	if err := json.Unmarshal([]byte(output), &genesis); err != nil {
		return "", fmt.Errorf("failed to parse genesis JSON: %w", err)
	}

	alloc, ok := genesis["alloc"].(map[string]any)
	if !ok {
		alloc = make(map[string]any)
		genesis["alloc"] = alloc
	}

	balanceWei, success := new(big.Int).SetString(genesisBalanceWei, 10)
	if !success {
		return "", fmt.Errorf("invalid genesis balance: %s", genesisBalanceWei)
	}

	for _, addr := range []string{walletAddress, sequencerAddress} {
		if addr == "" {
			return "", fmt.Errorf("wallet or sequencer address cannot be empty")
		}

		cleanAddr := addr
		if len(cleanAddr) > 2 && cleanAddr[:2] == "0x" {
			cleanAddr = cleanAddr[2:]
		}
		cleanAddr = "0x" + cleanAddr

		accountData, ok := alloc[cleanAddr].(map[string]any)
		if !ok {
			accountData = make(map[string]any)
			alloc[cleanAddr] = accountData
		}
		accountData["balance"] = fmt.Sprintf("0x%x", balanceWei)
	}

	config, ok := genesis["config"].(map[string]any)
	if !ok {
		config = make(map[string]any)
		genesis["config"] = config
	}
	config["pragueTime"] = 0
	config["isthmusTime"] = 0

	g.logger.Info("computing genesis hash")
	hash, err := g.computeGenesisHash(ctx, genesis)
	if err != nil {
		return "", fmt.Errorf("failed to compute genesis hash: %w", err)
	}

	genesisPath := filepath.Join(path, GenesisFileName)

	g.logger.
		With("hash", hash).
		With("file_path", genesisPath).
		Info("genesis generated successfully. Persisting file")
	if err := g.writer.WriteJSON(genesisPath, genesis); err != nil {
		return "", fmt.Errorf("failed to write genesis.json for chain %d: %w", chainID, err)
	}

	return hash, nil
}

// computeGenesisHash runs op-reth init natively and extracts the genesis hash
// from its "Genesis block written hash=0x..." log line.
// Stock go-ethereum's core.Genesis cannot compute OP-Stack hashes correctly
// past Isthmus, so we defer to the actual EL.
func (g *Generator) computeGenesisHash(ctx context.Context, genesis map[string]any) (string, error) {
	tmpBaseDir := filepath.Join(g.localnetDir, ".tmp")
	if err := os.MkdirAll(tmpBaseDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create temp base dir: %w", err)
	}

	tmpDir, err := os.MkdirTemp(tmpBaseDir, "genesis-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	genesisJSON, err := json.Marshal(genesis)
	if err != nil {
		return "", fmt.Errorf("failed to marshal genesis: %w", err)
	}
	genesisFile := filepath.Join(tmpDir, GenesisFileName)
	if err := os.WriteFile(genesisFile, genesisJSON, 0o644); err != nil {
		return "", fmt.Errorf("failed to write genesis file: %w", err)
	}

	dataDir := filepath.Join(tmpDir, "data")

	g.logger.With("binary", g.opRethBinaryPath).Info("running op-reth init")

	cmd := exec.CommandContext(ctx, g.opRethBinaryPath,
		"init",
		"--datadir", dataDir,
		"--chain", genesisFile,
	)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to run op-reth init: %w\nstdout: %s\nstderr: %s", err, outBuf.String(), errBuf.String())
	}

	// op-reth writes the genesis hash to stderr
	combined := outBuf.String() + errBuf.String()
	clean := ansiRe.ReplaceAllString(combined, "")
	match := genesisHashRe.FindStringSubmatch(clean)
	if len(match) < 2 {
		return "", fmt.Errorf("genesis hash not found in op-reth init output:\n%s", clean)
	}
	return match[1], nil
}
