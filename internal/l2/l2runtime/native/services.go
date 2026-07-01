package native

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethera-labs/local-testnet/internal/l2/infra/supervisor"
	"github.com/ethereum/go-ethereum/crypto"
)

// Port layout per chain — native host ports, no Docker port mapping.
const (
	// Chain A — op-reth
	RethAHTTPPort    = 18545
	RethAWSPort      = 18546
	RethAAuthPort    = 18551
	RethAP2PPort     = 30303
	RethAMetrics     = 19898
	NodeARPCPort     = 19545
	BatcherARPCPort  = 18548
	ProposerARPCPort = 18560

	// Chain B — op-reth
	RethBHTTPPort    = 28545
	RethBWSPort      = 28546
	RethBAuthPort    = 28551
	RethBP2PPort     = 30304
	RethBMetrics     = 29898
	NodeBRPCPort     = 29545
	BatcherBRPCPort  = 28548
	ProposerBRPCPort = 28560

	// Chain A — op-rbuilder (Flashblocks block builder, fork of op-reth)
	RbuilderAHTTPPort   = 17545 // HTTP RPC — op-batcher connects here
	RbuilderAEnginePort = 17552 // Engine API — rollup-boost connects here
	RbuilderAFlashPort  = 17111 // Flashblocks WebSocket
	RbuilderAP2PPort    = 30305 // P2P — dials op-reth-a at 30303
	RbuilderAMetrics    = 19001

	// Chain B — op-rbuilder
	RbuilderBHTTPPort   = 27545
	RbuilderBEnginePort = 27552
	RbuilderBFlashPort  = 27111
	RbuilderBP2PPort    = 30306 // P2P — dials op-reth-b at 30304
	RbuilderBMetrics    = 29001

	// Chain A — rollup-boost (Engine API multiplexer)
	RollupBoostAEnginePort = 17551 // Engine API — op-node connects here
	RollupBoostADebugPort  = 17555
	RollupBoostAFlashPort  = 17999 // Flashblocks SSE stream

	// Chain B — rollup-boost
	RollupBoostBEnginePort = 27551
	RollupBoostBDebugPort  = 27555
	RollupBoostBFlashPort  = 27999

	// Publisher (shared 2PC coordinator)
	PublisherAPIPort     = 18080 // QUIC — sidecars connect here
	PublisherMetricsPort = 18081 // HTTP metrics/health

	// Sidecar (cross-chain coordination layer, one per chain)
	SidecarAAPIPort = 17090
	SidecarBAPIPort = 27090

	// Otterscan (native EVM block explorer, one instance per chain)
	OtterscanAPort = 5100
	OtterscanBPort = 5200
)

// Binaries holds resolved filesystem paths to all required native binaries.
type Binaries struct {
	OpReth      string
	OpNode      string
	OpBatcher   string
	OpProposer  string
	OpRbuilder  string // Flashblocks block builder (op-reth fork)
	RollupBoost string // Engine API multiplexer
	Publisher   string // Shared 2PC coordinator (Rust)
	Sidecar     string // Cross-chain coordination sidecar (Rust)
	NPM         string // Node package manager — used to run the frontend dev server
}

// Builder constructs supervisor.ProcessSpec values for the core OP stack
// services for both chains.
type Builder struct {
	cfg         configs.L2
	bins        Binaries
	networksDir string // .localnet/networks/
	dataDir     string // .localnet/data/
}

// NewBuilder creates a Builder.
func NewBuilder(cfg configs.L2, bins Binaries, networksDir, dataDir string) *Builder {
	return &Builder{cfg: cfg, bins: bins, networksDir: networksDir, dataDir: dataDir}
}

// InitReth runs `op-reth init` for chain if its datadir is not yet
// initialized (checked by the presence of db/database.version). Must be
// called before starting the corresponding op-reth process.
func (b *Builder) InitReth(ctx context.Context, chain configs.L2ChainName) error {
	rethData := filepath.Join(b.dataDir, string(chain), "reth")
	dbVersion := filepath.Join(rethData, "db", "database.version")
	if _, err := os.Stat(dbVersion); err == nil {
		return nil // already initialized
	}
	if err := os.MkdirAll(rethData, 0o755); err != nil {
		return fmt.Errorf("mkdir reth datadir: %w", err)
	}
	genesisPath := filepath.Join(b.networksDir, string(chain), "genesis.json")
	cmd := exec.CommandContext(ctx, b.bins.OpReth,
		"init",
		"--datadir", rethData,
		"--chain", genesisPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("op-reth init for %s: %w", chain, err)
	}
	return nil
}

