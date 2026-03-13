package compute

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/lancekrogers/agent-inference/internal/zerog"
)

// Testnet contract addresses from 0G SDK constants.ts
const (
	ledgerManagerAddress   = "0xE70830508dAc0A97e6c087c75f402f9Be669E406"
	inferenceServingAddr   = "0xa79F4c8311FF93C06b8CfB403690cc987c93F91E"
	galileoChainID         = 16602
	ephemeralTokenID       = 255
	ephemeralMaxDurationMs = 24 * 60 * 60 * 1000 // 24 hours
)

// sessionToken matches the TypeScript SDK's SessionToken struct exactly.
// The provider validates this JSON structure when decoding the auth header.
type sessionToken struct {
	Address    string `json:"address"`
	Provider   string `json:"provider"`
	Timestamp  int64  `json:"timestamp"`
	ExpiresAt  int64  `json:"expiresAt"`
	Nonce      string `json:"nonce"`
	Generation int    `json:"generation"`
	TokenID    int    `json:"tokenId"`
}

// sessionManager handles on-chain session establishment and auth token generation
// for the 0G Compute Network.
type sessionManager struct {
	key     *ecdsa.PrivateKey
	backend zerog.ChainBackend
	chainID int64

	ledger  *bind.BoundContract
	serving *bind.BoundContract

	mu             sync.Mutex
	cachedToken    string
	cachedProvider string
	tokenExpiry    time.Time
	setupDone      map[string]bool // provider → setup complete
}

func newSessionManager(key *ecdsa.PrivateKey, backend zerog.ChainBackend, chainID int64) *sessionManager {
	ledgerAddr := common.HexToAddress(ledgerManagerAddress)
	servingAddr := common.HexToAddress(inferenceServingAddr)

	return &sessionManager{
		key:       key,
		backend:   backend,
		chainID:   chainID,
		ledger:    bind.NewBoundContract(ledgerAddr, ledgerABI, backend, backend, backend),
		serving:   bind.NewBoundContract(servingAddr, servingSessionABI, backend, backend, backend),
		setupDone: make(map[string]bool),
	}
}

// invalidate clears the cached session token so the next call re-generates it.
func (s *sessionManager) invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cachedToken = ""
	s.tokenExpiry = time.Time{}
}

// EnsureSession creates the on-chain account and funds if needed, then returns
// a valid auth token for the given provider.
func (s *sessionManager) EnsureSession(ctx context.Context, providerAddress string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Return cached token if valid (>1hr remaining) and same provider
	if s.cachedProvider == providerAddress && time.Now().Before(s.tokenExpiry.Add(-1*time.Hour)) {
		return s.cachedToken, nil
	}

	if !s.setupDone[providerAddress] {
		if err := s.setupOnChain(ctx, providerAddress); err != nil {
			slog.Warn("on-chain session setup failed — generating token anyway",
				"provider", providerAddress,
				"error", err,
				"hint", "fund wallet at https://faucet.0g.ai with ≥0.1 A0GI")
			// Mark as done to avoid retrying every request
			s.setupDone[providerAddress] = true
		} else {
			s.setupDone[providerAddress] = true
		}
	}

	token, err := s.buildSessionToken(providerAddress)
	if err != nil {
		return "", err
	}

	s.cachedToken = token
	s.cachedProvider = providerAddress
	s.tokenExpiry = time.Now().Add(24 * time.Hour)
	return token, nil
}

// setupOnChain performs the three-step account establishment:
// 1. Create ledger (main account) if not exists, with deposit
// 2. Transfer funds to provider sub-account
// 3. Acknowledge TEE signer
func (s *sessionManager) setupOnChain(ctx context.Context, providerAddress string) error {
	userAddr := zerog.AddressFromKey(s.key)
	provider := common.HexToAddress(providerAddress)

	opts, err := zerog.MakeTransactOpts(ctx, s.key, s.chainID)
	if err != nil {
		return err
	}

	// Step 1: Check if ledger exists, create if not
	if err := s.ensureLedger(ctx, userAddr, opts); err != nil {
		return fmt.Errorf("ensure ledger: %w", err)
	}

	// Step 2: Transfer funds to provider sub-account if no account exists
	if err := s.ensureProviderAccount(ctx, userAddr, provider, opts); err != nil {
		return fmt.Errorf("ensure provider account: %w", err)
	}

	// Step 3: Acknowledge TEE signer
	if err := s.ensureAcknowledged(ctx, userAddr, provider, opts); err != nil {
		return fmt.Errorf("acknowledge TEE signer: %w", err)
	}

	return nil
}

