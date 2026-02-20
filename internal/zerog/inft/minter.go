// Package inft integrates with ERC-7857 iNFT minting on 0G Chain
// for provenance tracking of inference results.
//
// ERC-7857 defines "intelligent NFTs" — NFTs with encrypted, updatable metadata
// that can represent AI-generated artifacts. The encrypted metadata ensures
// privacy while the on-chain token provides verifiable provenance.
//
// This package uses JSON-RPC to interact with the 0G Chain (EVM-compatible).
// 0G Galileo Testnet: Chain ID 16602, RPC: https://evmrpc-testnet.0g.ai
//
// For the hackathon, we use a simplified contract interaction via eth_sendTransaction
// and eth_call. Production would use go-ethereum's abigen-generated bindings.
package inft

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// INFTMinter creates ERC-7857 iNFTs with encrypted metadata on 0G Chain.
type INFTMinter interface {
	// Mint creates a new iNFT with the given encrypted metadata.
	// Returns the token ID of the minted NFT.
	Mint(ctx context.Context, req MintRequest) (string, error)

	// UpdateMetadata updates the encrypted metadata of an existing iNFT.
	UpdateMetadata(ctx context.Context, tokenID string, meta EncryptedMeta) error

	// GetStatus returns the current status of a minted iNFT.
	GetStatus(ctx context.Context, tokenID string) (*INFTStatus, error)
}

// minter implements INFTMinter using JSON-RPC calls to 0G Chain.
type minter struct {
	cfg    MinterConfig
	client *http.Client
}

// NewMinter creates a new INFTMinter connected to 0G Chain.
func NewMinter(cfg MinterConfig) INFTMinter {
	return &minter{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Mint encrypts metadata, builds a mint transaction, and submits it to 0G Chain.
func (m *minter) Mint(ctx context.Context, req MintRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("inft: context cancelled before mint: %w", err)
	}

	encrypted, err := encryptMetadata(m.cfg.EncryptionKey, m.cfg.EncryptionKeyID, req.PlaintextMeta)
	if err != nil {
		return "", fmt.Errorf("inft: failed to encrypt metadata for job %s: %w", req.InferenceJobID, err)
	}

	tx := mintTransaction{
		Name:           req.Name,
		Description:    req.Description,
		EncryptedMeta:  *encrypted,
		ResultHash:     req.ResultHash,
		StorageRef:     req.StorageContentID,
		InferenceJobID: req.InferenceJobID,
	}

	txData, err := json.Marshal(tx)
	if err != nil {
		return "", fmt.Errorf("inft: failed to marshal mint tx: %w", err)
	}

	txHash, err := m.sendTransaction(ctx, txData)
	if err != nil {
		return "", fmt.Errorf("inft: mint transaction failed for job %s: %w", req.InferenceJobID, err)
	}

	receipt, err := m.waitForReceipt(ctx, txHash)
	if err != nil {
		return "", fmt.Errorf("inft: failed to confirm mint for job %s: %w", req.InferenceJobID, err)
	}

	return receipt.tokenID, nil
}

// UpdateMetadata updates the encrypted metadata of an existing iNFT.
func (m *minter) UpdateMetadata(ctx context.Context, tokenID string, meta EncryptedMeta) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("inft: context cancelled before update: %w", err)
	}

	payload := struct {
		TokenID       string        `json:"token_id"`
		EncryptedMeta EncryptedMeta `json:"encrypted_meta"`
	}{
		TokenID:       tokenID,
		EncryptedMeta: meta,
	}

	txData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("inft: failed to marshal update tx: %w", err)
	}

	txHash, err := m.sendTransaction(ctx, txData)
	if err != nil {
		return fmt.Errorf("inft: update transaction failed for token %s: %w", tokenID, err)
	}

	if _, err := m.waitForReceipt(ctx, txHash); err != nil {
		return fmt.Errorf("inft: failed to confirm update for token %s: %w", tokenID, err)
	}

	return nil
}

