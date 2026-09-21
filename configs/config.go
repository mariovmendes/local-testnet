package configs

import (
	"errors"
	"fmt"
	"strings"
)

var Values Config

type (
	RepositoryName string
	L2ChainName    string
	ImageName      string

	Config struct {
		L1            L1            `mapstructure:"l1"`
		L2            L2            `mapstructure:"l2"`
		Observability Observability `mapstructure:"observability"`
	}

	L1 struct {
	}

	L2 struct {
		L1ChainID             int                           `mapstructure:"l1-chain-id"`
		L1ElURL               string                        `mapstructure:"l1-el-url"`
		L1ClURL               string                        `mapstructure:"l1-cl-url"`
		EtheraNetworkName     string                        `mapstructure:"ethera-labs-name"`
		Wallet                Wallet                        `mapstructure:"wallet"`
		CoordinatorPrivateKey string                        `mapstructure:"coordinator-private-key"`
		Repositories          map[RepositoryName]Repository `mapstructure:"repositories"`
		ChainConfigs          map[L2ChainName]Chain         `mapstructure:"chain-configs"`
		Images                map[ImageName]Image           `mapstructure:"images"`
		DeploymentTarget      string                        `mapstructure:"deployment-target"`
		GenesisBalanceWei     string                        `mapstructure:"genesis-balance-wei"`
		Dispute               DisputeConfig                 `mapstructure:"dispute"`
		Blockscout            BlockscoutConfig              `mapstructure:"blockscout"`
		Flashblocks           FlashblocksConfig             `mapstructure:"flashblocks"`
		Sidecar               SidecarConfig                 `mapstructure:"sidecar"`
		Bundler               BundlerConfig                 `mapstructure:"bundler"`
		Frontend              FrontendConfig                `mapstructure:"frontend"`
		AltDA                 AltDAConfig                   `mapstructure:"alt-da"`
		OPSuccinct            OPSuccinctConfig              `mapstructure:"op-succinct"`
		// ValidatorEL selects the rollup-boost validator execution client (L2_URL):
		// "op-reth" (default) or "op-besu". op-besu requires flashblocks and a local
		// local/op-besu:dev image (see op-besu/Dockerfile.local); it runs the full Isthmus
		// schedule (Prague EVM, final EIP-7685, EIP-2935), matching stage.
		ValidatorEL string `mapstructure:"validator-el"`
	}

	// OPSuccinctConfig controls whether localnet runs the op-succinct mock-mode
	// validity services for the configured rollups.
	OPSuccinctConfig struct {
		Enabled bool `mapstructure:"enabled"`
	}

	FrontendConfig struct {
		Enabled bool `mapstructure:"enabled"`
		Port    int  `mapstructure:"port"`
		// DevEnabled mounts frontend/ source into the container and runs the
		// Vite dev server so edits hot-reload without rebuilding the image.
		DevEnabled bool `mapstructure:"dev-enabled"`
	}

	BlockscoutConfig struct {
		Enabled bool `mapstructure:"enabled"`
	}

	FlashblocksConfig struct {
		Enabled                bool   `mapstructure:"enabled"`
		OpRbuilderImageTag     string `mapstructure:"op-rbuilder-image-tag"`
		RollupBoostImageTag    string `mapstructure:"rollup-boost-image-tag"`
		RollupARPCPort         int    `mapstructure:"rollup-a-rpc-port"`
		RollupBRPCPort         int    `mapstructure:"rollup-b-rpc-port"`
		RollupAP2PSecretKeyHex string `mapstructure:"rollup-a-p2p-secret-key-hex"`
		RollupBP2PSecretKeyHex string `mapstructure:"rollup-b-p2p-secret-key-hex"`
	}

	SidecarConfig struct {
		Enabled        bool `mapstructure:"enabled"`
		RollupAAPIPort int  `mapstructure:"rollup-a-api-port"`
		RollupBAPIPort int  `mapstructure:"rollup-b-api-port"`
	}

	// BundlerConfig controls the ERC-4337 v0.7 bundler service. When enabled,
	// localnet starts one bundler container per rollup, each signing handleOps
	// transactions with `l2.wallet.private-key` and pointed at the matching
	// op-rbuilder RPC.
	BundlerConfig struct {
		Enabled        bool `mapstructure:"enabled"`
		RollupAAPIPort int  `mapstructure:"rollup-a-api-port"`
		RollupBAPIPort int  `mapstructure:"rollup-b-api-port"`
	}

	// AltDAConfig controls Alternative Data Availability (AltDA) mode for the OP Stack.
	// When enabled, op-batcher publishes batches to the configured DA service instead of L1,
	// and op-node derives from that service. In localnet this is modeled with one DA server
	// per rollup so the runtime stays close to long-lived environment topology.
	AltDAConfig struct {
		Enabled          bool   `mapstructure:"enabled"`
		DACommitmentType string `mapstructure:"da-commitment-type"`

		// When true, op-deployer reuses existing AltDA challenge contracts instead of
		// deploying new ones. Use this only for long-lived environments where those
		// addresses are already provisioned and managed outside localnet.
		SkipL1Deploy          bool   `mapstructure:"skip-l1-deploy"`
		ChallengeProxyAddress string `mapstructure:"challenge-proxy-address"`
		ChallengeImplAddress  string `mapstructure:"challenge-impl-address"`

		// Challenge contract parameters used only when localnet owns AltDA contract deployment.
		// Zero values keep the deployer defaults: DAChallengeWindow=100, DAResolveWindow=100,
		// and DABondSize=1.
		DAChallengeWindow          uint64 `mapstructure:"da-challenge-window"`
		DAResolveWindow            uint64 `mapstructure:"da-resolve-window"`
		DABondSize                 uint64 `mapstructure:"da-bond-size"`
		DAResolverRefundPercentage uint64 `mapstructure:"da-resolver-refund-percentage"`
	}

	DisputeConfig struct {
		NetworkName                     string `mapstructure:"network-name"`
		ExplorerURL                     string `mapstructure:"explorer-url"`
		ExplorerAPIURL                  string `mapstructure:"explorer-api-url"`
		VerifierAddress                 string `mapstructure:"verifier-address"`
		OwnerAddress                    string `mapstructure:"owner-address"`
		ProposerAddress                 string `mapstructure:"proposer-address"`
		AggregationVkey                 string `mapstructure:"aggregation-vkey"`
		GuardianAddress                 string `mapstructure:"guardian-address"`
		ProofMaturityDelaySeconds       int    `mapstructure:"proof-maturity-delay-seconds"`
		DisputeGameFinalityDelaySeconds int    `mapstructure:"dispute-game-finality-delay-seconds"`
		DisputeGameInitBond             string `mapstructure:"dispute-game-init-bond"`
	}

	Chain struct {
		ID      int `mapstructure:"id"`
		RPCPort int `mapstructure:"rpc-port"`
	}

	Repository struct {
		URL       string `mapstructure:"url"`
		Branch    string `mapstructure:"branch"`
		LocalPath string `mapstructure:"local-path"`
	}

	Image struct {
		Tag string `mapstructure:"tag"`
	}

	Wallet struct {
		PrivateKey string `mapstructure:"private-key"`
		Address    string `mapstructure:"address"`
	}

	Observability struct {
	}
)

