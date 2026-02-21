// Package zerog provides shared utilities for interacting with 0G Chain
// via go-ethereum.
package zerog

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ChainBackend combines the go-ethereum interfaces needed for on-chain
// contract interaction and transaction receipt retrieval.
type ChainBackend interface {
	bind.ContractBackend
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
}

// DialClient connects to an Ethereum-compatible JSON-RPC endpoint.
func DialClient(ctx context.Context, rpcURL string) (*ethclient.Client, error) {
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("zerog: dial %s: %w", rpcURL, err)
	}
	return client, nil
}

// LoadKey parses a hex-encoded ECDSA private key.
func LoadKey(hexKey string) (*ecdsa.PrivateKey, error) {
	hexKey = strings.TrimPrefix(hexKey, "0x")
	key, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		return nil, fmt.Errorf("zerog: invalid private key: %w", err)
	}
	return key, nil
}

// MakeTransactOpts creates signed transaction options for on-chain calls.
func MakeTransactOpts(ctx context.Context, key *ecdsa.PrivateKey, chainID int64) (*bind.TransactOpts, error) {
	opts, err := bind.NewKeyedTransactorWithChainID(key, big.NewInt(chainID))
	if err != nil {
		return nil, fmt.Errorf("zerog: create transactor: %w", err)
	}
	opts.Context = ctx
	return opts, nil
}

// AddressFromKey derives the Ethereum address from a private key.
func AddressFromKey(key *ecdsa.PrivateKey) common.Address {
	return crypto.PubkeyToAddress(key.PublicKey)
}
