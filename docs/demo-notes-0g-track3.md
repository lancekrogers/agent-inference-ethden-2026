# Demo Notes: 0G Track 3 - ERC-7857 iNFT

Talking points and demo script for the 0G Track 3 ($7k) bounty presentation.

## Key Message

"Every AI inference result becomes a verifiable, encrypted NFT on-chain -- creating an immutable provenance chain from task to result to payment."

## Demo Flow (3-5 minutes)

### 1. Show the Pipeline (30s)

Open `internal/agent/agent.go` and walk through the 7-stage pipeline:

> "When the agent receives a task, it flows through seven stages. Stage 5 is where we mint the ERC-7857 iNFT. Notice that the tokenID flows forward to the HCS result report -- the coordinator knows which NFT corresponds to which task."

### 2. Show the Encryption (60s)

Open `internal/zerog/inft/minter.go`:

> "Before minting, we encrypt the inference metadata with AES-256-GCM. Each mint gets a fresh random nonce from crypto/rand. The key_id field supports rotation -- different keys for different security contexts."

Show the `EncryptedMeta` struct:
- ciphertext (base64-encoded)
- nonce (12 bytes, random)
- key_id ("default")
- algorithm ("AES-256-GCM")

### 3. Show the Mint Call (60s)

Show the contract ABI:

> "The mint function takes the agent address, a human-readable name, the encrypted metadata blob, a SHA-256 hash of the inference output, and a reference to 0G Storage. All of this goes on-chain in a single transaction."

Key point: The result hash lets anyone verify integrity without decrypting.

### 4. Show the Full Chain (60s)

Walk through the provenance chain:

```
Task Assignment (HCS) → Model Selection (on-chain) → GPU Inference (0G Compute)
→ Data Anchoring (0G Storage) → iNFT Mint (ERC-7857) → Audit Trail (0G DA)
→ Result Report (HCS with tokenID)
```

> "The coordinator receives the task result with the iNFT token ID, storage content ID, and DA submission ID. Anyone can independently verify each link in this chain."

### 5. Security Properties (30s)

> "Only the key holder can decrypt the metadata. The result hash proves integrity. The on-chain transaction proves who minted it and when. And the DA audit trail makes the entire pipeline verifiable."

## Talking Points for Judges

- **Why ERC-7857 over ERC-721?** Standard NFTs can't hold encrypted metadata. ERC-7857 is purpose-built for confidential AI inference results.
- **Why encrypt at all?** Inference outputs may contain proprietary or sensitive data. Encryption enables selective disclosure.
- **How does key rotation work?** The `key_id` field identifies which key encrypted each iNFT. Agents can rotate keys without invalidating old tokens.
- **What about gas costs?** The mint transaction is ~150-250k gas on 0G Galileo. At testnet gas prices, this is negligible.
- **Is the iNFT transferable?** Yes -- standard `ownerOf` and `Transfer` event semantics from ERC-721.

## Fallback Plan

If the 0G Galileo testnet is slow or the iNFT contract isn't deployed:

1. Show the code: `internal/zerog/inft/minter.go` has the complete implementation
2. Show the unit tests: all encryption, minting, and status flows are tested
3. Show the ABI: the contract interface is fully specified
4. Show the pipeline: `agent.go` demonstrates the full 7-stage flow
5. Emphasize: "The iNFT contract is a deploy-and-call -- the Go integration is complete"