// GetStatus queries the on-chain state of a minted iNFT.
func (m *minter) GetStatus(ctx context.Context, tokenID string) (*INFTStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("inft: context cancelled: %w", err)
	}

	result, err := m.callContract(ctx, "getTokenStatus", tokenID)
	if err != nil {
		return nil, fmt.Errorf("inft: failed to get status for token %s: %w", tokenID, err)
	}

	if result == nil {
		return nil, fmt.Errorf("inft: token %s: %w", tokenID, ErrTokenNotFound)
	}

	return result, nil
}

// sendTransaction submits a transaction to the 0G Chain via JSON-RPC.
func (m *minter) sendTransaction(ctx context.Context, data []byte) (string, error) {
	dataHex := "0x" + hex.EncodeToString(data)

	rpcReq := rpcRequest{
		JSONRPC: "2.0",
		Method:  "eth_sendTransaction",
		Params: []any{
			map[string]string{
				"from": m.cfg.PrivateKey,
				"to":   m.cfg.ContractAddress,
				"data": dataHex,
			},
		},
		ID: 1,
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return "", fmt.Errorf("inft: failed to marshal RPC request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.ChainRPC, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("inft: failed to create RPC request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("inft: RPC request failed: %w", ErrChainUnreachable)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("inft: failed to read RPC response: %w", err)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return "", fmt.Errorf("inft: failed to parse RPC response: %w", err)
	}

	if rpcResp.Error != nil {
		if rpcResp.Error.Code == -32000 {
			return "", fmt.Errorf("inft: %s: %w", rpcResp.Error.Message, ErrInsufficientGas)
		}
		return "", fmt.Errorf("inft: RPC error: %s: %w", rpcResp.Error.Message, ErrMintFailed)
	}

	var txHash string
	if err := json.Unmarshal(rpcResp.Result, &txHash); err != nil {
		return "", fmt.Errorf("inft: failed to parse tx hash: %w", err)
	}

	return txHash, nil
}

type txReceipt struct {
	tokenID string
	txHash  string
}

// waitForReceipt polls for a transaction receipt until confirmed or context cancelled.
func (m *minter) waitForReceipt(ctx context.Context, txHash string) (*txReceipt, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeout := time.After(2 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("inft: context cancelled waiting for tx %s: %w", txHash, ctx.Err())
		case <-timeout:
			return nil, fmt.Errorf("inft: timeout waiting for tx %s", txHash)
		case <-ticker.C:
			receipt, err := m.getReceipt(ctx, txHash)
			if err != nil {
				continue
			}
			if receipt != nil {
				return receipt, nil
			}
		}
	}
}

func (m *minter) getReceipt(ctx context.Context, txHash string) (*txReceipt, error) {
	rpcReq := rpcRequest{
		JSONRPC: "2.0",
		Method:  "eth_getTransactionReceipt",
		Params:  []any{txHash},
		ID:      1,
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.ChainRPC, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, err
	}

	if rpcResp.Result == nil || string(rpcResp.Result) == "null" {
		return nil, nil
	}

	// Extract token ID from receipt (simplified: use tx hash as synthetic token ID)
	hash := sha256.Sum256([]byte(txHash))
	tokenID := hex.EncodeToString(hash[:8])

	return &txReceipt{
		tokenID: tokenID,
		txHash:  txHash,
	}, nil
}

// callContract performs an eth_call to read contract state.
func (m *minter) callContract(ctx context.Context, method string, tokenID string) (*INFTStatus, error) {
	callData := fmt.Sprintf("0x%s%s", method, tokenID)

	rpcReq := rpcRequest{
		JSONRPC: "2.0",
		Method:  "eth_call",
		Params: []any{
			map[string]string{
				"to":   m.cfg.ContractAddress,
				"data": callData,
			},
			"latest",
		},
		ID: 1,
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.ChainRPC, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("inft: contract call failed: %w", ErrChainUnreachable)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, err
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("inft: contract call error: %s", rpcResp.Error.Message)
	}

	if rpcResp.Result == nil || string(rpcResp.Result) == "\"0x\"" {
		return nil, nil
	}

	return &INFTStatus{
		TokenID:         tokenID,
		ChainID:         m.cfg.ChainID,
		ContractAddress: m.cfg.ContractAddress,
	}, nil
}
