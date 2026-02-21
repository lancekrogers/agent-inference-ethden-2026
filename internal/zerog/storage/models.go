package storage

import (
	"errors"
	"time"
)

// Sentinel errors for storage operations.
var (
	ErrNotFound     = errors.New("storage: content not found")
	ErrUploadFailed = errors.New("storage: upload failed")
	ErrNodeDown     = errors.New("storage: storage node unreachable")
	ErrIntegrity    = errors.New("storage: data integrity check failed")
)

// Metadata describes a stored item on 0G Storage.
type Metadata struct {
	ContentID   string            `json:"content_id"`
	Name        string            `json:"name"`
	Size        int64             `json:"size"`
	ContentType string            `json:"content_type,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	Tags        map[string]string `json:"tags,omitempty"`
}

// ClientConfig holds configuration for the 0G Storage client.
type ClientConfig struct {
	// ChainRPC is the 0G Chain JSON-RPC endpoint for Flow contract interaction.
	ChainRPC string
	// ChainID is the chain identifier (Galileo: 16602).
	ChainID int64
	// FlowContractAddress is the Flow contract on 0G Chain.
	// Galileo default: 0x22E03a6A89B950F1c82ec5e74F8eCa321a105296
	FlowContractAddress string
	// PrivateKey is the hex-encoded private key for signing.
	PrivateKey string
	// StorageNodeEndpoint is the HTTP URL for the 0G Storage indexer/node.
	StorageNodeEndpoint string
	// DefaultChunkSize is the chunk size for uploads (bytes). Defaults to 4MB.
	DefaultChunkSize int64
	// MaxRetries is the number of retry attempts for failed operations.
	MaxRetries int

	// Endpoint is a legacy field for backward compat with REST mode.
	// If StorageNodeEndpoint is empty, falls back to Endpoint.
	Endpoint string
}

func (c *ClientConfig) storageEndpoint() string {
	if c.StorageNodeEndpoint != "" {
		return c.StorageNodeEndpoint
	}
	return c.Endpoint
}
