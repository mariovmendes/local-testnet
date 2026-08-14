package docker

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethera-labs/local-testnet/internal/l2/path"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// EnvBuilder constructs environment variables for docker-compose operations.
// It handles path resolution for both operator-supplied local checkouts
// (`local-path`) and workspace-managed clones under `.localnet/services`.
type EnvBuilder struct {
	rootDir     string
	networksDir string
	servicesDir string
}

func NewEnvBuilder(rootDir, networksDir, servicesDir string) *EnvBuilder {
	return &EnvBuilder{
		rootDir:     rootDir,
		networksDir: networksDir,
		servicesDir: servicesDir,
	}
}

// BuildComposeEnv builds environment variables for docker-compose.
// gameFactoryAddr and composeL2OOAddr may be the zero address during bootstrap
// before dispute-game contract addresses are known.
func (b *EnvBuilder) BuildComposeEnv(cfg configs.L2, gameFactoryAddr common.Address, composeL2OOAddr common.Address) (map[string]string, error) {
	env := make(map[string]string)

	publisherPath, err := b.ResolveRepoPath(cfg.Repositories[configs.RepositoryNamePublisher], configs.RepositoryNamePublisher)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve publisher path: %w", err)
	}

	rollupAConfigPath := filepath.Join(b.networksDir, string(configs.L2ChainNameRollupA))
	rollupBConfigPath := filepath.Join(b.networksDir, string(configs.L2ChainNameRollupB))

	rollupAHost, err := path.GetHostPath(rollupAConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve host path for rollup-a config: %w", err)
	}
	rollupBHost, err := path.GetHostPath(rollupBConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve host path for rollup-b config: %w", err)
	}
	rootHost, err := path.GetHostPath(b.rootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve host path for rootDir: %w", err)
	}

	env["ROOT_DIR"] = rootHost
	env["WALLET_PRIVATE_KEY"] = cfg.Wallet.PrivateKey
	env["WALLET_ADDRESS"] = cfg.Wallet.Address
	env["L1_EL_URL"] = cfg.L1ElURL
	env["L1_CL_URL"] = cfg.L1ClURL
	// Containers on the localnet-l2 network can't reach the host loopback
	// address Kurtosis binds L1 ports to. Services that join kt-localnet
	// (op-node, op-batcher, op-proposer, publisher) use these container-name
	// addresses instead; see deployer.Apply for the equivalent op-deployer fix.
	env["L1_EL_URL_INTERNAL"] = toKurtosisContainerURL(cfg.L1ElURL, kurtosisL1ELContainerName, kurtosisL1ELPort)
	env["L1_CL_URL_INTERNAL"] = toKurtosisContainerURL(cfg.L1ClURL, kurtosisL1CLContainerName, kurtosisL1CLPort)
	env["L1_CHAIN_ID"] = fmt.Sprintf("%d", cfg.L1ChainID)
	env["ETHERA_NETWORK_NAME"] = cfg.EtheraNetworkName
	env["COORDINATOR_PRIVATE_KEY"] = cfg.CoordinatorPrivateKey
	env["SEQUENCER_PRIVATE_KEY"] = cfg.CoordinatorPrivateKey
	env["SP_L1_SUPERBLOCK_CONTRACT"] = composeL2OOAddr.Hex()

	env["PUBLISHER_PATH"] = publisherPath

	if cfg.OPSuccinct.Enabled {
		opSuccinctPath, err := b.ResolveRepoPath(cfg.Repositories[configs.RepositoryNameOPSuccinct], configs.RepositoryNameOPSuccinct)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve op-succinct path: %w", err)
		}
		env["OP_SUCCINCT_PATH"] = opSuccinctPath

		env["OP_SUCCINCT_DOCKERFILE"] = filepath.Join(opSuccinctPath, "validity", "Dockerfile.ethera")
		if cfg.AltDA.Enabled {
			env["OP_SUCCINCT_BUILD_FEATURES"] = "altda"
			env["OP_SUCCINCT_ALTDA_SERVER_A"] = "http://op-alt-da-a:3100"
			env["OP_SUCCINCT_ALTDA_SERVER_B"] = "http://op-alt-da-b:3100"
		} else {
			env["OP_SUCCINCT_BUILD_FEATURES"] = ""
		}
	}

	if cfg.Flashblocks.Enabled {
		opRbuilderPath, err := b.ResolveRepoPath(cfg.Repositories[configs.RepositoryNameOpRbuilder], configs.RepositoryNameOpRbuilder)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve op-rbuilder path: %w", err)
		}
		env["OP_RBUILDER_PATH"] = opRbuilderPath
	}

	// The validator EL's P2P secret + matching enode pubkey are always populated so the
	// flashblocks trusted-peer wiring has a value regardless of feature flags. Both op-reth
	// (--p2p-secret-key-hex) and op-besu (--node-private-key-file) boot with this same key, so
	// the enode op-rbuilder dials is stable across either client. Derived from the secret; one
	// source of truth.
	rethASK, rethAEnode, err := derivePeerKeys(cfg.Flashblocks.RollupAP2PSecretKeyHex)
	if err != nil {
		return nil, fmt.Errorf("rollup-a flashblocks p2p key: %w", err)
	}
	env["VALIDATOR_EL_A_P2P_SECRET_KEY_HEX"] = rethASK
	env["VALIDATOR_EL_A_ENODE_PUBKEY"] = rethAEnode

	rethBSK, rethBEnode, err := derivePeerKeys(cfg.Flashblocks.RollupBP2PSecretKeyHex)
	if err != nil {
		return nil, fmt.Errorf("rollup-b flashblocks p2p key: %w", err)
	}
	env["VALIDATOR_EL_B_P2P_SECRET_KEY_HEX"] = rethBSK
	env["VALIDATOR_EL_B_ENODE_PUBKEY"] = rethBEnode

	if cfg.Sidecar.Enabled {
		sidecarPath, err := b.ResolveRepoPath(cfg.Repositories[configs.RepositoryNameSidecar], configs.RepositoryNameSidecar)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve sidecar path: %w", err)
		}
		env["SIDECAR_PATH"] = sidecarPath
	}
	if cfg.Bundler.Enabled {
		bundlerPath, err := b.ResolveRepoPath(cfg.Repositories[configs.RepositoryNameBundler], configs.RepositoryNameBundler)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve bundler path: %w", err)
		}
		env["BUNDLER_PATH"] = bundlerPath
	}
	if cfg.AltDA.Enabled {
		composeContractsPath, err := b.ResolveRepoPath(cfg.Repositories[configs.RepositoryNameEtheraContracts], configs.RepositoryNameEtheraContracts)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve ethera-contracts path for AltDA DA server build: %w", err)
		}
		env["COMPOSE_CONTRACTS_PATH"] = composeContractsPath
	}
	env["ALTDA_USE_GENERIC_COMMITMENT"] = fmt.Sprintf("%t", cfg.AltDA.CommitmentType() == configs.AltDACommitmentTypeGeneric)

	env["ROLLUP_A_CHAIN_ID"] = fmt.Sprintf("%d", cfg.ChainConfigs[configs.L2ChainNameRollupA].ID)
	env["ROLLUP_A_RPC_PORT"] = fmt.Sprintf("%d", cfg.ChainConfigs[configs.L2ChainNameRollupA].RPCPort)
	env["ROLLUP_A_CONFIG_PATH"] = rollupAHost
	env["ROLLUP_A_CONFIG_PATH_CONTAINER"] = rollupAConfigPath

	env["ROLLUP_B_CHAIN_ID"] = fmt.Sprintf("%d", cfg.ChainConfigs[configs.L2ChainNameRollupB].ID)
	env["ROLLUP_B_RPC_PORT"] = fmt.Sprintf("%d", cfg.ChainConfigs[configs.L2ChainNameRollupB].RPCPort)
	env["ROLLUP_B_CONFIG_PATH"] = rollupBHost
	env["ROLLUP_B_CONFIG_PATH_CONTAINER"] = rollupBConfigPath

	env["FLASHBLOCKS_ROLLUP_A_RPC_PORT"] = fmt.Sprintf("%d", cfg.Flashblocks.RollupARPCPort)
	env["FLASHBLOCKS_ROLLUP_B_RPC_PORT"] = fmt.Sprintf("%d", cfg.Flashblocks.RollupBRPCPort)

	env["SIDECAR_ROLLUP_A_API_PORT"] = fmt.Sprintf("%d", cfg.Sidecar.RollupAAPIPort)
	env["SIDECAR_ROLLUP_B_API_PORT"] = fmt.Sprintf("%d", cfg.Sidecar.RollupBAPIPort)

	env["BUNDLER_ROLLUP_A_API_PORT"] = fmt.Sprintf("%d", cfg.Bundler.RollupAAPIPort)
	env["BUNDLER_ROLLUP_B_API_PORT"] = fmt.Sprintf("%d", cfg.Bundler.RollupBAPIPort)

	frontendPath, err := path.GetHostPath(filepath.Join(b.rootDir, "frontend"))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve frontend path: %w", err)
	}
	env["FRONTEND_PATH"] = frontendPath

	if cfg.Frontend.Active() && cfg.Frontend.Port > 0 {
		env["CONSOLE_PORT"] = fmt.Sprintf("%d", cfg.Frontend.Port)
	}

	env["SP_L1_DISPUTE_GAME_FACTORY"] = gameFactoryAddr.Hex()

	env["OP_BATCHER_IMAGE_TAG"] = cfg.Images[configs.ImageNameOpBatcher].Tag
	env["OP_NODE_IMAGE_TAG"] = cfg.Images[configs.ImageNameOpNode].Tag
	env["OP_PROPOSER_IMAGE_TAG"] = cfg.Images[configs.ImageNameOpProposer].Tag
	env["OP_RETH_IMAGE_TAG"] = cfg.Images[configs.ImageNameOpReth].Tag

	// Contract-derived values (MAILBOX, ENTRYPOINT, SIMPLE_ACCOUNT_FACTORY) are
	// populated via MergePostDeployEnv. Called here so the first env snapshot
	// reflects any contracts.json that already exists on disk from a previous
	// run; the orchestrator calls MergePostDeployEnv again after Phase 3.
	b.MergePostDeployEnv(env)

	return env, nil
}

