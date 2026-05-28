package l2runtime

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethera-labs/local-testnet/internal/l2/infra/supervisor"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/genesis"
	"github.com/ethera-labs/local-testnet/internal/l2/l2config/secrets"
	"github.com/ethera-labs/local-testnet/internal/l2/l2runtime/contracts"
	"github.com/ethera-labs/local-testnet/internal/l2/l2runtime/native"
	"github.com/ethera-labs/local-testnet/internal/l2/l2runtime/services"
	"github.com/ethera-labs/local-testnet/internal/logger"
	"github.com/ethereum/go-ethereum/common"
)

// Orchestrator coordinates Phase 3: native L2 runtime operations.
//
//   - Inits op-reth datadirs if not already initialised
//   - Starts op-reth-a and op-reth-b as native processes
//   - Waits for their HTTP ports to accept connections
//   - Starts op-node, op-batcher, and op-proposer for both chains
//   - Deploys L2 helper contracts (unchanged from the Docker flow)
type Orchestrator struct {
	rootDir     string
	localnetDir string
	networksDir string
	servicesDir string
	logger      *slog.Logger
}

// NewOrchestrator creates a new Phase 3 orchestrator.
func NewOrchestrator(rootDir, localnetDir, networksDir, servicesDir string) *Orchestrator {
	return &Orchestrator{
		rootDir:     rootDir,
		localnetDir: localnetDir,
		networksDir: networksDir,
		servicesDir: servicesDir,
		logger:      logger.Named("l2_runtime_orchestrator"),
	}
}

// Execute runs Phase 3: start native OP stack processes, deploy contracts.
func (o *Orchestrator) Execute(ctx context.Context, cfg configs.L2, gameFactoryAddr common.Address, composeL2OOAddr common.Address) (map[configs.L2ChainName]map[contracts.ContractName]common.Address, error) {
	o.logger.Info("Phase 3: Starting L2 runtime operations (native process mode)")

	// ------------------------------------------------------------------
	// 1. Resolve native binary paths
	// ------------------------------------------------------------------
	bins, err := o.resolveBinaries(cfg.Flashblocks.Enabled)
	if err != nil {
		return nil, err
	}

	// ------------------------------------------------------------------
	// 2. Build process spec factory and process supervisor
	// ------------------------------------------------------------------
	dataDir := filepath.Join(o.localnetDir, "data")
	logDir := filepath.Join(o.localnetDir, "logs")

	builder := native.NewBuilder(cfg, bins, o.networksDir, dataDir)
	sup := supervisor.New(logDir)
	manager := services.NewNativeManager(builder, sup)

	// ------------------------------------------------------------------
	// 3. Wait for required network files (genesis.json, jwt.txt)
	// ------------------------------------------------------------------
	if err := o.waitForNetworkFiles(); err != nil {
		return nil, fmt.Errorf("required network files not ready: %w", err)
	}

	// ------------------------------------------------------------------
	// 4. Init + start op-reth
	// ------------------------------------------------------------------
	if err := manager.StartReth(ctx); err != nil {
		return nil, fmt.Errorf("failed to start op-reth: %w", err)
	}

	// ------------------------------------------------------------------
	// 5. Wait for op-reth HTTP ports
	// ------------------------------------------------------------------
	if err := manager.WaitRethReady(ctx); err != nil {
		return nil, fmt.Errorf("op-reth readiness check failed: %w", err)
	}

	// ------------------------------------------------------------------
	// 5b. Start Flashblocks native processes (op-rbuilder + rollup-boost)
	//     op-rbuilder must be ready before rollup-boost starts;
	//     rollup-boost must be ready before op-node connects to it.
	// ------------------------------------------------------------------
	if cfg.Flashblocks.Enabled {
		if err := o.startFlashblocksNative(ctx, manager); err != nil {
			return nil, fmt.Errorf("failed to start flashblocks services: %w", err)
		}
	}

	// ------------------------------------------------------------------
	// 6. Start op-node, op-batcher, op-proposer
	// ------------------------------------------------------------------
	if err := manager.StartNodeBatcherProposer(ctx); err != nil {
		return nil, fmt.Errorf("failed to start op-node/batcher/proposer: %w", err)
	}

	// ------------------------------------------------------------------
	// 7. Deploy L2 contracts (unchanged)
	// ------------------------------------------------------------------
	effectiveChainConfigs := cfg.ChainConfigs
	if cfg.Flashblocks.Enabled {
		effectiveChainConfigs = o.getFlashblocksChainConfigs(cfg)
		o.logger.Info("using flashblocks RPC ports for contract deployment",
			"rollup_a_port", effectiveChainConfigs[configs.L2ChainNameRollupA].RPCPort,
			"rollup_b_port", effectiveChainConfigs[configs.L2ChainNameRollupB].RPCPort)
	}

	contractDeployer := contracts.NewDeployer(o.networksDir)
	deployedContracts, err := contractDeployer.Deploy(ctx, effectiveChainConfigs, cfg.CoordinatorPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to deploy contracts: %w", err)
	}

	if _, _, err := mailboxAddresses(deployedContracts); err != nil {
		return nil, fmt.Errorf("failed to resolve mailbox addresses: %w", err)
	}

	// ------------------------------------------------------------------
	// 8. Start sidecar stack (publisher + sidecar-a/b) when enabled.
	//    Must happen after contract deployment so mailbox addresses are known.
	// ------------------------------------------------------------------
	if cfg.Sidecar.Enabled {
		if err := o.startSidecarStack(ctx, cfg, manager, gameFactoryAddr, composeL2OOAddr, deployedContracts); err != nil {
			return nil, fmt.Errorf("failed to start sidecar stack: %w", err)
		}
	}

	// ------------------------------------------------------------------
	// 9. Start the Ethera Labs Console (Vite dev server) when enabled.
	// ------------------------------------------------------------------
	if cfg.Frontend.Enabled || cfg.Frontend.DevEnabled {
		if err := o.startFrontend(ctx, cfg, manager, deployedContracts); err != nil {
			return nil, fmt.Errorf("failed to start frontend: %w", err)
		}
	}

	// ------------------------------------------------------------------
	// 10. Supervise: log if any process dies unexpectedly
	// ------------------------------------------------------------------
	go func() {
		if err := sup.Wait(ctx); err != nil {
			o.logger.Error("a supervised L2 process exited unexpectedly", "error", err)
		}
	}()

	o.logger.Info("Phase 3: L2 runtime operations completed successfully")
	return deployedContracts, nil
}

