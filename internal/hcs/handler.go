// Package hcs handles Hedera Consensus Service integration for receiving
// task assignments from the coordinator and publishing results.
//
// This package mirrors the message envelope format from the agent-coordinator's
// HCS package. The inference agent subscribes to a task topic, receives
// TaskAssignment messages, and publishes TaskResult and HealthStatus messages
// back to the coordinator.
//
// The transport is abstracted via the Transport interface to allow testing
// without a live Hedera network connection.
package hcs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"
)

// Transport abstracts the HCS topic operations for testability.
// In production this wraps the Hedera SDK; in tests it uses a mock.
type Transport interface {
	// Publish sends raw bytes to an HCS topic.
	Publish(ctx context.Context, topicID string, data []byte) error

	// Subscribe starts receiving messages from an HCS topic.
	// Messages are delivered to the returned channel until ctx is cancelled.
	Subscribe(ctx context.Context, topicID string) (<-chan []byte, <-chan error)
}

// TaskHandler processes incoming task assignments from the coordinator.
type TaskHandler interface {
	HandleTask(ctx context.Context, task TaskAssignment) error
}

// ResultPublisher sends task results back to the coordinator via HCS.
type ResultPublisher interface {
	PublishResult(ctx context.Context, result TaskResult) error
	PublishHealth(ctx context.Context, status HealthStatus) error
}

// HandlerConfig holds configuration for the HCS handler.
type HandlerConfig struct {
	// Transport is the HCS transport implementation.
	Transport Transport

	// TaskTopicID is the HCS topic for receiving task assignments.
	TaskTopicID string

	// ResultTopicID is the HCS topic for publishing results.
	ResultTopicID string

	// AgentID is this agent's unique identifier.
	AgentID string
}

// Handler manages HCS subscriptions and publishing for the inference agent.
// It implements both TaskHandler and ResultPublisher.
type Handler struct {
	cfg    HandlerConfig
	seqNum atomic.Uint64
	taskCh chan TaskAssignment
}

// NewHandler creates an HCS handler for the inference agent.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{
		cfg:    cfg,
		taskCh: make(chan TaskAssignment, 16),
	}
}

// Tasks returns a read-only channel of incoming task assignments.
func (h *Handler) Tasks() <-chan TaskAssignment {
	return h.taskCh
}

// StartSubscription begins listening for task assignments on HCS.
// It runs until the context is cancelled. Malformed messages are logged and skipped.
func (h *Handler) StartSubscription(ctx context.Context) error {
	msgCh, errCh := h.cfg.Transport.Subscribe(ctx, h.cfg.TaskTopicID)
	if msgCh == nil {
		return ErrSubscriptionFailed
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			if err != nil {
				return fmt.Errorf("hcs: subscription error: %w", ErrSubscriptionFailed)
			}
		case data, ok := <-msgCh:
			if !ok {
				return nil
			}
			h.processMessage(ctx, data)
		}
	}
}

func (h *Handler) processMessage(ctx context.Context, data []byte) {
	env, err := UnmarshalEnvelope(data)
	if err != nil {
		return // skip malformed messages
	}

	if env.Type != MessageTypeTaskAssignment {
		return // skip non-task messages
	}

	// Filter: only accept messages addressed to us or broadcast
	if env.Recipient != "" && env.Recipient != h.cfg.AgentID {
		return
	}

	var task TaskAssignment
	if err := json.Unmarshal(env.Payload, &task); err != nil {
		return // skip messages with invalid payload
	}

	select {
	case h.taskCh <- task:
	case <-ctx.Done():
	}
}

// HandleTask processes a task assignment (satisfies TaskHandler interface).
func (h *Handler) HandleTask(ctx context.Context, task TaskAssignment) error {
	select {
	case h.taskCh <- task:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// PublishResult sends a task result to the coordinator via HCS.
func (h *Handler) PublishResult(ctx context.Context, result TaskResult) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("hcs: context cancelled before publish result: %w", err)
	}

	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("hcs: failed to marshal result: %w", err)
	}

	env := Envelope{
		Type:        MessageTypeTaskResult,
		Sender:      h.cfg.AgentID,
		TaskID:      result.TaskID,
		SequenceNum: h.seqNum.Add(1),
		Timestamp:   time.Now(),
		Payload:     payload,
	}

	data, err := env.Marshal()
	if err != nil {
		return fmt.Errorf("hcs: failed to marshal envelope: %w", err)
	}

	if err := h.cfg.Transport.Publish(ctx, h.cfg.ResultTopicID, data); err != nil {
		return fmt.Errorf("hcs: failed to publish result for task %s: %w", result.TaskID, ErrPublishFailed)
	}

	return nil
}

// PublishHealth sends a health status update to the coordinator via HCS.
func (h *Handler) PublishHealth(ctx context.Context, status HealthStatus) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("hcs: context cancelled before publish health: %w", err)
	}

	payload, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("hcs: failed to marshal health status: %w", err)
	}

	env := Envelope{
		Type:        MessageTypeHeartbeat,
		Sender:      h.cfg.AgentID,
		SequenceNum: h.seqNum.Add(1),
		Timestamp:   time.Now(),
		Payload:     payload,
	}

	data, err := env.Marshal()
	if err != nil {
		return fmt.Errorf("hcs: failed to marshal envelope: %w", err)
	}

	if err := h.cfg.Transport.Publish(ctx, h.cfg.ResultTopicID, data); err != nil {
		return fmt.Errorf("hcs: failed to publish health: %w", ErrPublishFailed)
	}

	return nil
}