// ResolveRepoPath resolves the repository path for a given repository configuration.
// This is exported so other packages can resolve paths consistently.
// Config validation ensures URL and local-path are mutually exclusive.
// When URL is set, uses cloned repository path (.localnet/services/<name>).
// When local-path is set, uses the specified local path (for development).
// When running in Docker:
//   - Cloned paths stay as container paths (accessible via workspace mount)
//   - Local paths get translated to host paths (outside workspace mount)
func (b *EnvBuilder) ResolveRepoPath(repo configs.Repository, name configs.RepositoryName) (string, error) {
	// If URL is provided (via CLI or config), use cloned path
	// This ensures CLI flags like --op-reth-url override local-path from config
	// Cloned paths are inside the workspace mount, so they stay as container paths
	if repo.URL != "" {
		return filepath.Join(b.servicesDir, string(name)), nil
	}

	if repo.LocalPath != "" {
		expanded := expandUserHome(repo.LocalPath)

		var resolvedPath string
		if filepath.IsAbs(expanded) {
			resolvedPath = expanded
		} else {
			resolvedPath = filepath.Clean(filepath.Join(b.rootDir, expanded))
		}

		hostPath, err := path.GetHostPath(resolvedPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve host path for %s: %w", resolvedPath, err)
		}
		return hostPath, nil
	}

	return "", fmt.Errorf("repository %s has neither URL nor local-path set", name)
}

