// Package da integrates with 0G Data Availability for publishing
// inference audit trails.
//
// 0G DA provides a data availability layer where blobs are submitted to
// DA nodes and confirmed on-chain. A Go client exists at
// github.com/0glabs/0g-da-client for low-level operations.
//
// This package uses the REST API exposed by the DA indexer for simpler
// CRUD operations suitable for audit trail publishing.
//
// Architecture:
//
//	Agent → DA Indexer REST API → 0G DA Nodes → On-chain DA Entrance Contract
//
// Testnet DA entrance: 0xE75A073dA5bb7b0eC622170Fd268f35E675a957B (Galileo)
package da

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AuditPublisher posts inference audit events to 0G Data Availability.
type AuditPublisher interface {
	// Publish submits an audit event to the 0G DA layer.
	// Returns a submission ID for verification.
	Publish(ctx context.Context, event AuditEvent) (string, error)

	// Verify confirms that a previously published audit event is available.
	Verify(ctx context.Context, submissionID string) (bool, error)
}

// publisher implements AuditPublisher using the 0G DA REST API.
type publisher struct {
	cfg    PublisherConfig
	client *http.Client
}

// NewPublisher creates a new AuditPublisher connected to 0G DA.
func NewPublisher(cfg PublisherConfig) AuditPublisher {
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "inference-audit"
	}
	return &publisher{
		cfg: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Publish serializes an audit event and submits it to 0G DA with retry logic.
func (p *publisher) Publish(ctx context.Context, event AuditEvent) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("da: context cancelled before publish: %w", err)
	}

	data, err := serializeEvent(event)
	if err != nil {
		return "", fmt.Errorf("da: failed to serialize event %s: %w", event.Type, err)
	}

	sub, err := p.publishWithRetry(ctx, data)
	if err != nil {
		return "", fmt.Errorf("da: failed to publish event %s: %w", event.Type, err)
	}

	return sub.ID, nil
}

// Verify checks whether a previously submitted event is available on DA.
func (p *publisher) Verify(ctx context.Context, submissionID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("da: context cancelled before verify: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/da/verify/%s", p.cfg.Endpoint, submissionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("da: failed to create verify request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("da: verify request failed: %w", ErrDANodeUnreachable)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("da: failed to read verify response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("da: verify returned status %d: %s", resp.StatusCode, string(body))
	}

	var verifyResp daVerifyResponse
	if err := json.Unmarshal(body, &verifyResp); err != nil {
		return false, fmt.Errorf("da: failed to parse verify response: %w", err)
	}

	return verifyResp.Available, nil
}

// serializeEvent produces deterministic JSON bytes for an audit event.
func serializeEvent(event AuditEvent) ([]byte, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("da: serialization failed: %w", ErrSerializeFailed)
	}
	return data, nil
}

func (p *publisher) publishWithRetry(ctx context.Context, data []byte) (*Submission, error) {
	var lastErr error
	for attempt := 0; attempt <= p.cfg.MaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("da: context cancelled on attempt %d: %w", attempt+1, err)
		}

		sub, err := p.submitToDA(ctx, data)
		if err == nil {
			return sub, nil
		}
		lastErr = err

		if attempt < p.cfg.MaxRetries {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("da: context cancelled during backoff: %w", ctx.Err())
			case <-time.After(backoff):
			}
		}
	}
	return nil, fmt.Errorf("da: all %d attempts failed: %w", p.cfg.MaxRetries+1, lastErr)
}

func (p *publisher) submitToDA(ctx context.Context, data []byte) (*Submission, error) {
	daReq := daRequest{
		Data:      base64.StdEncoding.EncodeToString(data),
		Namespace: p.cfg.Namespace,
	}

	body, err := json.Marshal(daReq)
	if err != nil {
		return nil, fmt.Errorf("da: failed to marshal DA request: %w", err)
	}

	endpoint := p.cfg.Endpoint + "/api/da/submit"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("da: failed to create submit request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("da: submit request failed: %w", ErrDANodeUnreachable)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("da: failed to read submit response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("da: submit returned status %d: %s: %w", resp.StatusCode, string(respBody), ErrSubmissionFailed)
	}

	var daResp daResponse
	if err := json.Unmarshal(respBody, &daResp); err != nil {
		return nil, fmt.Errorf("da: failed to parse submit response: %w", err)
	}

	return &Submission{
		ID:          daResp.SubmissionID,
		BlockHeight: daResp.BlockHeight,
		SubmittedAt: time.Now(),
	}, nil
}
