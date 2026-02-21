// Package inft integrates with ERC-7857 iNFT minting on 0G Chain
// for provenance tracking of inference results.
//
// Uses go-ethereum to interact with the 0G Chain (EVM-compatible).
// 0G Galileo Testnet: Chain ID 16602, RPC: https://evmrpc-testnet.0g.ai
package inft

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog"
)

const contractABIJSON = `[
  {
    "name": "mint",
    "type": "function",
    "inputs": [
      {"name": "to", "type": "address"},
      {"name": "name", "type": "string"},
      {"name": "description", "type": "string"},
      {"name": "encryptedMeta", "type": "bytes"},
      {"name": "resultHash", "type": "bytes32"},
      {"name": "storageRef", "type": "string"}
    ],
    "outputs": [
      {"name": "tokenId", "type": "uint256"}
    ]
  },
  {
    "name": "updateEncryptedMetadata",
    "type": "function",
    "inputs": [
      {"name": "tokenId", "type": "uint256"},
      {"name": "encryptedMeta", "type": "bytes"}
    ],
    "outputs": []
  },
  {
    "name": "ownerOf",
    "type": "function",
    "stateMutability": "view",
    "inputs": [
      {"name": "tokenId", "type": "uint256"}
    ],
    "outputs": [
      {"name": "owner", "type": "address"}
    ]
  },
  {
    "name": "Transfer",
    "type": "event",
    "inputs": [
      {"name": "from", "type": "address", "indexed": true},
      {"name": "to", "type": "address", "indexed": true},
      {"name": "tokenId", "type": "uint256", "indexed": true}
    ]
  }
]`

var contractABI = mustParseABI(contractABIJSON)

func mustParseABI(raw string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic("inft: invalid ABI: " + err.Error())
	}
	return parsed
}

// INFTMinter creates ERC-7857 iNFTs with encrypted metadata on 0G Chain.
type INFTMinter interface {
	Mint(ctx context.Context, req MintRequest) (string, error)
	UpdateMetadata(ctx context.Context, tokenID string, meta EncryptedMeta) error
	GetStatus(ctx context.Context, tokenID string) (*INFTStatus, error)
}

type minter struct {
	cfg      MinterConfig
	backend  zerog.ChainBackend
	contract *bind.BoundContract
	key      *ecdsa.PrivateKey
	addr     common.Address
}

// NewMinter creates a new INFTMinter using go-ethereum to interact with 0G Chain.
func NewMinter(cfg MinterConfig, backend zerog.ChainBackend, key *ecdsa.PrivateKey) INFTMinter {
	contractAddr := common.HexToAddress(cfg.ContractAddress)
	bc := bind.NewBoundContract(contractAddr, contractABI, backend, backend, backend)

	return &minter{
		cfg:      cfg,
		backend:  backend,
		contract: bc,
		key:      key,
		addr:     crypto.PubkeyToAddress(key.PublicKey),
	}
}

func (m *minter) Mint(ctx context.Context, req MintRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("inft: context cancelled before mint: %w", err)
	}

	encrypted, err := encryptMetadata(m.cfg.EncryptionKey, m.cfg.EncryptionKeyID, req.PlaintextMeta)
	if err != nil {
		return "", fmt.Errorf("inft: encrypt metadata for job %s: %w", req.InferenceJobID, err)
	}

	encBytes, err := json.Marshal(encrypted)
	if err != nil {
		return "", fmt.Errorf("inft: marshal encrypted metadata: %w", err)
	}

	var resultHash [32]byte
	copy(resultHash[:], []byte(req.ResultHash))

	opts, err := zerog.MakeTransactOpts(ctx, m.key, m.cfg.ChainID)
	if err != nil {
		return "", fmt.Errorf("inft: create transact opts: %w", err)
	}

	tx, err := m.contract.Transact(opts, "mint",
		m.addr, req.Name, req.Description, encBytes, resultHash, req.StorageContentID)
	if err != nil {
		return "", fmt.Errorf("inft: mint tx for job %s: %w", req.InferenceJobID, err)
	}

	receipt, err := bind.WaitMined(ctx, m.backend, tx)
	if err != nil {
		return "", fmt.Errorf("inft: wait for mint tx %s: %w", tx.Hash().Hex(), err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return "", fmt.Errorf("inft: mint tx reverted for job %s: %w", req.InferenceJobID, ErrMintFailed)
	}

	tokenID, err := parseTransferEvent(receipt)
	if err != nil {
		return "", fmt.Errorf("inft: parse mint event for job %s: %w", req.InferenceJobID, err)
	}

	return tokenID.String(), nil
}

func (m *minter) UpdateMetadata(ctx context.Context, tokenID string, meta EncryptedMeta) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("inft: context cancelled before update: %w", err)
	}

	id, ok := new(big.Int).SetString(tokenID, 10)
	if !ok {
		return fmt.Errorf("inft: invalid token ID %q", tokenID)
	}

	encBytes, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("inft: marshal encrypted metadata: %w", err)
	}

	opts, err := zerog.MakeTransactOpts(ctx, m.key, m.cfg.ChainID)
	if err != nil {
		return fmt.Errorf("inft: create transact opts: %w", err)
	}

	tx, err := m.contract.Transact(opts, "updateEncryptedMetadata", id, encBytes)
	if err != nil {
		return fmt.Errorf("inft: update tx for token %s: %w", tokenID, err)
	}

	receipt, err := bind.WaitMined(ctx, m.backend, tx)
	if err != nil {
		return fmt.Errorf("inft: wait for update tx %s: %w", tx.Hash().Hex(), err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("inft: update tx reverted for token %s: %w", tokenID, ErrMintFailed)
	}

	return nil
}

func (m *minter) GetStatus(ctx context.Context, tokenID string) (*INFTStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("inft: context cancelled: %w", err)
	}

	id, ok := new(big.Int).SetString(tokenID, 10)
	if !ok {
		return nil, fmt.Errorf("inft: invalid token ID %q", tokenID)
	}

	var results []interface{}
	err := m.contract.Call(&bind.CallOpts{Context: ctx}, &results, "ownerOf", id)
	if err != nil {
		return nil, fmt.Errorf("inft: token %s: %w", tokenID, ErrTokenNotFound)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("inft: token %s: %w", tokenID, ErrTokenNotFound)
	}

	owner, ok := results[0].(common.Address)
	if !ok || owner == (common.Address{}) {
		return nil, fmt.Errorf("inft: token %s: %w", tokenID, ErrTokenNotFound)
	}

	return &INFTStatus{
		TokenID:         tokenID,
		Owner:           owner.Hex(),
		ChainID:         m.cfg.ChainID,
		ContractAddress: m.cfg.ContractAddress,
	}, nil
}

// parseTransferEvent extracts the tokenID from the Transfer(address,address,uint256) event.
func parseTransferEvent(receipt *types.Receipt) (*big.Int, error) {
	transferSig := contractABI.Events["Transfer"].ID
	for _, log := range receipt.Logs {
		if len(log.Topics) >= 4 && log.Topics[0] == transferSig {
			tokenID := new(big.Int).SetBytes(log.Topics[3].Bytes())
			return tokenID, nil
		}
	}
	return nil, fmt.Errorf("inft: Transfer event not found in receipt")
}
