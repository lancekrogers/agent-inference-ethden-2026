// Package compute integrates with 0G decentralized GPU compute for inference jobs.
//
// Architecture:
//
//	On-chain: Serving contract for provider discovery
//	Off-chain: OpenAI-compatible HTTP calls to provider endpoints (/v1/chat/completions)
//
// Testnet: 0G-Galileo (chain ID 16602), EVM RPC: https://evmrpc-testnet.0g.ai
package compute

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog"
)

// servingABIJSON matches the 0G InferenceServing contract on Galileo testnet.
// Reverse-engineered from on-chain response data at contract
// 0xa79F4c8311FF93C06b8CfB403690cc987c93F91E (chain ID 16602).
// The Service struct has 11 fields; field order must exactly match the contract.
const servingABIJSON = `[
  {
    "name": "getAllServices",
    "type": "function",
    "stateMutability": "view",
    "inputs": [
      {"name": "offset", "type": "uint256"},
      {"name": "limit", "type": "uint256"}
    ],
    "outputs": [
      {
        "name": "services",
        "type": "tuple[]",
        "components": [
          {"name": "provider", "type": "address"},
          {"name": "name", "type": "string"},
          {"name": "url", "type": "string"},
          {"name": "inputPrice", "type": "uint256"},
          {"name": "outputPrice", "type": "uint256"},
          {"name": "updatedAt", "type": "uint256"},
          {"name": "model", "type": "string"},
          {"name": "verifiability", "type": "string"},
          {"name": "content", "type": "string"},
          {"name": "signer", "type": "address"},
          {"name": "occupied", "type": "bool"}
        ]
      },
      {"name": "total", "type": "uint256"}
    ]
  },
  {
    "name": "getService",
    "type": "function",
    "stateMutability": "view",
    "inputs": [
      {"name": "provider", "type": "address"}
    ],
    "outputs": [
      {
        "name": "",
        "type": "tuple",
        "components": [
          {"name": "provider", "type": "address"},
          {"name": "name", "type": "string"},
          {"name": "url", "type": "string"},
          {"name": "inputPrice", "type": "uint256"},
          {"name": "outputPrice", "type": "uint256"},
          {"name": "updatedAt", "type": "uint256"},
          {"name": "model", "type": "string"},
          {"name": "verifiability", "type": "string"},
          {"name": "content", "type": "string"},
          {"name": "signer", "type": "address"},
          {"name": "occupied", "type": "bool"}
        ]
      }
    ]
  }
]`

var servingABI = mustParseABI(servingABIJSON)

func mustParseABI(raw string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic("compute: invalid ABI: " + err.Error())
	}
	return parsed
}

const (
	modelCacheDuration = 5 * time.Minute
	// servicesPageLimit is the maximum number of services the contract allows
	// per getAllServices call. The contract reverts with limit > 50.
	servicesPageLimit = 50
)

// ComputeBroker submits inference jobs to 0G decentralized GPU compute.
type ComputeBroker interface {
	SubmitJob(ctx context.Context, req JobRequest) (string, error)
	GetResult(ctx context.Context, jobID string) (*JobResult, error)
	ListModels(ctx context.Context) ([]Model, error)
}

type broker struct {
	cfg      BrokerConfig
	backend  zerog.ChainBackend
	contract *bind.BoundContract
	key      *ecdsa.PrivateKey
	client   *http.Client

	mu        sync.RWMutex
	models    []Model
	modelsTTL time.Time

	results sync.Map // jobID → *JobResult
}

