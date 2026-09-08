package l2

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/spf13/cobra"
)

var frontendEnvCmd = &cobra.Command{
	Use:   "frontend-env",
	Short: "Regenerate frontend/.env from the current config and deployed L2 contract addresses",
	Long: "Reads RPC/API ports from configs.yaml and contract addresses from " +
		".localnet/networks/<chain>/contracts.json (written by `l2` on deploy) and writes frontend/.env. " +
		"Run this after `localnet l2` whenever the console's addresses or ports go stale.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		rootDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}

		cfg := configs.Values.L2
		networksDir := filepath.Join(rootDir, localnetDirName, networksDirName)

		addressesA, err := readContractAddresses(filepath.Join(networksDir, string(configs.L2ChainNameRollupA), "contracts.json"))
		if err != nil {
			return fmt.Errorf("failed to read rollup-a contract addresses: %w", err)
		}
		addressesB, err := readContractAddresses(filepath.Join(networksDir, string(configs.L2ChainNameRollupB), "contracts.json"))
		if err != nil {
			return fmt.Errorf("failed to read rollup-b contract addresses: %w", err)
		}

		rollupA := cfg.ChainConfigs[configs.L2ChainNameRollupA]
		rollupB := cfg.ChainConfigs[configs.L2ChainNameRollupB]

		env := buildFrontendEnv(cfg, rollupA, rollupB, addressesA, addressesB)

		envPath := filepath.Join(rootDir, "frontend", ".env")
		if err := os.WriteFile(envPath, []byte(env), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", envPath, err)
		}

		slog.With("path", envPath).Info("wrote frontend/.env")
		return nil
	},
}

// contractAddresses is the subset of a deployed .localnet/networks/<chain>/contracts.json
// file this command needs.
type contractAddresses struct {
	UniversalBridgeMailbox string
	ComposeL2ToL2Bridge    string
	MockL2ERC20            string
	CetFactory             string
	ComposeETHLiquidity    string
	EntryPoint             string
	SimpleAccountFactory   string
}

func readContractAddresses(path string) (contractAddresses, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return contractAddresses{}, fmt.Errorf("failed to read %s (has `localnet l2` deployed contracts yet?): %w", path, err)
	}

	var doc struct {
		Addresses map[string]string `json:"addresses"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return contractAddresses{}, fmt.Errorf("failed to parse %s: %w", path, err)
	}

	return contractAddresses{
		UniversalBridgeMailbox: doc.Addresses["UniversalBridgeMailbox"],
		ComposeL2ToL2Bridge:    doc.Addresses["ComposeL2ToL2Bridge"],
		MockL2ERC20:            doc.Addresses["MockL2ERC20"],
		CetFactory:             doc.Addresses["CetFactory"],
		ComposeETHLiquidity:    doc.Addresses["ComposeETHLiquidity"],
		EntryPoint:             doc.Addresses["EntryPoint"],
		SimpleAccountFactory:   doc.Addresses["SimpleAccountFactory"],
	}, nil
}

// frontendHealthAPIPort mirrors HEALTH_API_PORT's default in
// internal/l2/infra/docker/docker-compose.yml (localnet-health service);
// there's no l2.* config field for it since it's always on and unconfigurable
// today.
const frontendHealthAPIPort = 8090

func buildFrontendEnv(cfg configs.L2, rollupA, rollupB configs.Chain, addressesA, addressesB contractAddresses) string {
	return fmt.Sprintf(`# Chain IDs
VITE_CHAIN_A_ID=%d
VITE_CHAIN_B_ID=%d

# Builder mode (flashblocks)
# When true, transactions are sent to op-rbuilder (builder RPC)
# When false, transactions are sent directly to op-reth (EL RPC)
VITE_FLASHBLOCKS_ENABLED=%t

# RPC endpoints — direct host URLs to the docker-compose port mappings
# (see internal/l2/infra/docker/docker-compose*.yml). The relative /rpc/...
# form only works behind a reverse proxy (e.g. a Serveo tunnel) that maps
# those exact paths; plain `+"`vite dev`"+` has no such proxy configured, so it
# 404s every request. op-reth/op-rbuilder allow CORS from any origin.
VITE_CHAIN_A_BUILDER_RPC=http://localhost:%d
VITE_CHAIN_A_OP_RETH_RPC=http://localhost:%d
VITE_CHAIN_B_BUILDER_RPC=http://localhost:%d
VITE_CHAIN_B_OP_RETH_RPC=http://localhost:%d

# Sidecar endpoints
VITE_SIDECAR_A_URL=http://localhost:%d
VITE_SIDECAR_B_URL=http://localhost:%d

# Health API
VITE_HEALTH_API_URL=http://localhost:%d

# Bundler endpoints
VITE_BUNDLER_A_URL=http://localhost:%d
VITE_BUNDLER_B_URL=http://localhost:%d

# Contract addresses (deployed by local-testnet)
VITE_CHAIN_A_BRIDGE_ADDRESS=%s
VITE_CHAIN_B_BRIDGE_ADDRESS=%s
VITE_CHAIN_A_TOKEN_ADDRESS=%s
VITE_CHAIN_B_TOKEN_ADDRESS=%s
VITE_CET_FACTORY_ADDRESS=%s
VITE_CHAIN_A_ETH_LIQUIDITY_ADDRESS=%s
VITE_CHAIN_B_ETH_LIQUIDITY_ADDRESS=%s

# Wallet private key ⚠ VITE_ vars are exposed in the browser bundle.
# This is a test key for local dev only — NEVER use a real key here.
VITE_WALLET_PRIVATE_KEY=%s

# ERC-4337 bundler (optional). When all three are set, the console shows a
# "Bundler" tab that drives an end-to-end ethera_buildSignedUserOpsTx flow.
VITE_ENTRYPOINT_A=%s
VITE_ENTRYPOINT_B=%s
VITE_SIMPLE_ACCOUNT_FACTORY_A=%s
VITE_SIMPLE_ACCOUNT_FACTORY_B=%s
`,
		rollupA.ID, rollupB.ID,
		cfg.Flashblocks.Enabled,
		cfg.Flashblocks.RollupARPCPort, rollupA.RPCPort,
		cfg.Flashblocks.RollupBRPCPort, rollupB.RPCPort,
		cfg.Sidecar.RollupAAPIPort, cfg.Sidecar.RollupBAPIPort,
		frontendHealthAPIPort,
		cfg.Bundler.RollupAAPIPort, cfg.Bundler.RollupBAPIPort,
		addressesA.ComposeL2ToL2Bridge, addressesB.ComposeL2ToL2Bridge,
		addressesA.MockL2ERC20, addressesB.MockL2ERC20,
		addressesA.CetFactory,
		addressesA.ComposeETHLiquidity, addressesB.ComposeETHLiquidity,
		cfg.Wallet.PrivateKey,
		addressesA.EntryPoint, addressesB.EntryPoint,
		addressesA.SimpleAccountFactory, addressesB.SimpleAccountFactory,
	)
}