// InitRbuilder runs `op-rbuilder init` for chain if its datadir is not yet
// initialized. Must be called before starting op-rbuilder (when Flashblocks
// is enabled).
func (b *Builder) InitRbuilder(ctx context.Context, chain configs.L2ChainName) error {
	rbuilderData := filepath.Join(b.dataDir, string(chain), "rbuilder")
	dbVersion := filepath.Join(rbuilderData, "db", "database.version")
	if _, err := os.Stat(dbVersion); err == nil {
		return nil // already initialized
	}
	if err := os.MkdirAll(rbuilderData, 0o755); err != nil {
		return fmt.Errorf("mkdir rbuilder datadir: %w", err)
	}
	genesisPath := filepath.Join(b.networksDir, string(chain), "genesis.json")
	cmd := exec.CommandContext(ctx, b.bins.OpRbuilder,
		"init",
		"--datadir", rbuilderData,
		"--chain", genesisPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("op-rbuilder init for %s: %w", chain, err)
	}
	return nil
}

// RethSpecs returns ProcessSpecs for op-reth-a and op-reth-b.
func (b *Builder) RethSpecs() ([]supervisor.ProcessSpec, error) {
	chainAConfig := b.cfg.ChainConfigs[configs.L2ChainNameRollupA]
	specA, err := b.rethSpec(
		configs.L2ChainNameRollupA,
		RethAHTTPPort, RethAWSPort, RethAAuthPort, RethAP2PPort, RethAMetrics,
		b.cfg.Flashblocks.RollupAP2PSecretKeyHex,
		chainAConfig.ID,
	)
	if err != nil {
		return nil, err
	}

	chainBConfig := b.cfg.ChainConfigs[configs.L2ChainNameRollupB]
	specB, err := b.rethSpec(
		configs.L2ChainNameRollupB,
		RethBHTTPPort, RethBWSPort, RethBAuthPort, RethBP2PPort, RethBMetrics,
		b.cfg.Flashblocks.RollupBP2PSecretKeyHex,
		chainBConfig.ID,
	)
	if err != nil {
		return nil, err
	}

	return []supervisor.ProcessSpec{specA, specB}, nil
}

// RbuilderSpecs returns ProcessSpecs for op-rbuilder-a and op-rbuilder-b.
// Only call when cfg.Flashblocks.Enabled is true.
func (b *Builder) RbuilderSpecs() ([]supervisor.ProcessSpec, error) {
	// op-rbuilder dials op-reth via P2P using op-reth's enode identity.
	_, rethAEnode, err := deriveEnodePubkey(b.cfg.Flashblocks.RollupAP2PSecretKeyHex)
	if err != nil {
		return nil, fmt.Errorf("derive rollup-a enode: %w", err)
	}
	_, rethBEnode, err := deriveEnodePubkey(b.cfg.Flashblocks.RollupBP2PSecretKeyHex)
	if err != nil {
		return nil, fmt.Errorf("derive rollup-b enode: %w", err)
	}

	specA := b.rbuilderSpec(
		configs.L2ChainNameRollupA,
		RbuilderAHTTPPort, RbuilderAEnginePort, RbuilderAFlashPort,
		RbuilderAP2PPort, RbuilderAMetrics,
		rethAEnode, RethAP2PPort,
	)
	specB := b.rbuilderSpec(
		configs.L2ChainNameRollupB,
		RbuilderBHTTPPort, RbuilderBEnginePort, RbuilderBFlashPort,
		RbuilderBP2PPort, RbuilderBMetrics,
		rethBEnode, RethBP2PPort,
	)
	return []supervisor.ProcessSpec{specA, specB}, nil
}

// RollupBoostSpecs returns ProcessSpecs for rollup-boost-a and rollup-boost-b.
// Only call when cfg.Flashblocks.Enabled is true.
func (b *Builder) RollupBoostSpecs() []supervisor.ProcessSpec {
	specA := b.rollupBoostSpec(
		configs.L2ChainNameRollupA,
		RethAAuthPort, RbuilderAEnginePort, RbuilderAFlashPort,
		RollupBoostAEnginePort, RollupBoostADebugPort, RollupBoostAFlashPort,
	)
	specB := b.rollupBoostSpec(
		configs.L2ChainNameRollupB,
		RethBAuthPort, RbuilderBEnginePort, RbuilderBFlashPort,
		RollupBoostBEnginePort, RollupBoostBDebugPort, RollupBoostBFlashPort,
	)
	return []supervisor.ProcessSpec{specA, specB}
}

