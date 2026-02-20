// Package compute integrates with 0G decentralized GPU compute for inference jobs.
//
// 0G Compute uses an OpenAI-compatible REST API served through a broker pattern.
// There is no Go SDK — only TypeScript (@0glabs/0g-serving-broker). This package
// wraps the REST API directly.
//
// Architecture:
//
//	User App → 0G Serving API → Provider Broker → GPU Model (vLLM)
//
// The serving API exposes OpenAI-compatible endpoints at:
//
//	POST /api/services/query       — Submit inference query
//	GET  /api/services/list        — List available services/models
//
// Testnet: 0G-Galileo (chain ID 16602), EVM RPC: https://evmrpc-testnet.0g.ai
// Faucet: https://faucet.0g.ai
package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// ComputeBroker submits inference jobs to 0G decentralized GPU compute
// and retrieves results.
type ComputeBroker interface {
	// SubmitJob sends an inference job to the 0G compute network.
	// Returns a job ID that can be used to poll for results.
	SubmitJob(ctx context.Context, req JobRequest) (string, error)

	// GetResult polls for the result of a previously submitted job.
	// Returns ErrJobPending if the job is still running.
	GetResult(ctx context.Context, jobID string) (*JobResult, error)

	// ListModels returns the available AI models on the 0G compute network.
	ListModels(ctx context.Context) ([]Model, error)
}

// broker implements ComputeBroker using the 0G serving REST API.
type broker struct {
	cfg    BrokerConfig
	client *http.Client

	mu        sync.RWMutex
	models    []Model
	modelsTTL time.Time
}

const modelCacheDuration = 5 * time.Minute

// NewBroker creates a new ComputeBroker connected to the 0G serving network.
func NewBroker(cfg BrokerConfig) ComputeBroker {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.PollTimeout == 0 {
		cfg.PollTimeout = 5 * time.Minute
	}
	return &broker{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SubmitJob sends an inference request to the 0G compute network.
func (b *broker) SubmitJob(ctx context.Context, req JobRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("compute: context cancelled before submit: %w", err)
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
		return "", fmt.Errorf("compute: failed to marshal request: %w", err)
	}

	endpoint := b.cfg.Endpoint + "/api/services/query"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("compute: failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("compute: broker request failed: %w", ErrBrokerDown)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("compute: failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("compute: broker returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("compute: failed to parse response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("compute: API error: %s: %w", chatResp.Error.Message, ErrJobFailed)
	}

	return chatResp.ID, nil
}

// GetResult polls for a job result with configurable timeout and interval.
func (b *broker) GetResult(ctx context.Context, jobID string) (*JobResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("compute: context cancelled: %w", err)
	}

	deadline := time.After(b.cfg.PollTimeout)
	ticker := time.NewTicker(b.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("compute: context cancelled while polling job %s: %w", jobID, ctx.Err())
		case <-deadline:
			return nil, fmt.Errorf("compute: timeout waiting for job %s after %v", jobID, b.cfg.PollTimeout)
		case <-ticker.C:
			result, err := b.fetchJobResult(ctx, jobID)
			if err != nil {
				return nil, fmt.Errorf("compute: failed to fetch result for job %s: %w", jobID, err)
			}
			switch result.Status {
			case JobStatusCompleted:
				return result, nil
			case JobStatusFailed:
				return nil, fmt.Errorf("compute: job %s failed: %s: %w", jobID, result.Error, ErrJobFailed)
			default:
				continue
			}
		}
	}
}

// ListModels returns the available AI models from 0G service providers.
// Results are cached for 5 minutes to avoid excessive API calls.
func (b *broker) ListModels(ctx context.Context) ([]Model, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("compute: context cancelled: %w", err)
	}

	if models := b.cachedModels(); models != nil {
		return models, nil
	}

	endpoint := b.cfg.Endpoint + "/api/services/list"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("compute: failed to create request: %w", err)
	}

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("compute: failed to list services: %w", ErrBrokerDown)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("compute: failed to read services response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("compute: list services returned status %d: %s", resp.StatusCode, string(body))
	}

	var services []serviceEntry
	if err := json.Unmarshal(body, &services); err != nil {
		return nil, fmt.Errorf("compute: failed to parse services: %w", err)
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

func (b *broker) fetchJobResult(ctx context.Context, jobID string) (*JobResult, error) {
	endpoint := fmt.Sprintf("%s/api/services/query/%s", b.cfg.Endpoint, jobID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("compute: failed to create status request: %w", err)
	}

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("compute: status request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("compute: failed to read status response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return &JobResult{JobID: jobID, Status: JobStatusPending}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("compute: status returned %d: %s", resp.StatusCode, string(body))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return nil, fmt.Errorf("compute: failed to parse status: %w", err)
	}

	if chatResp.Error != nil {
		return &JobResult{
			JobID:  jobID,
			Status: JobStatusFailed,
			Error:  chatResp.Error.Message,
		}, nil
	}

	output := ""
	if len(chatResp.Choices) > 0 {
		output = chatResp.Choices[0].Message.Content
	}

	return &JobResult{
		JobID:      jobID,
		Status:     JobStatusCompleted,
		Output:     output,
		ModelID:    chatResp.Model,
		TokensUsed: chatResp.Usage.TotalTokens,
	}, nil
}
