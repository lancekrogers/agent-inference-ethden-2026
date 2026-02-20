package inft

import (
	"encoding/json"
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
	// Name is the human-readable name for this iNFT.
	Name string `json:"name"`

	// Description describes what this iNFT represents.
	Description string `json:"description"`

	// InferenceJobID links this iNFT to the inference job that produced it.
	InferenceJobID string `json:"inference_job_id"`

	// ResultHash is the hash of the inference result for integrity verification.
	ResultHash string `json:"result_hash"`

	// PlaintextMeta is the metadata to encrypt before attaching to the iNFT.
	PlaintextMeta map[string]string `json:"plaintext_meta,omitempty"`

	// StorageContentID is the 0G Storage content ID where the full result is stored.
	StorageContentID string `json:"storage_content_id,omitempty"`
}

// EncryptedMeta holds AES-256-GCM encrypted iNFT metadata.
type EncryptedMeta struct {
	// Ciphertext is the encrypted data.
	Ciphertext []byte `json:"ciphertext"`

	// Nonce is the encryption nonce used with AES-256-GCM.
	Nonce []byte `json:"nonce"`

	// KeyID identifies which encryption key was used.
	KeyID string `json:"key_id"`

	// Algorithm identifies the encryption algorithm.
	Algorithm string `json:"algorithm"`
}

// INFTStatus describes the current state of a minted iNFT.
type INFTStatus struct {
	// TokenID is the on-chain token identifier.
	TokenID string `json:"token_id"`

	// Owner is the current owner address.
	Owner string `json:"owner"`

	// MintedAt is when the iNFT was created.
	MintedAt time.Time `json:"minted_at"`

	// MetadataHash is the hash of the current encrypted metadata.
	MetadataHash string `json:"metadata_hash"`

	// ChainID identifies which chain the iNFT is on.
	ChainID int64 `json:"chain_id"`

	// ContractAddress is the iNFT contract address.
	ContractAddress string `json:"contract_address"`

	// TxHash is the transaction hash of the most recent update.
	TxHash string `json:"tx_hash"`
}

// MinterConfig holds configuration for the iNFT minter.
type MinterConfig struct {
	// ChainRPC is the 0G Chain JSON-RPC endpoint.
	// Testnet: https://evmrpc-testnet.0g.ai
	ChainRPC string

	// ChainID is the 0G Chain network identifier.
	// Testnet (Galileo): 16602
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

// mintTransaction represents the JSON-RPC transaction for minting.
type mintTransaction struct {
	Name            string        `json:"name"`
	Description     string        `json:"description"`
	EncryptedMeta   EncryptedMeta `json:"encrypted_meta"`
	ResultHash      string        `json:"result_hash"`
	StorageRef      string        `json:"storage_ref"`
	InferenceJobID  string        `json:"inference_job_id"`
}

// rpcRequest is a JSON-RPC 2.0 request.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

// rpcResponse is a JSON-RPC 2.0 response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      int             `json:"id"`
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
