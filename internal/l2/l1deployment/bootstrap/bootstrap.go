// Package bootstrap prepares the Kurtosis L1 devnet for op-deployer: it
// deploys the CREATE2 deterministic-deployment proxy that op-deployer
// requires and seeds the deployer wallet with L1 gas funds. Neither the
// devnet genesis nor op-deployer provide these, so they must be bootstrapped
// at runtime. Everything here talks to the L1 RPC directly over go-ethereum's
// ethclient — no external binaries (cast/forge) or extra containers involved.
package bootstrap

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/ethera-labs/local-testnet/internal/logger"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	// kurtosisFunderPrivateKey is account 0 of the ethereum-package/ssv-mini
	// default mnemonic ("giant issue aisle ..."), which Kurtosis prefunds
	// with a large L1 balance in genesis. Used to seed both the deployer
	// wallet and the CREATE2 proxy sender below.
	kurtosisFunderPrivateKey = "bcdf20249abf0ed6d944c0288fad489e33f66b3960d9e6229c1cd214ed3bbe31"

	// proxySenderAddress is the sender of the canonical pre-signed CREATE2
	// factory deployment tx below (nonce 0 -> deterministicDeployerAddress).
	proxySenderAddress = "0x3fAB184622Dc19b6109349B94811493BF2a45362"
	// deterministicDeployerAddress is the well-known CREATE2 factory address
	// ("Nick Johnson's" proxy) that op-deployer requires to already exist.
	deterministicDeployerAddress = "0x4e59b44847b379578588920ca78fbf26c0b4956c"
	// proxyRawTxHex is the pre-signed raw transaction (v=27, r=s=0x22..22)
	// that deploys the CREATE2 factory at deterministicDeployerAddress when
	// broadcast from a funded proxySenderAddress.
	proxyRawTxHex = "f8a58085174876e800830186a08080b853604580600e600039806000f350fe7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe03601600081602082378035828234f58015156039578182fd5b8082525050506014600cf31ba02222222222222222222222222222222222222222222222222222222222222222a02222222222222222222222222222222222222222222222222222222222222222"
)

var (
	proxySenderFundingWei = big.NewInt(1e17)                                     // 0.1 ETH
	minDeployerBalanceWei = new(big.Int).Mul(big.NewInt(10), big.NewInt(1e18))   // 10 ETH
	deployerFundingWei    = new(big.Int).Mul(big.NewInt(1000), big.NewInt(1e18)) // 1000 ETH
)

// EnsureL1Ready funds the deployer wallet (if below minDeployerBalanceWei)
// and deploys the CREATE2 deterministic-deployment proxy (if missing), both
// against the L1 chain at rpcURL. Call this before running op-deployer apply.
func EnsureL1Ready(ctx context.Context, rpcURL, deployerAddress string) error {
	log := logger.Named("l1_bootstrap")

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return fmt.Errorf("failed to connect to L1 RPC: %w", err)
	}
	defer client.Close()

	if err := ensureFunded(ctx, client, log, common.HexToAddress(deployerAddress), minDeployerBalanceWei, deployerFundingWei); err != nil {
		return fmt.Errorf("failed to fund deployer wallet: %w", err)
	}

	if err := ensureDeterministicDeployer(ctx, client, log); err != nil {
		return fmt.Errorf("failed to deploy deterministic deployment proxy: %w", err)
	}

	return nil
}

// ensureDeterministicDeployer deploys the CREATE2 factory op-deployer
// requires, funding its canonical sender first if needed.
func ensureDeterministicDeployer(ctx context.Context, client *ethclient.Client, log *slog.Logger) error {
	deployerAddr := common.HexToAddress(deterministicDeployerAddress)
	code, err := client.CodeAt(ctx, deployerAddr, nil)
	if err != nil {
		return fmt.Errorf("failed to check deterministic deployer code: %w", err)
	}
	if len(code) > 0 {
		log.Info("deterministic deployment proxy already present, skipping")
		return nil
	}

	senderAddr := common.HexToAddress(proxySenderAddress)
	if err := ensureFunded(ctx, client, log, senderAddr, big.NewInt(0), proxySenderFundingWei); err != nil {
		return fmt.Errorf("failed to fund proxy sender: %w", err)
	}

	log.Info("broadcasting deterministic deployment proxy tx", "address", deterministicDeployerAddress)
	rawTxBytes, err := hex.DecodeString(strings.TrimPrefix(proxyRawTxHex, "0x"))
	if err != nil {
		return fmt.Errorf("failed to decode proxy raw tx: %w", err)
	}
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(rawTxBytes); err != nil {
		return fmt.Errorf("failed to decode proxy raw tx: %w", err)
	}

	if err := client.SendTransaction(ctx, tx); err != nil {
		return fmt.Errorf("failed to broadcast proxy deployment tx: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if _, err := bind.WaitMined(waitCtx, client, tx); err != nil {
		return fmt.Errorf("failed waiting for proxy deployment tx: %w", err)
	}

	log.Info("deterministic deployment proxy deployed", "address", deterministicDeployerAddress)
	return nil
}

// ensureFunded tops up address to fundingWei from the Kurtosis-prefunded
// account if its current balance is at or below minWei.
func ensureFunded(ctx context.Context, client *ethclient.Client, log *slog.Logger, address common.Address, minWei, fundingWei *big.Int) error {
	balance, err := client.BalanceAt(ctx, address, nil)
	if err != nil {
		return fmt.Errorf("failed to check balance for %s: %w", address.Hex(), err)
	}
	if balance.Cmp(minWei) > 0 {
		return nil
	}

	log.Info("funding L1 account", "address", address.Hex(), "amount_wei", fundingWei.String())

	funderKey, err := crypto.HexToECDSA(kurtosisFunderPrivateKey)
	if err != nil {
		return fmt.Errorf("failed to parse funder private key: %w", err)
	}
	funderAddr := crypto.PubkeyToAddress(funderKey.PublicKey)

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch chain ID: %w", err)
	}
	nonce, err := client.PendingNonceAt(ctx, funderAddr)
	if err != nil {
		return fmt.Errorf("failed to fetch funder nonce: %w", err)
	}
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch gas price: %w", err)
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &address,
		Value:    fundingWei,
		Gas:      21_000,
		GasPrice: gasPrice,
	})

	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), funderKey)
	if err != nil {
		return fmt.Errorf("failed to sign funding tx: %w", err)
	}

	if err := client.SendTransaction(ctx, signedTx); err != nil {
		return fmt.Errorf("failed to broadcast funding tx: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if _, err := bind.WaitMined(waitCtx, client, signedTx); err != nil {
		return fmt.Errorf("failed waiting for funding tx: %w", err)
	}

	log.Info("L1 account funded", "address", address.Hex())
	return nil
}
