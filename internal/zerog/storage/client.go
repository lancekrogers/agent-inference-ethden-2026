// Package storage integrates with 0G decentralized storage for persisting
// inference results and agent memory.
//
// 0G Storage provides a Go client library at github.com/0glabs/0g-storage-client
// for low-level Merkle tree and node operations. This package wraps the REST API
// exposed by the 0G Storage indexer for simpler CRUD operations.
//
// Architecture:
//
//	Agent → Storage Indexer REST API → 0G Storage Nodes → On-chain Flow Contract
//
// The indexer exposes endpoints for upload, download, and listing.
// Content is addressed by Merkle root hash (content ID).
//
// Testnet indexer: https://indexer-storage-testnet-turbo.0g.ai
// Flow contract: 0x22E03a6A89B950F1c82ec5e74F8eCa321a105296 (Galileo)
package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultChunkSize = 4 * 1024 * 1024 // 4MB

// StorageClient persists and retrieves data from 0G decentralized storage.
type StorageClient interface {
	// Upload stores data on 0G Storage. Returns a content identifier.
	Upload(ctx context.Context, data []byte, meta Metadata) (string, error)

	// Download retrieves data from 0G Storage by content identifier.
	Download(ctx context.Context, contentID string) ([]byte, error)

	// List returns metadata for stored items matching the given prefix.
	List(ctx context.Context, prefix string) ([]Metadata, error)
}

// client implements StorageClient using the 0G Storage indexer REST API.
type client struct {
	cfg        ClientConfig
	httpClient *http.Client
}

// NewClient creates a new StorageClient connected to 0G Storage.
func NewClient(cfg ClientConfig) StorageClient {
	if cfg.DefaultChunkSize == 0 {
		cfg.DefaultChunkSize = defaultChunkSize
	}
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	return &client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Upload stores data on 0G Storage. For data larger than the configured
// chunk size, it performs a chunked upload with context checks between chunks.
func (c *client) Upload(ctx context.Context, data []byte, meta Metadata) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("storage: context cancelled before upload: %w", err)
	}

	if int64(len(data)) > c.cfg.DefaultChunkSize {
		return c.uploadChunked(ctx, data, meta)
	}

	return c.uploadSingle(ctx, data, meta)
}

// Download retrieves data from 0G Storage by content identifier.
func (c *client) Download(ctx context.Context, contentID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: context cancelled before download: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/storage/%s", c.cfg.Endpoint, contentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to create download request: %w", err)
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

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to read download response: %w", err)
	}

	return data, nil
}

// List returns metadata for stored items matching the given prefix.
func (c *client) List(ctx context.Context, prefix string) ([]Metadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: context cancelled before list: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/storage?prefix=%s", c.cfg.Endpoint, prefix)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to create list request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage: list failed: %w", ErrNodeDown)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to read list response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("storage: list returned status %d: %s", resp.StatusCode, string(body))
	}

	var listResp listResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("storage: failed to parse list response: %w", err)
	}

	return listResp.Items, nil
}

func (c *client) uploadSingle(ctx context.Context, data []byte, meta Metadata) (string, error) {
	req := uploadRequest{
		Data:        base64.StdEncoding.EncodeToString(data),
		Name:        meta.Name,
		ContentType: meta.ContentType,
		Tags:        meta.Tags,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("storage: failed to marshal upload request: %w", err)
	}

	endpoint := c.cfg.Endpoint + "/api/storage"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("storage: failed to create upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("storage: upload failed: %w", ErrNodeDown)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("storage: failed to read upload response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("storage: upload returned status %d: %s: %w", resp.StatusCode, string(respBody), ErrUploadFailed)
	}

	var uploadResp uploadResponse
	if err := json.Unmarshal(respBody, &uploadResp); err != nil {
		return "", fmt.Errorf("storage: failed to parse upload response: %w", err)
	}

	return uploadResp.ContentID, nil
}

func (c *client) uploadChunked(ctx context.Context, data []byte, meta Metadata) (string, error) {
	chunkSize := c.cfg.DefaultChunkSize
	totalChunks := (int64(len(data)) + chunkSize - 1) / chunkSize
	var lastContentID string

	for i := int64(0); i < totalChunks; i++ {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("storage: context cancelled during chunk %d/%d: %w", i+1, totalChunks, err)
		}

		start := i * chunkSize
		end := start + chunkSize
		if end > int64(len(data)) {
			end = int64(len(data))
		}

		req := uploadRequest{
			Data:        base64.StdEncoding.EncodeToString(data[start:end]),
			Name:        meta.Name,
			ContentType: meta.ContentType,
			Tags:        meta.Tags,
			ChunkIndex:  int(i),
			TotalChunks: int(totalChunks),
		}

		body, err := json.Marshal(req)
		if err != nil {
			return "", fmt.Errorf("storage: failed to marshal chunk %d: %w", i, err)
		}

		endpoint := c.cfg.Endpoint + "/api/storage"
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return "", fmt.Errorf("storage: failed to create chunk %d request: %w", i, err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("storage: chunk %d upload failed: %w", i, ErrNodeDown)
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("storage: failed to read chunk %d response: %w", i, err)
		}

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			return "", fmt.Errorf("storage: chunk %d returned status %d: %s: %w", i, resp.StatusCode, string(respBody), ErrUploadFailed)
		}

		var uploadResp uploadResponse
		if err := json.Unmarshal(respBody, &uploadResp); err != nil {
			return "", fmt.Errorf("storage: failed to parse chunk %d response: %w", i, err)
		}
		lastContentID = uploadResp.ContentID
	}

	return lastContentID, nil
}