// MergePostDeployEnv re-reads the per-chain contracts.json files and injects
// the resulting addresses into the env map. Idempotent: safe to call before
// deployment (no-op when contracts.json is missing) and again after
// deployment to pick up the freshly-written values.
func (b *EnvBuilder) MergePostDeployEnv(env map[string]string) {
	if ma := b.readUniversalBridgeMailboxAddress(configs.L2ChainNameRollupA); ma != "" {
		env["MAILBOX_A"] = ma
	}
	if mb := b.readUniversalBridgeMailboxAddress(configs.L2ChainNameRollupB); mb != "" {
		env["MAILBOX_B"] = mb
	}
	if ep := b.readContractAddress(configs.L2ChainNameRollupA, "EntryPoint"); ep != "" {
		env["ENTRYPOINT_A"] = ep
	}
	if ep := b.readContractAddress(configs.L2ChainNameRollupB, "EntryPoint"); ep != "" {
		env["ENTRYPOINT_B"] = ep
	}
	if f := b.readContractAddress(configs.L2ChainNameRollupA, "SimpleAccountFactory"); f != "" {
		env["SIMPLE_ACCOUNT_FACTORY_A"] = f
	}
	if f := b.readContractAddress(configs.L2ChainNameRollupB, "SimpleAccountFactory"); f != "" {
		env["SIMPLE_ACCOUNT_FACTORY_B"] = f
	}
}