// NewBroker creates a new ComputeBroker.
// Uses on-chain serving contract for provider discovery, HTTP for inference.
func NewBroker(cfg BrokerConfig, backend zerog.ChainBackend, key *ecdsa.PrivateKey) ComputeBroker {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.PollTimeout == 0 {
		cfg.PollTimeout = 5 * time.Minute
	}

	contractAddr := common.HexToAddress(cfg.ServingContractAddress)
	bc := bind.NewBoundContract(contractAddr, servingABI, backend, backend, backend)

	return &broker{
		cfg:      cfg,
		backend:  backend,
		contract: bc,
		key:      key,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (b *broker) SubmitJob(ctx context.Context, req JobRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("compute: context cancelled before submit: %w", err)
	}

	// Discover provider URL for the requested model
	providerURL, err := b.resolveProvider(ctx, req.ModelID)
	if err != nil {
		return "", fmt.Errorf("compute: resolve provider for %s: %w", req.ModelID, err)
	}

	chatReq := chatRequest{
		Model: req.ModelID,
		Messages: []chatMessage{
			{Role: "user", Content: req.Input},
		},
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return "", fmt.Errorf("compute: marshal request: %w", err)
	}

	endpoint := providerURL + "/v1/proxy/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("compute: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Attach signed Bearer token for 0G session auth.
	if b.key != nil {
		token, tokenErr := b.buildAuthToken()
		if tokenErr != nil {
			return "", fmt.Errorf("compute: build auth token: %w", tokenErr)
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := b.doWithAuthRetry(ctx, httpReq, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	const maxResponseBytes = 1 << 20 // 1 MB
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("compute: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("compute: provider returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("compute: parse response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("compute: API error: %s: %w", chatResp.Error.Message, ErrJobFailed)
	}

	// Cache the result for GetResult
	output := ""
	if len(chatResp.Choices) > 0 {
		output = chatResp.Choices[0].Message.Content
	}

	result := &JobResult{
		JobID:      chatResp.ID,
		Status:     JobStatusCompleted,
		Output:     output,
		ModelID:    chatResp.Model,
		TokensUsed: chatResp.Usage.TotalTokens,
	}
	b.results.Store(chatResp.ID, result)

	return chatResp.ID, nil
}

// buildAuthToken constructs a signed Bearer token for 0G Compute session auth.
// Format: app-sk-<base64(timestamp:0xSignatureHex)>
func (b *broker) buildAuthToken() (string, error) {
	msg := fmt.Sprintf("%d", time.Now().Unix())
	msgHash := crypto.Keccak256Hash([]byte(msg))

	sig, err := crypto.Sign(msgHash.Bytes(), b.key)
	if err != nil {
		return "", fmt.Errorf("sign auth message: %w", err)
	}

	payload := fmt.Sprintf("%s:%s", msg, hexutil.Encode(sig))
	token := "app-sk-" + base64.StdEncoding.EncodeToString([]byte(payload))
	return token, nil
}

// doWithAuthRetry executes the HTTP request and retries once on 401
// with a fresh auth token.
func (b *broker) doWithAuthRetry(ctx context.Context, req *http.Request, body []byte) (*http.Response, error) {
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("compute: provider request failed: %w", ErrBrokerDown)
	}

	if resp.StatusCode != http.StatusUnauthorized || b.key == nil {
		return resp, nil
	}

	// 401 — refresh token and retry once.
	resp.Body.Close()

	token, tokenErr := b.buildAuthToken()
	if tokenErr != nil {
		return nil, fmt.Errorf("compute: refresh auth token: %w", tokenErr)
	}

	retryReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("compute: create retry request: %w", err)
	}
	retryReq.Header.Set("Content-Type", "application/json")
	retryReq.Header.Set("Authorization", "Bearer "+token)

	resp, err = b.client.Do(retryReq)
	if err != nil {
		return nil, fmt.Errorf("compute: retry request failed: %w", ErrBrokerDown)
	}

	return resp, nil
}

func (b *broker) GetResult(ctx context.Context, jobID string) (*JobResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("compute: context cancelled: %w", err)
	}

	// Check cache first (populated by SubmitJob)
	if val, ok := b.results.Load(jobID); ok {
		return val.(*JobResult), nil
	}

	// Poll for result (fallback for async providers)
	deadline := time.After(b.cfg.PollTimeout)
	ticker := time.NewTicker(b.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("compute: context cancelled polling job %s: %w", jobID, ctx.Err())
		case <-deadline:
			return nil, fmt.Errorf("compute: timeout waiting for job %s after %v", jobID, b.cfg.PollTimeout)
		case <-ticker.C:
			if val, ok := b.results.Load(jobID); ok {
				return val.(*JobResult), nil
			}
		}
	}
}

func (b *broker) ListModels(ctx context.Context) ([]Model, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("compute: context cancelled: %w", err)
	}

	if models := b.cachedModels(); models != nil {
		return models, nil
	}

	models, err := b.listFromChain(ctx)
	if err != nil {
		// Fall back to HTTP endpoint if chain query fails and endpoint is set
		if b.cfg.Endpoint != "" {
			return b.listFromHTTP(ctx)
		}
		return nil, fmt.Errorf("compute: list models from chain: %w", err)
	}

	if len(models) == 0 {
		return nil, ErrNoModels
	}

	b.cacheModels(models)
	return models, nil
}