func (s *sessionManager) ensureLedger(ctx context.Context, userAddr common.Address, opts *bind.TransactOpts) error {
	// Check if ledger exists
	var result []interface{}
	err := s.ledger.Call(&bind.CallOpts{Context: ctx}, &result, "getLedger", userAddr)
	if err == nil && len(result) > 0 {
		return nil // ledger exists
	}

	// Create ledger with 0.1 A0GI deposit (contract minimum is 10^17)
	depositAmount := new(big.Int).Exp(big.NewInt(10), big.NewInt(17), nil) // 0.1 * 10^18 = 10^17

	slog.Info("creating 0G ledger",
		"wallet", userAddr.Hex(),
		"deposit", "0.1 A0GI",
		"ledger_contract", ledgerManagerAddress)
	txOpts := *opts
	txOpts.Value = depositAmount

	tx, err := s.ledger.Transact(&txOpts, "addLedger", "")
	if err != nil {
		return fmt.Errorf("addLedger tx: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, s.backend, tx)
	if err != nil {
		return fmt.Errorf("addLedger wait: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("addLedger reverted (tx: %s)", tx.Hash().Hex())
	}

	return nil
}

func (s *sessionManager) ensureProviderAccount(ctx context.Context, userAddr, provider common.Address, opts *bind.TransactOpts) error {
	// Check if account exists
	var result []interface{}
	err := s.serving.Call(&bind.CallOpts{Context: ctx}, &result, "getAccount", userAddr, provider)
	if err == nil && len(result) > 0 {
		return nil // account exists
	}

	// Transfer to provider sub-account via ledger (provider requires ≥0.1 A0GI locked balance)
	transferAmount := new(big.Int).Exp(big.NewInt(10), big.NewInt(17), nil) // 10^17 = 0.1 A0GI

	slog.Info("transferring funds to provider sub-account",
		"provider", provider.Hex(),
		"amount", "0.1 A0GI")
	txOpts := *opts
	txOpts.Value = nil

	tx, err := s.ledger.Transact(&txOpts, "transferFund", provider, "inference-v1.0", transferAmount)
	if err != nil {
		return fmt.Errorf("transferFund tx: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, s.backend, tx)
	if err != nil {
		return fmt.Errorf("transferFund wait: %w", err)
	}
	if receipt.Status != 1 {
		return fmt.Errorf("transferFund reverted (tx: %s)", tx.Hash().Hex())
	}

	return nil
}

func (s *sessionManager) ensureAcknowledged(ctx context.Context, userAddr, provider common.Address, opts *bind.TransactOpts) error {
	// Check if already acknowledged by trying to get account
	var result []interface{}
	err := s.serving.Call(&bind.CallOpts{Context: ctx}, &result, "getAccount", userAddr, provider)
	if err == nil && len(result) > 0 {
		// Account exists — check if acknowledged field is true
		// The account struct has an `acknowledged` bool field
		// For now, try acknowledgement anyway; it's idempotent
	}

	txOpts := *opts
	txOpts.Value = nil

	tx, err := s.serving.Transact(&txOpts, "acknowledgeTEESigner", provider, true)
	if err != nil {
		// May already be acknowledged — not fatal
		if strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "reverted") {
			return nil
		}
		return fmt.Errorf("acknowledgeTEESigner tx: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, s.backend, tx)
	if err != nil {
		return fmt.Errorf("acknowledgeTEESigner wait: %w", err)
	}
	if receipt.Status != 1 {
		// Not fatal — may already be acknowledged
		return nil
	}

	return nil
}

// buildSessionToken creates a signed ephemeral session token matching
// the 0G TypeScript SDK format exactly.
// Format: app-sk-<base64(JSON_message|EIP191_signature)>
func (s *sessionManager) buildSessionToken(providerAddress string) (string, error) {
	userAddr := zerog.AddressFromKey(s.key)
	now := time.Now().UnixMilli()

	nonce, err := generateNonce()
	if err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	token := sessionToken{
		Address:    userAddr.Hex(),
		Provider:   providerAddress,
		Timestamp:  now,
		ExpiresAt:  now + int64(ephemeralMaxDurationMs),
		Nonce:      nonce,
		Generation: 0,
		TokenID:    ephemeralTokenID,
	}

	message, err := json.Marshal(token)
	if err != nil {
		return "", fmt.Errorf("marshal session token: %w", err)
	}

	// Hash the JSON message with keccak256 (matching TypeScript SDK)
	messageHash := crypto.Keccak256(message)

	// Sign using EIP-191 personal_sign: prefix + hash
	// ethers.js signMessage does: sign(keccak256("\x19Ethereum Signed Message:\n32" + hash))
	prefixedHash := signHash(messageHash)
	sig, err := crypto.Sign(prefixedHash, s.key)
	if err != nil {
		return "", fmt.Errorf("sign session token: %w", err)
	}

	// Adjust V value from 0/1 to 27/28 for Ethereum compatibility
	if sig[64] < 27 {
		sig[64] += 27
	}

	sigHex := "0x" + hex.EncodeToString(sig)

	// Build the raw token: base64(JSON_message + "|" + signature)
	raw := string(message) + "|" + sigHex
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))

	return "app-sk-" + encoded, nil
}

// signHash applies the Ethereum signed message prefix (EIP-191).
func signHash(data []byte) []byte {
	msg := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(data), data)
	return crypto.Keccak256([]byte(msg))
}

func generateNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// --- ABI definitions for session management contracts ---

const ledgerABIJSON = `[
  {
    "name": "addLedger",
    "type": "function",
    "stateMutability": "payable",
    "inputs": [{"name": "additionalInfo", "type": "string"}],
    "outputs": []
  },
  {
    "name": "depositFund",
    "type": "function",
    "stateMutability": "payable",
    "inputs": [],
    "outputs": []
  },
  {
    "name": "getLedger",
    "type": "function",
    "stateMutability": "view",
    "inputs": [{"name": "user", "type": "address"}],
    "outputs": [
      {
        "name": "",
        "type": "tuple",
        "components": [
          {"name": "user", "type": "address"},
          {"name": "totalBalance", "type": "uint256"},
          {"name": "availableBalance", "type": "uint256"},
          {"name": "additionalInfo", "type": "string"}
        ]
      }
    ]
  },
  {
    "name": "transferFund",
    "type": "function",
    "stateMutability": "nonpayable",
    "inputs": [
      {"name": "provider", "type": "address"},
      {"name": "serviceName", "type": "string"},
      {"name": "amount", "type": "uint256"}
    ],
    "outputs": []
  }
]`

const servingSessionABIJSON = `[
  {
    "name": "getAccount",
    "type": "function",
    "stateMutability": "view",
    "inputs": [
      {"name": "user", "type": "address"},
      {"name": "provider", "type": "address"}
    ],
    "outputs": [
      {
        "name": "",
        "type": "tuple",
        "components": [
          {"name": "user", "type": "address"},
          {"name": "provider", "type": "address"},
          {"name": "balance", "type": "uint256"},
          {"name": "pendingRefund", "type": "uint256"},
          {"name": "acknowledged", "type": "bool"},
          {"name": "generation", "type": "uint256"},
          {"name": "revokedBitmap", "type": "uint256"},
          {"name": "validRefundsLength", "type": "uint256"}
        ]
      }
    ]
  },
  {
    "name": "acknowledgeTEESigner",
    "type": "function",
    "stateMutability": "nonpayable",
    "inputs": [
      {"name": "provider", "type": "address"},
      {"name": "acknowledged", "type": "bool"}
    ],
    "outputs": []
  }
]`

// ledgerABI and servingSessionABI are parsed at init time.
// mustParseABI is defined in broker.go.
var (
	ledgerABI         = mustParseABI(ledgerABIJSON)
	servingSessionABI = mustParseABI(servingSessionABIJSON)
)
