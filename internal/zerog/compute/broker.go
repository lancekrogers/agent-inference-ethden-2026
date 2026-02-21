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
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog"
)

const servingABIJSON = `[
  {
    "name": "getServiceCount",
    "type": "function",
    "stateMutability": "view",
    "inputs": [],
    "outputs": [
      {"name": "", "type": "uint256"}
    ]
  },
  {
    "name": "getService",
    "type": "function",
    "stateMutability": "view",
    "inputs": [
      {"name": "index", "type": "uint256"}
    ],
    "outputs": [
      {"name": "provider", "type": "address"},
      {"name": "name", "type": "string"},
      {"name": "serviceType", "type": "string"},
      {"name": "url", "type": "string"},
      {"name": "model", "type": "string"}
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

const modelCacheDuration = 5 * time.Minute

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

	endpoint := providerURL + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("compute: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("compute: provider request failed: %w", ErrBrokerDown)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
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
	// Get service count
	var countResult []interface{}
	err := b.contract.Call(&bind.CallOpts{Context: ctx}, &countResult, "getServiceCount")
	if err != nil {
		return nil, fmt.Errorf("getServiceCount: %w", err)
	}

	if len(countResult) == 0 {
		return nil, nil
	}

	count, ok := countResult[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected count type")
	}

	n := int(count.Int64())
	models := make([]Model, 0, n)

	for i := 0; i < n; i++ {
		var svcResult []interface{}
		err := b.contract.Call(&bind.CallOpts{Context: ctx}, &svcResult, "getService", big.NewInt(int64(i)))
		if err != nil {
			continue // skip unavailable services
		}

		if len(svcResult) < 5 {
			continue
		}

		provider, _ := svcResult[0].(common.Address)
		name, _ := svcResult[1].(string)
		svcType, _ := svcResult[2].(string)
		url, _ := svcResult[3].(string)
		model, _ := svcResult[4].(string)

		models = append(models, Model{
			ID:          model,
			Name:        name,
			Provider:    provider.Hex(),
			ServiceType: svcType,
			URL:         url,
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

	body, err := io.ReadAll(resp.Body)
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

// Ensure ethereum import is used (needed for CallMsg in listFromChain).
var _ = ethereum.CallMsg{}