// NodeBatcherProposerSpecs returns ProcessSpecs for op-node, op-batcher,
// and op-proposer on both chains (6 processes total).
func (b *Builder) NodeBatcherProposerSpecs() ([]supervisor.ProcessSpec, error) {
	type chainLayout struct {
		name         configs.L2ChainName
		engineRPC    int // Engine API endpoint for op-node (op-reth authrpc, or rollup-boost)
		rbuilderHTTP int // HTTP RPC for op-batcher (op-reth HTTP, or op-rbuilder)
		nodeRPC      int
		batchRPC     int
		propRPC      int
	}

	chains := []chainLayout{
		{
			name:         configs.L2ChainNameRollupA,
			engineRPC:    RethAAuthPort,
			rbuilderHTTP: RethAHTTPPort,
			nodeRPC:      NodeARPCPort,
			batchRPC:     BatcherARPCPort,
			propRPC:      ProposerARPCPort,
		},
		{
			name:         configs.L2ChainNameRollupB,
			engineRPC:    RethBAuthPort,
			rbuilderHTTP: RethBHTTPPort,
			nodeRPC:      NodeBRPCPort,
			batchRPC:     BatcherBRPCPort,
			propRPC:      ProposerBRPCPort,
		},
	}

	// When Flashblocks is enabled, op-node talks to rollup-boost (Engine API
	// multiplexer) and op-batcher talks to op-rbuilder (optimised block builder).
	if b.cfg.Flashblocks.Enabled {
		chains[0].engineRPC = RollupBoostAEnginePort
		chains[0].rbuilderHTTP = b.cfg.Flashblocks.RollupARPCPort
		chains[1].engineRPC = RollupBoostBEnginePort
		chains[1].rbuilderHTTP = b.cfg.Flashblocks.RollupBRPCPort
	}

	var specs []supervisor.ProcessSpec
	for _, c := range chains {
		nodeSpec := b.nodeSpec(c.name, c.engineRPC, c.nodeRPC)
		batchSpec := b.batcherSpec(c.name, c.rbuilderHTTP, c.nodeRPC, c.batchRPC)
		propSpec, err := b.proposerSpec(c.name, c.nodeRPC, c.propRPC)
		if err != nil {
			return nil, fmt.Errorf("proposer spec for %s: %w", c.name, err)
		}
		specs = append(specs, nodeSpec, batchSpec, propSpec)
	}
	return specs, nil
}

// PublisherSpec returns the ProcessSpec for the shared Publisher service.
// composeL2OOAddr and gameFactoryAddr come from the L1 deployment state.
func (b *Builder) PublisherSpec(registryDir string, composeL2OOAddr, gameFactoryAddr string) supervisor.ProcessSpec {
	// PROOFS_ENABLED/PROOFS_REQUIRE_PROOF are read nowhere in publisher's
	// code today (mock mode is achieved by fabricated proofs arriving from
	// the sidecars, not by a publisher-side flag) — kept here only for
	// forward compatibility, derived from MockMode so they stay coherent
	// with it.
	return supervisor.ProcessSpec{
		Name:   "publisher",
		Binary: b.bins.Publisher,
		Env: map[string]string{
			"SERVER_LISTEN_ADDR":        fmt.Sprintf(":%d", PublisherAPIPort),
			"API_LISTEN_ADDR":           fmt.Sprintf(":%d", PublisherMetricsPort),
			"METRICS_ENABLED":           "true",
			"METRICS_PORT":              fmt.Sprintf("%d", PublisherMetricsPort),
			"LOG_LEVEL":                 "debug",
			"LOG_PRETTY":                "true",
			"AUTH_ENABLED":              "false",
			"MOCK_MODE":                 strconv.FormatBool(b.cfg.MockMode),
			"PROOFS_ENABLED":            strconv.FormatBool(!b.cfg.MockMode),
			"PROOFS_REQUIRE_PROOF":      strconv.FormatBool(!b.cfg.MockMode),
			"CONSENSUS_TIMEOUT":         "20s",
			"CONSENSUS_PERIOD_DURATION": "60s",
			"CONSENSUS_PROOF_WINDOW":    "600s",
			"L1_RPC_ENDPOINT":           b.cfg.L1ElURL,
			"L1_SUPERBLOCK_CONTRACT":    composeL2OOAddr,
			"L1_SHARED_PUBLISHER_PK_HEX": b.cfg.Wallet.PrivateKey,
			"L1_FROM_ADDRESS":           b.cfg.Wallet.Address,
			"L1_DISPUTE_GAME_FACTORY":   gameFactoryAddr,
			"L1_CHAIN_ID":               fmt.Sprintf("%d", b.cfg.L1ChainID),
			"L1_ETHERA_NETWORK_NAME":    b.cfg.EtheraNetworkName,
			"REGISTRY_PATH":             registryDir,
			"SETTLEMENT_L1_RPC_URL":     b.cfg.L1ElURL,
			"SETTLEMENT_L2OO_ADDRESS":   composeL2OOAddr,
			"SETTLEMENT_PROPOSER_KEY":   b.cfg.Wallet.PrivateKey,
		},
	}
}