// startSidecarStack starts the publisher, then sidecar-a and sidecar-b after
// contract deployment so mailbox addresses are available.
func (o *Orchestrator) startSidecarStack(
	ctx context.Context,
	cfg configs.L2,
	manager *services.NativeManager,
	gameFactoryAddr, composeL2OOAddr common.Address,
	deployedContracts map[configs.L2ChainName]map[contracts.ContractName]common.Address,
) error {
	o.logger.Info("starting sidecar stack (publisher + sidecar-a/b) natively")

	mailboxA, mailboxB, err := mailboxAddresses(deployedContracts)
	if err != nil {
		return fmt.Errorf("resolve mailbox addresses: %w", err)
	}

	// Ensure registry directory exists (publisher reads chain definitions from it).
	registryDir := filepath.Join(o.localnetDir, "registry")
	if err := os.MkdirAll(registryDir, 0o755); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}

	dataDir := filepath.Join(o.localnetDir, "data")
	networksDir := o.networksDir
	bins, err := o.resolveBinaries(cfg.Flashblocks.Enabled)
	if err != nil {
		return fmt.Errorf("resolve binaries for sidecar stack: %w", err)
	}
	builder := native.NewBuilder(cfg, bins, networksDir, dataDir)

	pubSpec := builder.PublisherSpec(registryDir, composeL2OOAddr.Hex(), gameFactoryAddr.Hex())
	if err := manager.StartPublisher(ctx, pubSpec); err != nil {
		return fmt.Errorf("start publisher: %w", err)
	}
	if err := manager.WaitPublisherReady(ctx); err != nil {
		return fmt.Errorf("publisher readiness: %w", err)
	}

	sidecarSpecs := builder.SidecarSpecs(mailboxA.Hex(), mailboxB.Hex())
	if err := manager.StartSidecars(ctx, sidecarSpecs); err != nil {
		return fmt.Errorf("start sidecars: %w", err)
	}
	if err := manager.WaitSidecarsReady(ctx); err != nil {
		return fmt.Errorf("sidecar readiness: %w", err)
	}
	return nil
}