// readUniversalBridgeMailboxAddress reads the deployed UniversalBridgeMailbox
// address from the chain's contracts.json. Returns an empty string before the
// file is written or if the address is missing.
func (b *EnvBuilder) readUniversalBridgeMailboxAddress(chainName configs.L2ChainName) string {
	path := filepath.Join(b.networksDir, string(chainName), "contracts.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	var cf struct {
		Addresses map[string]string `json:"addresses"`
	}
	if err := json.Unmarshal(data, &cf); err != nil {
		return ""
	}

	return strings.TrimSpace(cf.Addresses["UniversalBridgeMailbox"])
}

// readContractAddress reads a named contract address from the chain's
// contracts.json. Returns "" when the file or entry is missing so callers can
// treat the address as optional.
func (b *EnvBuilder) readContractAddress(chainName configs.L2ChainName, contractName string) string {
	data, err := os.ReadFile(filepath.Join(b.networksDir, string(chainName), "contracts.json"))
	if err != nil {
		return ""
	}
	var cf struct {
		Addresses map[string]string `json:"addresses"`
	}
	if err := json.Unmarshal(data, &cf); err != nil {
		return ""
	}
	return strings.TrimSpace(cf.Addresses[contractName])
}

// derivePeerKeys takes a 32-byte hex-encoded secp256k1 secret and returns the
// normalized hex secret (no 0x prefix) plus the 64-byte uncompressed enode
// pubkey (no 0x04 prefix), as expected by `--p2p-secret-key-hex` and the enode
// URL format respectively.
func derivePeerKeys(secretHex string) (string, string, error) {
	sk := strings.TrimPrefix(secretHex, "0x")
	priv, err := crypto.HexToECDSA(sk)
	if err != nil {
		return "", "", fmt.Errorf("invalid secret: %w", err)
	}
	pub := crypto.FromECDSAPub(&priv.PublicKey) // 65 bytes: 0x04 || X || Y
	return sk, hex.EncodeToString(pub[1:]), nil
}

const (
	kurtosisL1ELContainerName = "el-1-geth-lighthouse"
	kurtosisL1ELPort          = 8545
	kurtosisL1CLContainerName = "cl-1-lighthouse-geth"
	kurtosisL1CLPort          = 4000
)

// toKurtosisContainerURL rewrites a host-facing L1 URL (127.0.0.1, localhost,
// or host.docker.internal) to the Kurtosis container's name and in-network
// port, so containers joined to the kt-localnet network can reach it.
func toKurtosisContainerURL(hostURL, containerName string, containerPort int) string {
	rewritten := strings.NewReplacer(
		"host.docker.internal", containerName,
		"127.0.0.1", containerName,
		"localhost", containerName,
	).Replace(hostURL)

	if strings.Contains(rewritten, containerName+":") {
		if colonIdx := strings.LastIndex(rewritten, ":"); colonIdx != -1 {
			rewritten = fmt.Sprintf("%s:%d", rewritten[:colonIdx], containerPort)
		}
	}

	return rewritten
}

// expandUserHome expands a leading ~ to the current user's home directory.
// Returns the original path if expansion fails or is not needed.
func expandUserHome(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}