// SidecarSpecs returns ProcessSpecs for sidecar-a and sidecar-b.
// mailboxA/B are the UniversalBridgeMailbox contract addresses deployed on each chain.
func (b *Builder) SidecarSpecs(mailboxA, mailboxB string) []supervisor.ProcessSpec {
	chainAConfig := b.cfg.ChainConfigs[configs.L2ChainNameRollupA]
	chainBConfig := b.cfg.ChainConfigs[configs.L2ChainNameRollupB]

	specA := supervisor.ProcessSpec{
		Name:   "sidecar-a",
		Binary: b.bins.Sidecar,
		Env: map[string]string{
			"SIDECAR_LISTEN_ADDR":                       fmt.Sprintf("0.0.0.0:%d", SidecarAAPIPort),
			"SIDECAR_PUBLISHER_ENABLED":                 "true",
			"SIDECAR_PUBLISHER_ADDR":                    fmt.Sprintf("127.0.0.1:%d", PublisherAPIPort),
			"SIDECAR_CHAIN_ID":                          fmt.Sprintf("%d", chainAConfig.ID),
			"SIDECAR_CHAIN_RPC":                         fmt.Sprintf("http://127.0.0.1:%d", RbuilderAHTTPPort),
			"SIDECAR_UNIVERSAL_BRIDGE_MAILBOX_ADDRESS":  mailboxA,
			"SIDECAR_COORDINATOR_KEY":                   b.cfg.CoordinatorPrivateKey,
			"SIDECAR_PEERS":                             fmt.Sprintf("%d=http://127.0.0.1:%d", chainBConfig.ID, SidecarBAPIPort),
			"SIDECAR_LOG_LEVEL":                         "debug",
			"SIDECAR_LOG_FORMAT":                        "pretty",
			"SIDECAR_MOCK_PROOF_ENABLED":                 strconv.FormatBool(b.cfg.MockMode),
			"SIDECAR_MOCK_PROOF_PUBLISHER_HTTP_ADDR":     fmt.Sprintf("127.0.0.1:%d", PublisherMetricsPort),
			"SIDECAR_MOCK_PROOF_INTERVAL_SECS":           "60",
		},
	}
	specB := supervisor.ProcessSpec{
		Name:   "sidecar-b",
		Binary: b.bins.Sidecar,
		Env: map[string]string{
			"SIDECAR_LISTEN_ADDR":                       fmt.Sprintf("0.0.0.0:%d", SidecarBAPIPort),
			"SIDECAR_PUBLISHER_ENABLED":                 "true",
			"SIDECAR_PUBLISHER_ADDR":                    fmt.Sprintf("127.0.0.1:%d", PublisherAPIPort),
			"SIDECAR_CHAIN_ID":                          fmt.Sprintf("%d", chainBConfig.ID),
			"SIDECAR_CHAIN_RPC":                         fmt.Sprintf("http://127.0.0.1:%d", RbuilderBHTTPPort),
			"SIDECAR_UNIVERSAL_BRIDGE_MAILBOX_ADDRESS":  mailboxB,
			"SIDECAR_COORDINATOR_KEY":                   b.cfg.CoordinatorPrivateKey,
			"SIDECAR_PEERS":                             fmt.Sprintf("%d=http://127.0.0.1:%d", chainAConfig.ID, SidecarAAPIPort),
			"SIDECAR_LOG_LEVEL":                         "debug",
			"SIDECAR_LOG_FORMAT":                        "pretty",
			"SIDECAR_MOCK_PROOF_ENABLED":                 strconv.FormatBool(b.cfg.MockMode),
			"SIDECAR_MOCK_PROOF_PUBLISHER_HTTP_ADDR":     fmt.Sprintf("127.0.0.1:%d", PublisherMetricsPort),
			"SIDECAR_MOCK_PROOF_INTERVAL_SECS":           "60",
		},
	}
	return []supervisor.ProcessSpec{specA, specB}
}