// startFrontend starts the Ethera Labs Console (Vite dev server via npm run dev).
func (o *Orchestrator) startFrontend(
	ctx context.Context,
	cfg configs.L2,
	manager *services.NativeManager,
	deployedContracts map[configs.L2ChainName]map[contracts.ContractName]common.Address,
) error {
	frontendDir := filepath.Join(o.rootDir, "frontend")
	if _, err := os.Stat(frontendDir); err != nil {
		return fmt.Errorf("frontend directory not found at %s: %w", frontendDir, err)
	}

	// Flatten deployed contract addresses for the frontend env vars.
	chainA := deployedContracts[configs.L2ChainNameRollupA]
	contractMap := map[string]string{
		"bridge":               chainA[contracts.ContractNameComposeL2ToL2Bridge].Hex(),
		"token":                chainA[contracts.ContractNameTestToken].Hex(),
		"cetFactory":           chainA[contracts.ContractNameCetFactory].Hex(),
		"ethLiquidity":         chainA[contracts.ContractNameComposeETHLiquidity].Hex(),
		"entryPoint":           chainA[contracts.ContractNameEntryPoint].Hex(),
		"simpleAccountFactory": chainA[contracts.ContractNameSimpleAccountFactory].Hex(),
	}

	dataDir := filepath.Join(o.localnetDir, "data")
	bins, err := o.resolveBinaries(cfg.Flashblocks.Enabled)
	if err != nil {
		return fmt.Errorf("resolve binaries for frontend: %w", err)
	}
	builder := native.NewBuilder(cfg, bins, o.networksDir, dataDir)
	spec := builder.FrontendSpec(frontendDir, contractMap)

	// Install npm dependencies if node_modules is absent.
	nodeModules := filepath.Join(frontendDir, "node_modules")
	if _, err := os.Stat(nodeModules); err != nil {
		o.logger.Info("running npm install for frontend (first run)")
		cmd := exec.CommandContext(ctx, bins.NPM, "install")
		cmd.Dir = frontendDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("npm install failed: %w", err)
		}
	}

	return manager.StartFrontend(ctx, spec)
}

// startFlashblocksNative starts op-rbuilder and rollup-boost as native
// processes, waiting for each tier to be ready before starting the next.
func (o *Orchestrator) startFlashblocksNative(ctx context.Context, manager *services.NativeManager) error {
	o.logger.Info("starting flashblocks services natively (op-rbuilder + rollup-boost)")

	if err := manager.StartRbuilder(ctx); err != nil {
		return fmt.Errorf("start op-rbuilder: %w", err)
	}
	if err := manager.WaitRbuilderReady(ctx); err != nil {
		return fmt.Errorf("op-rbuilder readiness: %w", err)
	}

	if err := manager.StartRollupBoost(ctx); err != nil {
		return fmt.Errorf("start rollup-boost: %w", err)
	}
	if err := manager.WaitRollupBoostReady(ctx); err != nil {
		return fmt.Errorf("rollup-boost readiness: %w", err)
	}
	return nil
}

// resolveBinaries locates all required native binaries: first in
// .localnet/bin/, then on PATH.
func (o *Orchestrator) resolveBinaries(flashblocksEnabled bool) (native.Binaries, error) {
	required := []string{"op-reth", "op-node", "op-batcher", "op-proposer"}
	if flashblocksEnabled {
		required = append(required, "op-rbuilder", "rollup-boost")
	}

	resolved := make(map[string]string, len(required))
	for _, name := range required {
		path, err := resolveBinary(o.rootDir, name)
		if err != nil {
			return native.Binaries{}, fmt.Errorf("failed to locate binary %q: %w", name, err)
		}
		resolved[name] = path
	}

	// publisher and sidecar are resolved lazily (only when sidecar.enabled=true).
	publisherPath, _ := resolveBinary(o.rootDir, "publisher")
	sidecarPath, _ := resolveBinary(o.rootDir, "sidecar")

	// npm is the frontend runner — resolve from PATH only.
	npmPath, _ := exec.LookPath("npm")

	return native.Binaries{
		OpReth:      resolved["op-reth"],
		OpNode:      resolved["op-node"],
		OpBatcher:   resolved["op-batcher"],
		OpProposer:  resolved["op-proposer"],
		OpRbuilder:  resolved["op-rbuilder"],
		RollupBoost: resolved["rollup-boost"],
		Publisher:   publisherPath,
		Sidecar:     sidecarPath,
		NPM:         npmPath,
	}, nil
}