const (
	// Validator execution client choices for l2.validator-el (rollup-boost L2_URL).
	ValidatorELOpReth = "op-reth"
	ValidatorELOpBesu = "op-besu"

	RepositoryNameOpRbuilder         RepositoryName = "op-rbuilder"
	RepositoryNamePublisher          RepositoryName = "publisher"
	RepositoryNameSidecar            RepositoryName = "sidecar"
	RepositoryNameEtheraContracts    RepositoryName = "ethera-contracts"
	RepositoryNameAccountAbstraction RepositoryName = "account-abstraction"
	RepositoryNameBundler            RepositoryName = "bundler"
	RepositoryNameOPSuccinct         RepositoryName = "op-succinct"

	ImageNameOpDeployer ImageName = "op-deployer"
	ImageNameOpNode     ImageName = "op-node"
	ImageNameOpProposer ImageName = "op-proposer"
	ImageNameOpBatcher  ImageName = "op-batcher"
	ImageNameOpReth     ImageName = "op-reth"

	oplabsImageRegistry = "us-docker.pkg.dev/oplabs-tools-artifacts/images"

	L2ChainNameRollupA L2ChainName = "rollup-a"
	L2ChainNameRollupB L2ChainName = "rollup-b"

	AltDACommitmentTypeKeccak  = "KeccakCommitment"
	AltDACommitmentTypeGeneric = "GenericCommitment"

	altDADefaultChallengeWindow = 100
	altDADefaultResolveWindow   = 100
	altDADefaultBondSize        = 1
)

// ImageRef returns the fully-qualified oplabs-tools-artifacts reference for
// the named image.
func (c *L2) ImageRef(name ImageName) string {
	return fmt.Sprintf("%s/%s:%s", oplabsImageRegistry, name, c.Images[name].Tag)
}

// CommitmentType returns the configured DA commitment type, defaulting to KeccakCommitment.
func (c AltDAConfig) CommitmentType() string {
	if c.DACommitmentType != "" {
		return c.DACommitmentType
	}
	return AltDACommitmentTypeKeccak
}

// ChallengeWindow returns the challenge window, applying the default when zero.
func (c AltDAConfig) ChallengeWindow() uint64 {
	if c.DAChallengeWindow > 0 {
		return c.DAChallengeWindow
	}
	return altDADefaultChallengeWindow
}