// OtterscanSpecs returns ProcessSpecs for the two Expedition block explorer instances.
// Expedition works with standard eth_* JSON-RPC (unlike Otterscan which requires erigon_*).
// expeditionDir is the absolute path to the cloned + built Expedition source.
// The static build under expeditionDir/build/ is served via `npx serve` — this avoids
// the slow webpack dev-server startup and Node 17+ OpenSSL incompatibilities.
func (b *Builder) OtterscanSpecs(expeditionDir string) []supervisor.ProcessSpec {
	buildDir := filepath.Join(expeditionDir, "build")
	specA := supervisor.ProcessSpec{
		Name:   "explorer-a",
		Binary: b.bins.NPM,
		// `npx serve -s <dir> -p <port>` serves the CRA static build in SPA mode.
		Args: []string{"exec", "serve", "--", "-s", buildDir, "-p", fmt.Sprintf("%d", OtterscanAPort)},
		Dir:  expeditionDir,
	}
	specB := supervisor.ProcessSpec{
		Name:   "explorer-b",
		Binary: b.bins.NPM,
		Args:   []string{"exec", "serve", "--", "-s", buildDir, "-p", fmt.Sprintf("%d", OtterscanBPort)},
		Dir:    expeditionDir,
	}
	return []supervisor.ProcessSpec{specA, specB}
}

// FrontendSpec returns the ProcessSpec for the Ethera Labs Console (Vite dev server).
// frontendDir is the absolute path to the frontend/ source directory.
// deployedContracts maps contract names to their hex addresses.
func (b *Builder) FrontendSpec(frontendDir string, deployedContracts map[string]string) supervisor.ProcessSpec {
	chainAConfig := b.cfg.ChainConfigs[configs.L2ChainNameRollupA]
	chainBConfig := b.cfg.ChainConfigs[configs.L2ChainNameRollupB]
	port := b.cfg.Frontend.Port
	if port == 0 {
		port = 3000
	}
	env := map[string]string{
		"VITE_CHAIN_A_ID":                     fmt.Sprintf("%d", chainAConfig.ID),
		"VITE_CHAIN_B_ID":                     fmt.Sprintf("%d", chainBConfig.ID),
		"VITE_FLASHBLOCKS_ENABLED":            "true",
		"VITE_CHAIN_A_BUILDER_RPC":            fmt.Sprintf("http://localhost:%d", RbuilderAHTTPPort),
		"VITE_CHAIN_A_OP_RETH_RPC":            fmt.Sprintf("http://localhost:%d", RethAHTTPPort),
		"VITE_CHAIN_B_BUILDER_RPC":            fmt.Sprintf("http://localhost:%d", RbuilderBHTTPPort),
		"VITE_CHAIN_B_OP_RETH_RPC":            fmt.Sprintf("http://localhost:%d", RethBHTTPPort),
		"VITE_SIDECAR_A_URL":                  fmt.Sprintf("http://localhost:%d", SidecarAAPIPort),
		"VITE_SIDECAR_B_URL":                  fmt.Sprintf("http://localhost:%d", SidecarBAPIPort),
		"VITE_HEALTH_API_URL":                 fmt.Sprintf("http://localhost:%d", SidecarAAPIPort),
		"VITE_WALLET_PRIVATE_KEY":             b.cfg.Wallet.PrivateKey,
		"VITE_CHAIN_A_BRIDGE_ADDRESS":         deployedContracts["bridge"],
		"VITE_CHAIN_B_BRIDGE_ADDRESS":         deployedContracts["bridge"],
		"VITE_CHAIN_A_TOKEN_ADDRESS":          deployedContracts["token"],
		"VITE_CHAIN_B_TOKEN_ADDRESS":          deployedContracts["token"],
		"VITE_CET_FACTORY_ADDRESS":            deployedContracts["cetFactory"],
		"VITE_CHAIN_A_ETH_LIQUIDITY_ADDRESS":  deployedContracts["ethLiquidity"],
		"VITE_CHAIN_B_ETH_LIQUIDITY_ADDRESS":  deployedContracts["ethLiquidity"],
		"VITE_ENTRYPOINT_A":                   deployedContracts["entryPoint"],
		"VITE_ENTRYPOINT_B":                   deployedContracts["entryPoint"],
		"VITE_SIMPLE_ACCOUNT_FACTORY_A":       deployedContracts["simpleAccountFactory"],
		"VITE_SIMPLE_ACCOUNT_FACTORY_B":       deployedContracts["simpleAccountFactory"],
		"VITE_BUNDLER_A_URL":                  fmt.Sprintf("http://localhost:%d", 17082),
		"VITE_BUNDLER_B_URL":                  fmt.Sprintf("http://localhost:%d", 27082),
		"VITE_CHAIN_A_EXPLORER_URL":           fmt.Sprintf("http://localhost:%d", OtterscanAPort),
		"VITE_CHAIN_B_EXPLORER_URL":           fmt.Sprintf("http://localhost:%d", OtterscanBPort),
	}
	return supervisor.ProcessSpec{
		Name:   "frontend",
		Binary: b.bins.NPM,
		Args:   []string{"run", "dev", "--", "--host", "0.0.0.0", "--port", fmt.Sprintf("%d", port)},
		Dir:    frontendDir,
		Env:    env,
	}
}

