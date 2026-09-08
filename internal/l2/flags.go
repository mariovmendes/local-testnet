package l2

import (
	"github.com/spf13/viper"
)

// flagDef defines a command-line flag with its configuration.
type (
	flagType interface {
		string | int | bool
	}

	flagDef[T flagType] struct {
		name         string
		viperKey     string
		defaultValue T
		description  string
	}
)

var (
	stringFlags = []flagDef[string]{
		// L1 connection
		{"l1-el-url", "l2.l1-el-url", "", "L1 execution layer RPC URL"},
		{"l1-cl-url", "l2.l1-cl-url", "", "L1 consensus layer RPC URL"},
		{"ethera-labs-name", "l2.ethera-labs-name", "", "Ethera Labs network name for publisher registry"},
		{"validator-el", "l2.validator-el", "op-reth", "Rollup-boost validator execution client (L2_URL): op-reth | op-besu. op-besu requires flashblocks + a local local/op-besu:dev image"},

		// Wallet
		{"wallet-private-key", "l2.wallet.private-key", "", "Deployer wallet private key"},
		{"wallet-address", "l2.wallet.address", "", "Deployer wallet address"},
		{"coordinator-private-key", "l2.coordinator-private-key", "", "Coordinator private key"},

		// Deployment
		{"deployment-target", "l2.deployment-target", "live", "Deployment target (live or calldata)"},
		{"genesis-balance-wei", "l2.genesis-balance-wei", "100000000000000000000000", "Genesis balance in wei for funded accounts (default: 100_000 ETH)"},

		// Repositories (no defaults - must be explicitly set in config or via CLI)
		{"op-rbuilder-url", "l2.repositories.op-rbuilder.url", "", "op-rbuilder repository URL"},
		{"op-rbuilder-branch", "l2.repositories.op-rbuilder.branch", "", "op-rbuilder repository branch"},
		{"publisher-url", "l2.repositories.publisher.url", "", "publisher repository URL"},
		{"publisher-branch", "l2.repositories.publisher.branch", "", "publisher repository branch"},
		{"sidecar-url", "l2.repositories.sidecar.url", "", "sidecar repository URL"},
		{"sidecar-branch", "l2.repositories.sidecar.branch", "", "sidecar repository branch"},
		{"ethera-contracts-url", "l2.repositories.ethera-contracts.url", "", "ethera-contracts repository URL"},
		{"ethera-contracts-branch", "l2.repositories.ethera-contracts.branch", "", "ethera-contracts repository branch"},
		{"bundler-url", "l2.repositories.bundler.url", "", "ethera-bundler repository URL"},
		{"bundler-branch", "l2.repositories.bundler.branch", "", "ethera-bundler repository branch"},
		{"account-abstraction-url", "l2.repositories.account-abstraction.url", "https://github.com/eth-infinitism/account-abstraction.git", "eth-infinitism/account-abstraction repository URL"},
		{"account-abstraction-branch", "l2.repositories.account-abstraction.branch", "releases/v0.7", "eth-infinitism/account-abstraction repository branch"},
		{"op-succinct-url", "l2.repositories.op-succinct.url", "", "op-succinct repository URL"},
		{"op-succinct-branch", "l2.repositories.op-succinct.branch", "", "op-succinct repository branch"},

		// Flashblocks P2P identity (deterministic enode keypair for op-reth so op-rbuilder can dial it as a trusted peer)
		{"flashblocks-rollup-a-p2p-secret-key-hex", "l2.flashblocks.rollup-a-p2p-secret-key-hex", "0101010101010101010101010101010101010101010101010101010101010101", "Rollup A op-reth P2P secret key (32-byte hex)"},
		{"flashblocks-rollup-b-p2p-secret-key-hex", "l2.flashblocks.rollup-b-p2p-secret-key-hex", "0202020202020202020202020202020202020202020202020202020202020202", "Rollup B op-reth P2P secret key (32-byte hex)"},

		// Images
		{"op-deployer-tag", "l2.images.op-deployer.tag", "v0.4.5", "op-deployer image tag"},
		{"op-node-tag", "l2.images.op-node.tag", "v1.16.2", "op-node image tag"},
		{"op-proposer-tag", "l2.images.op-proposer.tag", "v1.10.0", "op-proposer image tag"},
		{"op-batcher-tag", "l2.images.op-batcher.tag", "v1.16.2", "op-batcher image tag"},
		{"op-reth-tag", "l2.images.op-reth.tag", "v1.11.5", "op-reth image tag"},

		// Dispute config
		{"dispute-network-name", "l2.dispute.network-name", "", "Dispute network name"},
		{"dispute-explorer-url", "l2.dispute.explorer-url", "", "Dispute explorer URL"},
		{"dispute-explorer-api-url", "l2.dispute.explorer-api-url", "", "Dispute explorer API URL"},
		{"dispute-verifier-address", "l2.dispute.verifier-address", "", "Verifier contract address"},
		{"dispute-owner-address", "l2.dispute.owner-address", "", "Owner address"},
		{"dispute-proposer-address", "l2.dispute.proposer-address", "", "Proposer address"},
		{"dispute-aggregation-vkey", "l2.dispute.aggregation-vkey", "", "Aggregation verification key"},
		{"dispute-guardian-address", "l2.dispute.guardian-address", "", "Guardian address"},
		{"dispute-game-init-bond", "l2.dispute.dispute-game-init-bond", "80000000000000000", "Initial bond for dispute games in wei"},
	}

	intFlags = []flagDef[int]{
		// L1 connection
		{"l1-chain-id", "l2.l1-chain-id", 0, "L1 chain ID"},

		// Chain configs
		{"rollup-a-id", "l2.chain-configs.rollup-a.id", 77777, "Rollup A chain ID"},
		{"rollup-a-rpc-port", "l2.chain-configs.rollup-a.rpc-port", 18545, "Rollup A RPC port"},
		{"rollup-b-id", "l2.chain-configs.rollup-b.id", 88888, "Rollup B chain ID"},
		{"rollup-b-rpc-port", "l2.chain-configs.rollup-b.rpc-port", 28545, "Rollup B RPC port"},

		// Flashblocks
		{"flashblocks-rollup-a-rpc-port", "l2.flashblocks.rollup-a-rpc-port", 17545, "Rollup A op-rbuilder RPC port"},
		{"flashblocks-rollup-b-rpc-port", "l2.flashblocks.rollup-b-rpc-port", 27545, "Rollup B op-rbuilder RPC port"},

		// Sidecar
		{"sidecar-rollup-a-api-port", "l2.sidecar.rollup-a-api-port", 17090, "Rollup A sidecar API port"},
		{"sidecar-rollup-b-api-port", "l2.sidecar.rollup-b-api-port", 27090, "Rollup B sidecar API port"},

		// Bundler
		{"bundler-rollup-a-api-port", "l2.bundler.rollup-a-api-port", 17082, "Rollup A ethera-bundler API port"},
		{"bundler-rollup-b-api-port", "l2.bundler.rollup-b-api-port", 27082, "Rollup B ethera-bundler API port"},

		// Frontend (Ethera Labs Console)
		{"frontend-port", "l2.frontend.port", 3000, "Ethera Labs Console port"},

		// Dispute config
		{"dispute-proof-maturity-delay-seconds", "l2.dispute.proof-maturity-delay-seconds", 604800, "Proof maturity delay in seconds (default: 7 days)"},
		{"dispute-game-finality-delay-seconds", "l2.dispute.dispute-game-finality-delay-seconds", 302400, "Dispute game finality delay in seconds (default: 3.5 days)"},
	}

	boolFlags = []flagDef[bool]{
		{"blockscout-enabled", "l2.blockscout.enabled", false, "Enable Blockscout block explorer"},
		{"flashblocks-enabled", "l2.flashblocks.enabled", false, "Enable flashblocks support (op-rbuilder and rollup-boost)"},
		{"sidecar-enabled", "l2.sidecar.enabled", false, "Enable sidecar for cross-chain coordination (requires flashblocks)"},
		{"bundler-enabled", "l2.bundler.enabled", false, "Enable the ERC-4337 v0.7 ethera-bundler service (requires flashblocks)"},
		{"frontend-enabled", "l2.frontend.enabled", false, "Enable Ethera Labs Console (requires flashblocks and sidecar)"},
		{"frontend-dev-enabled", "l2.frontend.dev-enabled", false, "Run the Ethera Labs Console with Vite hot-reload, mounting frontend/ source into the container (requires flashblocks and sidecar)"},
		{"alt-da-enabled", "l2.alt-da.enabled", false, "Enable AltDA mode: post transaction batches to a local DA server instead of L1"},
		{"op-succinct-enabled", "l2.op-succinct.enabled", false, "Enable op-succinct mock-mode validity services in localnet"},
	}
)