// ResolveWindow returns the resolve window, applying the default when zero.
func (c AltDAConfig) ResolveWindow() uint64 {
	if c.DAResolveWindow > 0 {
		return c.DAResolveWindow
	}
	return altDADefaultResolveWindow
}

// Active reports whether the Ethera Labs Console should be started in any
// mode. Either flag alone is sufficient; dev mode does not require the
// production flag to also be set.
func (c FrontendConfig) Active() bool {
	return c.Enabled || c.DevEnabled
}

// BondSize returns the challenge bond size, applying the default when zero.
func (c AltDAConfig) BondSize() uint64 {
	if c.DABondSize > 0 {
		return c.DABondSize
	}
	return altDADefaultBondSize
}

func (c *L2) Validate() error {
	var errs []error

	if c.L1ChainID == 0 {
		errs = append(errs, errors.New("l2.l1-chain-id is required"))
	}
	if c.L1ElURL == "" {
		errs = append(errs, errors.New("l2.l1-el-url is required"))
	}
	if c.L1ClURL == "" {
		errs = append(errs, errors.New("l2.l1-cl-url is required"))
	}
	if c.CoordinatorPrivateKey == "" {
		errs = append(errs, errors.New("l2.coordinator-private-key is required"))
	}
	if c.Wallet.PrivateKey == "" {
		errs = append(errs, errors.New("l2.wallet.private-key is required"))
	}
	if c.Wallet.Address == "" {
		errs = append(errs, errors.New("l2.wallet.address is required"))
	}
	if normalizedCoordinatorKey, normalizedWalletKey := normalizePrivateKey(c.CoordinatorPrivateKey), normalizePrivateKey(c.Wallet.PrivateKey); normalizedCoordinatorKey != "" && normalizedCoordinatorKey == normalizedWalletKey {
		errs = append(errs, errors.New("l2.coordinator-private-key must differ from l2.wallet.private-key to avoid nonce collisions"))
	}

	requiredRepos := []RepositoryName{
		RepositoryNamePublisher,
		RepositoryNameEtheraContracts,
	}
	if c.Flashblocks.Enabled {
		requiredRepos = append(requiredRepos, RepositoryNameOpRbuilder)
	}
	if c.Sidecar.Enabled {
		requiredRepos = append(requiredRepos, RepositoryNameSidecar)
	}
	if c.Bundler.Enabled {
		if !c.Flashblocks.Enabled {
			errs = append(errs, errors.New("l2.bundler.enabled requires l2.flashblocks.enabled"))
		}
		requiredRepos = append(requiredRepos,
			RepositoryNameBundler,
			RepositoryNameAccountAbstraction,
		)
	}

	for _, name := range requiredRepos {
		if err := validateRepositoryConfig(c.Repositories, name, true); err != nil {
			errs = append(errs, err)
		}
	}

	if c.OPSuccinct.Enabled {
		if err := validateRepositoryConfig(c.Repositories, RepositoryNameOPSuccinct, true); err != nil {
			errs = append(errs, err)
		}
	}

	requiredImages := []ImageName{ImageNameOpDeployer, ImageNameOpNode, ImageNameOpProposer, ImageNameOpBatcher, ImageNameOpReth}
	for _, name := range requiredImages {
		img, exists := c.Images[name]
		if !exists {
			errs = append(errs, fmt.Errorf("l2.images.%s is required", name))
			continue
		}
		if img.Tag == "" {
			errs = append(errs, fmt.Errorf("l2.images.%s.tag is required", name))
		}
	}

	rollupA, hasRollupA := c.ChainConfigs[L2ChainNameRollupA]
	rollupB, hasRollupB := c.ChainConfigs[L2ChainNameRollupB]

	if !hasRollupA {
		errs = append(errs, errors.New("l2.chain-configs.rollup-a is required"))
	} else {
		if rollupA.ID == 0 {
			errs = append(errs, errors.New("l2.chain-configs.rollup-a.id is required"))
		}
		if rollupA.RPCPort == 0 {
			errs = append(errs, errors.New("l2.chain-configs.rollup-a.rpc-port is required"))
		}
	}

	if !hasRollupB {
		errs = append(errs, errors.New("l2.chain-configs.rollup-b is required"))
	} else {
		if rollupB.ID == 0 {
			errs = append(errs, errors.New("l2.chain-configs.rollup-b.id is required"))
		}
		if rollupB.RPCPort == 0 {
			errs = append(errs, errors.New("l2.chain-configs.rollup-b.rpc-port is required"))
		}
	}

	if c.DeploymentTarget == "" {
		errs = append(errs, errors.New("l2.deployment-target is required"))
	} else if c.DeploymentTarget != "live" && c.DeploymentTarget != "calldata" {
		errs = append(errs, errors.New("l2.deployment-target must be either 'live' or 'calldata'"))
	}

	if c.AltDA.Enabled {
		ct := c.AltDA.CommitmentType()
		if ct != AltDACommitmentTypeKeccak && ct != AltDACommitmentTypeGeneric {
			errs = append(errs, fmt.Errorf("l2.alt-da.da-commitment-type must be %q or %q (got %q)", AltDACommitmentTypeKeccak, AltDACommitmentTypeGeneric, ct))
		}
		if c.AltDA.SkipL1Deploy {
			if c.AltDA.ChallengeProxyAddress == "" {
				errs = append(errs, errors.New("l2.alt-da.challenge-proxy-address is required when skip-l1-deploy is true"))
			}
			if c.AltDA.ChallengeImplAddress == "" {
				errs = append(errs, errors.New("l2.alt-da.challenge-impl-address is required when skip-l1-deploy is true"))
			}
		}
		if c.AltDA.DAResolverRefundPercentage > 100 {
			errs = append(errs, errors.New("l2.alt-da.da-resolver-refund-percentage must be between 0 and 100"))
		}
	}

	// Validate dispute config
	if c.Dispute.NetworkName == "" {
		errs = append(errs, errors.New("l2.dispute.network-name is required"))
	}
	if c.Dispute.VerifierAddress == "" {
		errs = append(errs, errors.New("l2.dispute.verifier-address is required"))
	}
	if c.Dispute.OwnerAddress == "" {
		errs = append(errs, errors.New("l2.dispute.owner-address is required"))
	}
	if c.Dispute.ProposerAddress == "" {
		errs = append(errs, errors.New("l2.dispute.proposer-address is required"))
	}
	if c.Dispute.AggregationVkey == "" {
		errs = append(errs, errors.New("l2.dispute.aggregation-vkey is required"))
	}
	if c.Dispute.GuardianAddress == "" {
		errs = append(errs, errors.New("l2.dispute.guardian-address is required"))
	}
	if c.Dispute.ProofMaturityDelaySeconds <= 0 {
		errs = append(errs, errors.New("l2.dispute.proof-maturity-delay-seconds must be positive"))
	}
	if c.Dispute.DisputeGameFinalityDelaySeconds <= 0 {
		errs = append(errs, errors.New("l2.dispute.dispute-game-finality-delay-seconds must be positive"))
	}
	if c.Dispute.DisputeGameInitBond == "" {
		errs = append(errs, errors.New("l2.dispute.dispute-game-init-bond is required"))
	}

	if c.EtheraNetworkName == "" {
		errs = append(errs, errors.New("l2.ethera-labs-name is required"))
	}
	if c.Sidecar.Enabled && !c.Flashblocks.Enabled {
		errs = append(errs, errors.New("l2.sidecar.enabled requires l2.flashblocks.enabled"))
	}
	if c.Frontend.Active() && (!c.Flashblocks.Enabled || !c.Sidecar.Enabled) {
		errs = append(errs, errors.New("l2.frontend requires l2.flashblocks.enabled and l2.sidecar.enabled"))
	}
	switch c.ValidatorEL {
	case "", ValidatorELOpReth:
		// op-reth is the default; no extra requirements.
	case ValidatorELOpBesu:
		if !c.Flashblocks.Enabled {
			errs = append(errs, errors.New("l2.validator-el=op-besu requires l2.flashblocks.enabled (op-besu is the rollup-boost L2_URL validator)"))
		}
	default:
		errs = append(errs, fmt.Errorf("l2.validator-el must be %q or %q, got %q", ValidatorELOpReth, ValidatorELOpBesu, c.ValidatorEL))
	}

	if len(errs) > 0 {
		return fmt.Errorf("L2 configuration validation failed: %w", errors.Join(errs...))
	}

	return nil
}

func validateRepositoryConfig(repos map[RepositoryName]Repository, name RepositoryName, required bool) error {
	repo, exists := repos[name]
	if !exists {
		if required {
			return fmt.Errorf("l2.repositories.%s is required", name)
		}
		return nil
	}

	hasLocal := repo.LocalPath != ""
	hasRemote := repo.URL != "" && repo.Branch != ""
	if !hasLocal && !hasRemote {
		return fmt.Errorf("l2.repositories.%s must set either local-path or url+branch", name)
	}
	if hasLocal && hasRemote {
		return fmt.Errorf("l2.repositories.%s cannot set both local-path and url+branch (choose one)", name)
	}
	return nil
}

func normalizePrivateKey(key string) string {
	trimmed := strings.TrimSpace(key)
	trimmed = strings.ToLower(trimmed)
	return strings.TrimPrefix(trimmed, "0x")
}