// ---------------------------------------------------------------------------
// internal spec builders
// ---------------------------------------------------------------------------

func (b *Builder) rethSpec(chain configs.L2ChainName, httpPort, wsPort, authPort, p2pPort, metricsPort int, p2pSecretHex string, chainID int) (supervisor.ProcessSpec, error) {
	sk, err := normalizePeerKey(p2pSecretHex)
	if err != nil {
		return supervisor.ProcessSpec{}, fmt.Errorf("derive p2p key for %s: %w", chain, err)
	}
	configPath := filepath.Join(b.networksDir, string(chain))
	dataDir := filepath.Join(b.dataDir, string(chain), "reth")
	return supervisor.ProcessSpec{
		Name:   "op-reth-" + chainSuffix(chain),
		Binary: b.bins.OpReth,
		Args: []string{
			"node",
			"--chain", filepath.Join(configPath, "genesis.json"),
			"--datadir", dataDir,
			"--http",
			"--http.addr", "0.0.0.0",
			"--http.port", fmt.Sprintf("%d", httpPort),
			"--http.corsdomain", "*",
			"--http.api", "web3,debug,eth,txpool,net,miner",
			"--ws",
			"--ws.addr", "0.0.0.0",
			"--ws.port", fmt.Sprintf("%d", wsPort),
			"--ws.origins", "*",
			"--ws.api", "web3,debug,eth,txpool,net,miner",
			"--authrpc.addr", "127.0.0.1",
			"--authrpc.port", fmt.Sprintf("%d", authPort),
			"--authrpc.jwtsecret", filepath.Join(configPath, "jwt.txt"),
			"--disable-tx-gossip",
			"--disable-discovery",
			"--max-peers", "10",
			"--port", fmt.Sprintf("%d", p2pPort),
			"--p2p-secret-key-hex", sk,
			"--nat", "none",
			"--network-id", fmt.Sprintf("%d", chainID),
			"--metrics", fmt.Sprintf("0.0.0.0:%d", metricsPort),
			"--ipcpath", fmt.Sprintf("/tmp/reth-%s.ipc", chainSuffix(chain)),
			"--rollup.compute-pending-block",
		},
	}, nil
}

