# ERC-7857 iNFT Showcase: Encrypted Inference Provenance

Demonstration of the ERC-7857 encrypted iNFT implementation for the 0G Track 3 bounty.

## Overview

Every inference result processed by the agent is minted as an ERC-7857 iNFT (intelligent NFT) on 0G Chain. The iNFT contains:

- **Encrypted metadata**: AES-256-GCM encrypted key-value pairs (model ID, task ID, timestamps)
- **Result hash**: SHA-256 hash of the inference output for integrity verification
- **Storage reference**: 0G Storage content ID linking to the persisted result
- **On-chain provenance**: Immutable record of which model, provider, and agent produced the result

## Why ERC-7857?

ERC-7857 extends the standard NFT model with encrypted metadata capabilities:

| Feature | ERC-721 | ERC-7857 |
|---------|---------|----------|
| Ownership tracking | Yes | Yes |
| Public metadata | Yes | Yes |
| Encrypted metadata | No | Yes (AES-256-GCM) |
| Selective disclosure | No | Yes (key-based access) |
| AI inference provenance | No | Purpose-built |

This makes ERC-7857 ideal for AI inference results where:
- The inference output itself may be confidential
- Provenance must be verifiable without revealing content
- Different parties can be granted decryption access via key sharing

## Implementation

### Contract Interface

The iNFT contract on 0G Chain exposes:

```solidity
function mint(
    address to,
    string memory name,
    string memory description,
    bytes memory encryptedMeta,
    bytes32 resultHash,
    string memory storageRef
) external returns (uint256 tokenId);

function updateEncryptedMetadata(
    uint256 tokenId,
    bytes memory encryptedMeta
) external;

function ownerOf(uint256 tokenId) external view returns (address);
```

### Go Integration

The `INFTMinter` interface in `internal/zerog/inft/`:

```go
type INFTMinter interface {
    Mint(ctx context.Context, req MintRequest) (string, error)
    UpdateMetadata(ctx context.Context, tokenID string, meta EncryptedMeta) error
    GetStatus(ctx context.Context, tokenID string) (*INFTStatus, error)
}
```

### Encryption Flow

```
Step 1: Collect plaintext metadata
  {
    "task_id": "task-inference-01",
    "model_id": "qwen/qwen-2.5-7b-instruct",
    "provider": "0x1234...abcd",
    "timestamp": "2026-02-21T10:05:00Z",
    "tokens_used": "512"
  }

Step 2: Encrypt with AES-256-GCM
  key:       32-byte key from ZG_ENCRYPTION_KEY (hex-encoded)
  nonce:     12-byte random (crypto/rand)
  plaintext: JSON-marshaled metadata
  output:    ciphertext + nonce + key_id + algorithm tag

Step 3: Marshal EncryptedMeta
  {
    "ciphertext": "<base64>",
    "nonce":      "<base64>",
    "key_id":     "default",
    "algorithm":  "AES-256-GCM"
  }

Step 4: Mint on-chain
  mint(agentAddress, name, description, encryptedMetaBytes, resultHash, storageContentID)
  → Parse Transfer event from receipt → extract tokenID from log.Topics[3]
```

### Mint Request Structure

```go
type MintRequest struct {
    Name             string            // Human-readable iNFT name
    Description      string            // What this iNFT represents
    InferenceJobID   string            // Links to the compute job
    ResultHash       string            // SHA-256 of inference output
    PlaintextMeta    map[string]string // Encrypted before minting
    StorageContentID string            // 0G Storage reference
}
```

### Result

After minting, the `INFTStatus` contains:

```go
type INFTStatus struct {
    TokenID         string    // Decimal token ID
    Owner           string    // Agent's address
    MintedAt        time.Time
    MetadataHash    string    // Hash of encrypted metadata
    ChainID         int64     // 16602 (Galileo)
    ContractAddress string    // ERC-7857 contract
    TxHash          string    // Mint transaction hash
}
```

## Pipeline Integration

The iNFT mint is stage 5 of the 7-stage inference pipeline:

```
1. HCS receive      → Task assignment from coordinator
2. Provider discover → getAllServices() on InferenceServing contract
3. GPU inference     → POST /v1/proxy/chat/completions
4. Storage anchor    → Flow contract submit() + blob upload
5. iNFT mint        → ERC-7857 mint() with encrypted metadata  ← HERE
6. DA audit          → submitOriginalData() on DA Entrance
7. HCS report        → task_result with tokenID, storageRef, auditRef
```

The iNFT tokenID and storage content ID are included in the `task_result` message published back to the coordinator, creating a complete provenance chain from task assignment through inference to on-chain record.

## Security Properties

| Property | Mechanism |
|----------|-----------|
| Confidentiality | AES-256-GCM encryption; only key holders can decrypt |
| Integrity | SHA-256 result hash stored on-chain; any tampering is detectable |
| Provenance | Token owner = minting agent; verified via `ownerOf()` |
| Non-repudiation | On-chain transaction signed by agent's ECDSA key |
| Key rotation | `key_id` field supports multiple encryption keys |
| Nonce uniqueness | `crypto/rand` generates fresh 12-byte nonce per mint |

## 0G Track 3 Alignment

| Requirement | Implementation |
|-------------|----------------|
| ERC-7857 implementation | Full mint/update/status interface on 0G Chain |
| Encrypted metadata | AES-256-GCM with per-mint random nonce |
| On-chain storage | Mint transaction + result hash + storage ref all on Galileo |
| AI inference integration | Each inference result automatically minted as iNFT |
| Provenance chain | task → inference → storage → iNFT → DA audit → HCS report |

## On-Chain Contracts

| Contract | Network | Address |
|----------|---------|---------|
| ERC-7857 iNFT | 0G Galileo (16602) | Set via `ZG_INFT_CONTRACT` |
| InferenceServing | 0G Galileo (16602) | `0xa79F4c8311FF93C06b8CfB403690cc987c93F91E` |
| Flow (Storage) | 0G Galileo (16602) | `0x22E03a6A89B950F1c82ec5e74F8eCa321a105296` |
| DA Entrance | 0G Galileo (16602) | `0xE75A073dA5bb7b0eC622170Fd268f35E675a957B` |

## Verification

To verify an iNFT on-chain:

1. Query `ownerOf(tokenID)` on the ERC-7857 contract to confirm ownership
2. Retrieve the encrypted metadata from the mint transaction's calldata
3. Decrypt with the AES-256-GCM key (if authorized)
4. Verify the result hash matches the stored inference output
5. Cross-reference the storage content ID with 0G Storage to retrieve the full result
