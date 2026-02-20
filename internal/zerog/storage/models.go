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
	// ContentID is the content-addressed identifier for retrieval.
	ContentID string `json:"content_id"`

	// Name is the human-readable name for the data.
	Name string `json:"name"`

	// Size is the data size in bytes.
	Size int64 `json:"size"`

	// ContentType is the MIME type of the stored data.
	ContentType string `json:"content_type,omitempty"`

	// CreatedAt is when the data was stored.
	CreatedAt time.Time `json:"created_at"`

	// Tags holds arbitrary key-value metadata.
	Tags map[string]string `json:"tags,omitempty"`
}

// ClientConfig holds configuration for the 0G Storage client.
type ClientConfig struct {
	// Endpoint is the 0G Storage indexer/node URL.
	// Testnet: https://indexer-storage-testnet-turbo.0g.ai
	Endpoint string

	// DefaultChunkSize is the default chunk size for uploads (bytes).
	// Defaults to 4MB if zero.
	DefaultChunkSize int64

	// MaxRetries is the number of retry attempts for failed operations.
	MaxRetries int
}

// uploadRequest is the JSON payload for an upload to 0G Storage.
type uploadRequest struct {
	Data        string            `json:"data"`
	Name        string            `json:"name"`
	ContentType string            `json:"content_type,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	ChunkIndex  int               `json:"chunk_index,omitempty"`
	TotalChunks int               `json:"total_chunks,omitempty"`
}

// uploadResponse is the JSON response from a successful upload.
type uploadResponse struct {
	ContentID string `json:"content_id"`
	Size      int64  `json:"size"`
}

// listResponse is the JSON response from a list query.
type listResponse struct {
	Items []Metadata `json:"items"`
}