func (b *Builder) rbuilderSpec(chain configs.L2ChainName, httpPort, enginePort, flashPort, p2pPort, metricsPort int, rethEnodePubkey string, rethP2PPort int) supervisor.ProcessSpec {
	configPath := filepath.Join(b.networksDir, string(chain))
	dataDir := filepath.Join(b.dataDir, string(chain), "rbuilder")
	suffix := chainSuffix(chain)
	return supervisor.ProcessSpec{
		Name:   "op-rbuilder-" + suffix,
		Binary: b.bins.OpRbuilder,
		Args: []string{
			"node",
			"--chain", filepath.Join(configPath, "genesis.json"),
			"--datadir", dataDir,
			"--http",
			"--http.addr", "0.0.0.0",
			"--http.port", fmt.Sprintf("%d", httpPort),
			"--http.corsdomain", "*",
			"--http.api", "eth,net,web3,debug,txpool",
			"--authrpc.addr", "0.0.0.0",
			"--authrpc.port", fmt.Sprintf("%d", enginePort),
			"--authrpc.jwtsecret", filepath.Join(configPath, "jwt.txt"),
			"--metrics", fmt.Sprintf("0.0.0.0:%d", metricsPort),
			"--flashblocks.enabled",
			"--flashblocks.addr", "0.0.0.0",
			"--flashblocks.port", fmt.Sprintf("%d", flashPort),
			"--flashblocks.fixed",
			"--disable-discovery",
			"--max-peers", "5",
			"--port", fmt.Sprintf("%d", p2pPort),
			"--nat", "none",
			"--trusted-only",
			"--trusted-peers", fmt.Sprintf("enode://%s@127.0.0.1:%d", rethEnodePubkey, rethP2PPort),
			"--ipcpath", fmt.Sprintf("/tmp/rbuilder-%s.ipc", suffix),
		},
	}
}

func (b *Builder) rollupBoostSpec(chain configs.L2ChainName, rethAuthPort, rbuilderEnginePort, rbuilderFlashPort, enginePort, debugPort, flashPort int) supervisor.ProcessSpec {
	jwtPath := filepath.Join(b.networksDir, string(chain), "jwt.txt")
	return supervisor.ProcessSpec{
		Name:   "rollup-boost-" + chainSuffix(chain),
		Binary: b.bins.RollupBoost,
		// rollup-boost reads all configuration from env vars.
		Env: map[string]string{
			"L2_URL":                  fmt.Sprintf("http://127.0.0.1:%d", rethAuthPort),
			"L2_JWT_PATH":             jwtPath,
			"BUILDER_URL":             fmt.Sprintf("http://127.0.0.1:%d", rbuilderEnginePort),
			"BUILDER_JWT_PATH":        jwtPath,
			"RPC_HOST":                "0.0.0.0",
			"RPC_PORT":                fmt.Sprintf("%d", enginePort),
			"DEBUG_HOST":              "0.0.0.0",
			"DEBUG_SERVER_PORT":       fmt.Sprintf("%d", debugPort),
			"FLASHBLOCKS":             "true",
			"FLASHBLOCKS_BUILDER_URL": fmt.Sprintf("ws://127.0.0.1:%d", rbuilderFlashPort),
			"FLASHBLOCKS_HOST":        "0.0.0.0",
			"FLASHBLOCKS_PORT":        fmt.Sprintf("%d", flashPort),
			"LOG_LEVEL":               "info",
		},
	}
}

func (b *Builder) nodeSpec(chain configs.L2ChainName, rethAuthPort, nodeRPCPort int) supervisor.ProcessSpec {
	configPath := filepath.Join(b.networksDir, string(chain))
	return supervisor.ProcessSpec{
		Name:   "op-node-" + chainSuffix(chain),
		Binary: b.bins.OpNode,
		Env: map[string]string{
			"OP_NODE_L1_ETH_RPC":             b.cfg.L1ElURL,
			"OP_NODE_L1_BEACON":              b.cfg.L1ClURL,
			"OP_NODE_L2_ENGINE_RPC":          fmt.Sprintf("http://127.0.0.1:%d", rethAuthPort),
			"OP_NODE_L2_ENGINE_AUTH":         filepath.Join(configPath, "jwt.txt"),
			"OP_NODE_L2_ENGINE_KIND":         "reth",
			"OP_NODE_ROLLUP_CONFIG":          filepath.Join(configPath, "rollup.json"),
			"OP_NODE_ROLLUP_L1_CHAIN_CONFIG": filepath.Join(configPath, "l1-chainconfig.json"),
			"OP_NODE_P2P_DISABLE":            "true",
			"OP_NODE_SEQUENCER_ENABLED":      "true",
			"OP_NODE_SEQUENCER_L1_CONFS":     "0",
			"OP_NODE_VERIFIER_L1_CONFS":      "0",
			"OP_NODE_P2P_SEQUENCER_KEY":      b.cfg.CoordinatorPrivateKey,
			"OP_NODE_RPC_ADDR":               "0.0.0.0",
			"OP_NODE_RPC_PORT":               fmt.Sprintf("%d", nodeRPCPort),
			"OP_NODE_RPC_ENABLE_ADMIN":       "true",
			"OP_NODE_LOG_LEVEL":              "info",
		},
	}
}

