package inft

import (
	"errors"
	"time"
)

// Sentinel errors for iNFT operations.
var (
	ErrMintFailed       = errors.New("inft: minting transaction failed")
	ErrTokenNotFound    = errors.New("inft: token not found")
	ErrEncryptionFailed = errors.New("inft: metadata encryption failed")
	ErrChainUnreachable = errors.New("inft: 0G Chain RPC unreachable")
	ErrInsufficientGas  = errors.New("inft: insufficient gas for transaction")
)

// MintRequest contains the parameters for minting a new iNFT.
type MintRequest struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	InferenceJobID   string            `json:"inference_job_id"`
	ResultHash       string            `json:"result_hash"`
	PlaintextMeta    map[string]string `json:"plaintext_meta,omitempty"`
	StorageContentID string            `json:"storage_content_id,omitempty"`
}

// EncryptedMeta holds AES-256-GCM encrypted iNFT metadata.
type EncryptedMeta struct {
	Ciphertext []byte `json:"ciphertext"`
	Nonce      []byte `json:"nonce"`
	KeyID      string `json:"key_id"`
	Algorithm  string `json:"algorithm"`
}

// INFTStatus describes the current state of a minted iNFT.
type INFTStatus struct {
	TokenID         string    `json:"token_id"`
	Owner           string    `json:"owner"`
	MintedAt        time.Time `json:"minted_at"`
	MetadataHash    string    `json:"metadata_hash"`
	ChainID         int64     `json:"chain_id"`
	ContractAddress string    `json:"contract_address"`
	TxHash          string    `json:"tx_hash"`
}

// MinterConfig holds configuration for the iNFT minter.
type MinterConfig struct {
	// ChainRPC is the 0G Chain JSON-RPC endpoint.
	ChainRPC string
	// ChainID is the 0G Chain network identifier (Galileo: 16602).
	ChainID int64
	// ContractAddress is the ERC-7857 contract address on 0G Chain.
	ContractAddress string
	// PrivateKey is the agent's hex-encoded private key for signing.
	PrivateKey string
	// EncryptionKey is the AES-256 key for metadata encryption (32 bytes).
	EncryptionKey []byte
	// EncryptionKeyID identifies the key for rotation tracking.
	EncryptionKeyID string
}
