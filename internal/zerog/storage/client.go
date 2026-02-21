// Package storage integrates with 0G decentralized storage for persisting
// inference results and agent memory.
//
// Architecture:
//
//	Agent → Flow Contract (on-chain anchoring) + Storage Nodes (data upload/download)
//
// Upload: compute data root → submit to Flow contract → upload data to storage node
// Download/List: HTTP calls to storage node indexer
//
// Flow contract: 0x22E03a6A89B950F1c82ec5e74F8eCa321a105296 (Galileo)
package storage

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog"
)

const defaultChunkSize = 4 * 1024 * 1024 // 4MB

const flowABIJSON = `[
  {
    "name": "submit",
    "type": "function",
    "inputs": [
      {"name": "dataRoot", "type": "bytes32"},
      {"name": "length", "type": "uint256"}
    ],
    "outputs": []
  },
  {
    "name": "DataSubmit",
    "type": "event",
    "inputs": [
      {"name": "sender", "type": "address", "indexed": true},
      {"name": "dataRoot", "type": "bytes32", "indexed": true},
      {"name": "index", "type": "uint256", "indexed": false}
    ]
  }
]`

var flowABI = mustParseABI(flowABIJSON)

func mustParseABI(raw string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic("storage: invalid ABI: " + err.Error())
	}
	return parsed
}

// StorageClient persists and retrieves data from 0G decentralized storage.
type StorageClient interface {
	Upload(ctx context.Context, data []byte, meta Metadata) (string, error)
	Download(ctx context.Context, contentID string) ([]byte, error)
	List(ctx context.Context, prefix string) ([]Metadata, error)
}

type client struct {
	cfg        ClientConfig
	backend    zerog.ChainBackend
	contract   *bind.BoundContract
	key        *ecdsa.PrivateKey
	httpClient *http.Client
}

// NewClient creates a new StorageClient connected to 0G Storage.
// The backend and key are used for Flow contract interactions.
func NewClient(cfg ClientConfig, backend zerog.ChainBackend, key *ecdsa.PrivateKey) StorageClient {
	if cfg.DefaultChunkSize == 0 {
		cfg.DefaultChunkSize = defaultChunkSize
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}

	contractAddr := common.HexToAddress(cfg.FlowContractAddress)
	bc := bind.NewBoundContract(contractAddr, flowABI, backend, backend, backend)

	return &client{
		cfg:      cfg,
		backend:  backend,
		contract: bc,
		key:      key,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *client) Upload(ctx context.Context, data []byte, meta Metadata) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("storage: context cancelled before upload: %w", err)
	}

	// Compute data root (SHA-256 of content)
	hash := sha256.Sum256(data)
	dataRoot := hash

	// Submit data root to Flow contract on-chain
	opts, err := zerog.MakeTransactOpts(ctx, c.key, c.cfg.ChainID)
	if err != nil {
		return "", fmt.Errorf("storage: create transact opts: %w", err)
	}

	length := new(big.Int).SetInt64(int64(len(data)))
	tx, err := c.contract.Transact(opts, "submit", dataRoot, length)
	if err != nil {
		return "", fmt.Errorf("storage: flow submit tx: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, c.backend, tx)
	if err != nil {
		return "", fmt.Errorf("storage: wait for flow tx %s: %w", tx.Hash().Hex(), err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return "", fmt.Errorf("storage: flow submit reverted: %w", ErrUploadFailed)
	}

	contentID := common.Bytes2Hex(dataRoot[:])

	// Upload data to storage node if endpoint is configured
	if endpoint := c.cfg.storageEndpoint(); endpoint != "" {
		if err := c.uploadToNode(ctx, data, meta, contentID); err != nil {
			return "", fmt.Errorf("storage: node upload: %w", err)
		}
	}

	return contentID, nil
}

func (c *client) Download(ctx context.Context, contentID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: context cancelled before download: %w", err)
	}

	endpoint := c.cfg.storageEndpoint()
	if endpoint == "" {
		return nil, fmt.Errorf("storage: no storage node endpoint configured: %w", ErrNodeDown)
	}

	url := fmt.Sprintf("%s/api/storage/%s", endpoint, contentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("storage: create download request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage: download failed: %w", ErrNodeDown)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("storage: content %s: %w", contentID, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("storage: download returned status %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

func (c *client) List(ctx context.Context, prefix string) ([]Metadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: context cancelled before list: %w", err)
	}

	endpoint := c.cfg.storageEndpoint()
	if endpoint == "" {
		return nil, fmt.Errorf("storage: no storage node endpoint configured: %w", ErrNodeDown)
	}

	url := fmt.Sprintf("%s/api/storage?prefix=%s", endpoint, prefix)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("storage: create list request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage: list failed: %w", ErrNodeDown)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("storage: read list response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("storage: list returned status %d: %s", resp.StatusCode, string(body))
	}

	var listResp struct {
		Items []Metadata `json:"items"`
	}
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("storage: parse list response: %w", err)
	}
	return listResp.Items, nil
}

func (c *client) uploadToNode(ctx context.Context, data []byte, meta Metadata, contentID string) error {
	payload := struct {
		Data        string            `json:"data"`
		Name        string            `json:"name"`
		ContentType string            `json:"content_type,omitempty"`
		Tags        map[string]string `json:"tags,omitempty"`
		ContentID   string            `json:"content_id"`
	}{
		Data:        base64.StdEncoding.EncodeToString(data),
		Name:        meta.Name,
		ContentType: meta.ContentType,
		Tags:        meta.Tags,
		ContentID:   contentID,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal upload request: %w", err)
	}

	endpoint := c.cfg.storageEndpoint() + "/api/storage"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("upload to node: %w", ErrNodeDown)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("node returned status %d: %s: %w", resp.StatusCode, string(respBody), ErrUploadFailed)
	}
	return nil
}