func (b *Builder) batcherSpec(chain configs.L2ChainName, rethHTTPPort, nodeRPCPort, batcherRPCPort int) supervisor.ProcessSpec {
	return supervisor.ProcessSpec{
		Name:   "op-batcher-" + chainSuffix(chain),
		Binary: b.bins.OpBatcher,
		Env: map[string]string{
			"OP_BATCHER_L1_ETH_RPC":           b.cfg.L1ElURL,
			"OP_BATCHER_L2_ETH_RPC":           fmt.Sprintf("http://127.0.0.1:%d", rethHTTPPort),
			"OP_BATCHER_ROLLUP_RPC":           fmt.Sprintf("http://127.0.0.1:%d", nodeRPCPort),
			"OP_BATCHER_PRIVATE_KEY":          b.cfg.Wallet.PrivateKey,
			"OP_BATCHER_POLL_INTERVAL":        "1s",
			"OP_BATCHER_SUB_SAFETY_MARGIN":    "6",
			"OP_BATCHER_NUM_CONFIRMATIONS":    "1",
			"OP_BATCHER_MAX_CHANNEL_DURATION": "25",
			"OP_BATCHER_RPC_ADDR":             "0.0.0.0",
			"OP_BATCHER_RPC_PORT":             fmt.Sprintf("%d", batcherRPCPort),
			"OP_BATCHER_RPC_ENABLE_ADMIN":     "true",
		},
	}
}

func (b *Builder) proposerSpec(chain configs.L2ChainName, nodeRPCPort, proposerRPCPort int) (supervisor.ProcessSpec, error) {
	runtimeEnv, err := readEnvFile(filepath.Join(b.networksDir, string(chain), "runtime.env"))
	if err != nil {
		return supervisor.ProcessSpec{}, fmt.Errorf("read runtime.env for %s: %w", chain, err)
	}

	env := map[string]string{
		"OP_PROPOSER_L1_ETH_RPC":        b.cfg.L1ElURL,
		"OP_PROPOSER_ROLLUP_RPC":        fmt.Sprintf("http://127.0.0.1:%d", nodeRPCPort),
		"OP_PROPOSER_PRIVATE_KEY":       b.cfg.Wallet.PrivateKey,
		"OP_PROPOSER_POLL_INTERVAL":     "12s",
		"OP_PROPOSER_PROPOSAL_INTERVAL": "10m",
		"OP_PROPOSER_GAME_TYPE":         "1",
		"OP_PROPOSER_RPC_PORT":          fmt.Sprintf("%d", proposerRPCPort),
		"OP_PROPOSER_RPC_ADDR":          "0.0.0.0",
		"OP_PROPOSER_RPC_ENABLE_ADMIN":  "true",
	}
	for k, v := range runtimeEnv {
		env[k] = v
	}

	return supervisor.ProcessSpec{
		Name:   "op-proposer-" + chainSuffix(chain),
		Binary: b.bins.OpProposer,
		Env:    env,
	}, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		result[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return result, scanner.Err()
}

// normalizePeerKey strips the optional 0x prefix and validates the key via
// go-ethereum's ECDSA parser.
func normalizePeerKey(hexKey string) (string, error) {
	sk := strings.TrimPrefix(hexKey, "0x")
	if _, err := crypto.HexToECDSA(sk); err != nil {
		return "", fmt.Errorf("invalid p2p secret key: %w", err)
	}
	return sk, nil
}

// deriveEnodePubkey returns the (normalized secret, 64-byte uncompressed
// enode public key hex) for the given secp256k1 secret.
func deriveEnodePubkey(secretHex string) (string, string, error) {
	sk := strings.TrimPrefix(secretHex, "0x")
	priv, err := crypto.HexToECDSA(sk)
	if err != nil {
		return "", "", fmt.Errorf("invalid p2p secret key: %w", err)
	}
	pub := crypto.FromECDSAPub(&priv.PublicKey) // 65 bytes: 0x04 || X || Y
	return sk, hex.EncodeToString(pub[1:]), nil
}

// chainSuffix converts a chain name constant to the short letter used in
// process names ("a" / "b").
func chainSuffix(chain configs.L2ChainName) string {
	switch chain {
	case configs.L2ChainNameRollupA:
		return "a"
	case configs.L2ChainNameRollupB:
		return "b"
	default:
		return string(chain)
	}
}
