package hcs

import (
	"encoding/json"
	"errors"
	"time"
)

// Sentinel errors for HCS operations.
var (
	ErrSubscriptionFailed = errors.New("hcs: topic subscription failed")
	ErrPublishFailed      = errors.New("hcs: message publish failed")
	ErrInvalidMessage     = errors.New("hcs: received invalid message format")
	ErrTopicNotFound      = errors.New("hcs: topic not found")
)

// MessageType identifies the kind of protocol message in an envelope.
// These types match the coordinator's message protocol.
type MessageType string

const (
	MessageTypeTaskAssignment MessageType = "task_assignment"
	MessageTypeStatusUpdate   MessageType = "status_update"
	MessageTypeTaskResult     MessageType = "task_result"
	MessageTypeHeartbeat      MessageType = "heartbeat"
)

// Envelope is the standard message format for all protocol messages
// sent through HCS topics. This format MUST match the coordinator's
// envelope format exactly for interoperability.
type Envelope struct {
	Type        MessageType     `json:"type"`
	Sender      string          `json:"sender"`
	Recipient   string          `json:"recipient,omitempty"`
	TaskID      string          `json:"task_id,omitempty"`
	SequenceNum uint64          `json:"sequence_num"`
	Timestamp   time.Time       `json:"timestamp"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

// Marshal serializes the envelope to JSON bytes for publishing to HCS.
func (e *Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEnvelope deserializes JSON bytes from HCS into an Envelope.
func UnmarshalEnvelope(data []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

// TaskAssignment is received from the coordinator when a new task is assigned.
type TaskAssignment struct {
	TaskID      string    `json:"task_id"`
	ModelID     string    `json:"model_id"`
	Input       string    `json:"input"`
	Priority    int       `json:"priority"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	CallbackURL string    `json:"callback_url,omitempty"`
	Deadline    time.Time `json:"deadline,omitempty"`
}

// TaskResult is published back to the coordinator when a task completes.
type TaskResult struct {
	TaskID            string `json:"task_id"`
	Status            string `json:"status"`
	Output            string `json:"output,omitempty"`
	DurationMs        int64  `json:"duration_ms,omitempty"`
	TokensUsed        int    `json:"tokens_used,omitempty"`
	StorageContentID  string `json:"storage_content_id,omitempty"`
	INFTTokenID       string `json:"inft_token_id,omitempty"`
	AuditSubmissionID string `json:"audit_submission_id,omitempty"`
	Error             string `json:"error,omitempty"`
}

// HealthStatus is published periodically to signal agent liveness.
type HealthStatus struct {
	AgentID        string `json:"agent_id"`
	Status         string `json:"status"`
	ActiveTaskID   string `json:"active_task_id,omitempty"`
	UptimeSeconds  int64  `json:"uptime_seconds"`
	CompletedTasks int    `json:"completed_tasks"`
	FailedTasks    int    `json:"failed_tasks"`
}