// resolveBinary locates a named binary: first in .localnet/bin/, then in PATH.
func resolveBinary(rootDir, name string) (string, error) {
	localBin := filepath.Join(rootDir, ".localnet", "bin", name)
	if _, err := os.Stat(localBin); err == nil {
		return localBin, nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%q not found in %s or PATH", name, filepath.Join(rootDir, ".localnet/bin"))
	}
	return path, nil
}

// waitForNetworkFiles blocks until the genesis.json and jwt.txt files for
// both chains exist and are non-empty, or until 120 seconds have elapsed.
func (o *Orchestrator) waitForNetworkFiles() error {
	type fileSpec struct {
		path  string
		label string
	}
	files := []fileSpec{
		{
			path:  filepath.Join(o.networksDir, string(configs.L2ChainNameRollupA), genesis.GenesisFileName),
			label: "rollup-a genesis",
		},
		{
			path:  filepath.Join(o.networksDir, string(configs.L2ChainNameRollupA), secrets.JWTFileName),
			label: "rollup-a jwt",
		},
		{
			path:  filepath.Join(o.networksDir, string(configs.L2ChainNameRollupB), genesis.GenesisFileName),
			label: "rollup-b genesis",
		},
		{
			path:  filepath.Join(o.networksDir, string(configs.L2ChainNameRollupB), secrets.JWTFileName),
			label: "rollup-b jwt",
		},
	}

	deadline := time.Now().Add(120 * time.Second)
	for {
		missing := make([]fileSpec, 0, len(files))
		for _, f := range files {
			info, err := os.Stat(f.path)
			if err != nil || info.Size() == 0 {
				missing = append(missing, f)
			}
		}

		if len(missing) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			parts := make([]string, 0, len(missing))
			for _, f := range missing {
				parts = append(parts, fmt.Sprintf("%s(%s)", f.label, f.path))
			}
			return fmt.Errorf("missing files: %s", strings.Join(parts, " "))
		}

		time.Sleep(1 * time.Second)
	}
}

// mailboxAddresses extracts the UniversalBridgeMailbox addresses for both chains.
func mailboxAddresses(deployedContracts map[configs.L2ChainName]map[contracts.ContractName]common.Address) (common.Address, common.Address, error) {
	mailboxA := deployedContracts[configs.L2ChainNameRollupA][contracts.ContractNameUniversalBridgeMailbox]
	mailboxB := deployedContracts[configs.L2ChainNameRollupB][contracts.ContractNameUniversalBridgeMailbox]
	if mailboxA == (common.Address{}) || mailboxB == (common.Address{}) {
		return common.Address{}, common.Address{}, fmt.Errorf("mailbox addresses not found in deployed contracts")
	}
	return mailboxA, mailboxB, nil
}

// getFlashblocksChainConfigs returns chain configs with op-rbuilder RPC ports
// substituted in when flashblocks is enabled.
func (o *Orchestrator) getFlashblocksChainConfigs(cfg configs.L2) map[configs.L2ChainName]configs.Chain {
	result := make(map[configs.L2ChainName]configs.Chain)

	for chainName, chainCfg := range cfg.ChainConfigs {
		modifiedCfg := chainCfg
		switch chainName {
		case configs.L2ChainNameRollupA:
			if cfg.Flashblocks.RollupARPCPort > 0 {
				modifiedCfg.RPCPort = cfg.Flashblocks.RollupARPCPort
			}
		case configs.L2ChainNameRollupB:
			if cfg.Flashblocks.RollupBRPCPort > 0 {
				modifiedCfg.RPCPort = cfg.Flashblocks.RollupBRPCPort
			}
		}
		result[chainName] = modifiedCfg
	}

	return result
}