func (b *broker) listFromChain(ctx context.Context) ([]Model, error) {
	var result []interface{}
	err := b.contract.Call(&bind.CallOpts{Context: ctx}, &result, "getAllServices", big.NewInt(0), big.NewInt(servicesPageLimit))
	if err != nil {
		return nil, fmt.Errorf("getAllServices: %w", err)
	}

	if len(result) < 2 {
		return nil, nil
	}

	// result[0] is the services array, result[1] is the total count.
	// Struct field order must match the contract's Service struct exactly.
	services, ok := result[0].([]struct {
		Provider      common.Address `json:"provider"`
		Name          string         `json:"name"`
		Url           string         `json:"url"`
		InputPrice    *big.Int       `json:"inputPrice"`
		OutputPrice   *big.Int       `json:"outputPrice"`
		UpdatedAt     *big.Int       `json:"updatedAt"`
		Model         string         `json:"model"`
		Verifiability string         `json:"verifiability"`
		Content       string         `json:"content"`
		Signer        common.Address `json:"signer"`
		Occupied      bool           `json:"occupied"`
	})
	if !ok {
		return nil, fmt.Errorf("unexpected services type: %T", result[0])
	}

	models := make([]Model, 0, len(services))
	for _, svc := range services {
		models = append(models, Model{
			ID:       svc.Model,
			Name:     svc.Name,
			Provider: svc.Provider.Hex(),
			URL:      svc.Url,
		})
	}

	return models, nil
}

func (b *broker) listFromHTTP(ctx context.Context) ([]Model, error) {
	endpoint := b.cfg.Endpoint + "/api/services/list"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("list services: %w", ErrBrokerDown)
	}
	defer resp.Body.Close()

	const maxListBytes = 64 * 1024 // 64 KB
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list returned status %d: %s", resp.StatusCode, string(body))
	}

	type serviceEntry struct {
		Provider    string `json:"providerAddress"`
		Name        string `json:"name"`
		ServiceType string `json:"serviceType"`
		URL         string `json:"url"`
		Model       string `json:"model"`
	}

	var services []serviceEntry
	if err := json.Unmarshal(body, &services); err != nil {
		return nil, fmt.Errorf("parse services: %w", err)
	}

	if len(services) == 0 {
		return nil, ErrNoModels
	}

	models := make([]Model, len(services))
	for i, svc := range services {
		models[i] = Model{
			ID:          svc.Model,
			Name:        svc.Name,
			Provider:    svc.Provider,
			ServiceType: svc.ServiceType,
			URL:         svc.URL,
		}
	}

	b.cacheModels(models)
	return models, nil
}

func (b *broker) resolveProvider(ctx context.Context, modelID string) (string, error) {
	// Try cache first
	if models := b.cachedModels(); models != nil {
		for _, m := range models {
			if m.ID == modelID && m.URL != "" {
				return m.URL, nil
			}
		}
	}

	// Query chain for services
	models, err := b.ListModels(ctx)
	if err != nil {
		// Last resort: use fallback endpoint
		if b.cfg.Endpoint != "" {
			return b.cfg.Endpoint, nil
		}
		return "", fmt.Errorf("no provider for model %s: %w", modelID, err)
	}

	for _, m := range models {
		if m.ID == modelID && m.URL != "" {
			return m.URL, nil
		}
	}

	// If model not found but we have a fallback endpoint, use it
	if b.cfg.Endpoint != "" {
		return b.cfg.Endpoint, nil
	}

	return "", fmt.Errorf("no provider for model %s: %w", modelID, ErrNoModels)
}

func (b *broker) cachedModels() []Model {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.models != nil && time.Now().Before(b.modelsTTL) {
		dst := make([]Model, len(b.models))
		copy(dst, b.models)
		return dst
	}
	return nil
}

func (b *broker) cacheModels(models []Model) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.models = models
	b.modelsTTL = time.Now().Add(modelCacheDuration)
}