func init() {
	if err := declareFlags(stringFlags); err != nil {
		panic(err)
	}
	if err := declareFlags(intFlags); err != nil {
		panic(err)
	}
	if err := declareFlags(boolFlags); err != nil {
		panic(err)
	}
	CMD.AddCommand(compileCmd)
	CMD.AddCommand(deployCmd)
	CMD.AddCommand(frontendEnvCmd)
}

// declareFlags declares multiple flags and binds them to viper configuration keys.
func declareFlags[T flagType](flags []flagDef[T]) error {
	for _, flag := range flags {
		if err := declareFlag(flag.name, flag.viperKey, flag.defaultValue, flag.description); err != nil {
			return err
		}
	}
	return nil
}

// declareFlag declares a single flag and binds it to a viper configuration key.
// The type parameter T determines the flag type (string, int, or bool).
func declareFlag[T flagType](flagName, viperKey string, defaultValue T, description string) error {
	var zero T
	switch any(zero).(type) {
	case string:
		CMD.Flags().String(flagName, any(defaultValue).(string), description)
	case int:
		CMD.Flags().Int(flagName, any(defaultValue).(int), description)
	case bool:
		CMD.Flags().Bool(flagName, any(defaultValue).(bool), description)
	}
	return viper.BindPFlag(viperKey, CMD.Flags().Lookup(flagName))
}
