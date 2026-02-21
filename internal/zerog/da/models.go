package da

import (
	"errors"
	"time"
)

// Sentinel errors for DA operations.
var (
	ErrSubmissionFailed  = errors.New("da: submission to DA layer failed")
	ErrNotAvailable      = errors.New("da: data not yet available")
	ErrDANodeUnreachable = errors.New("da: DA node unreachable")
	ErrSerializeFailed   = errors.New("da: event serialization failed")
)

// EventType identifies what kind of audit event occurred.
type EventType string

const (
	EventTypeTaskReceived EventType = "task_received"
	EventTypeJobSubmitted EventType = "job_submitted"
	EventTypeJobCompleted EventType = "job_completed"
	EventTypeJobFailed    EventType = "job_failed"
	EventTypeResultStored EventType = "result_stored"
	EventTypeINFTMinted   EventType = "inft_minted"
	EventTypeResultReport EventType = "result_reported"
)

// AuditEvent represents a single auditable action by the inference agent.
type AuditEvent struct {
	Type       EventType         `json:"type"`
	AgentID    string            `json:"agent_id"`
	TaskID     string            `json:"task_id,omitempty"`
	JobID      string            `json:"job_id,omitempty"`
	InputHash  string            `json:"input_hash,omitempty"`
	OutputHash string            `json:"output_hash,omitempty"`
	StorageRef string            `json:"storage_ref,omitempty"`
	INFTRef    string            `json:"inft_ref,omitempty"`
	Details    map[string]string `json:"details,omitempty"`
	Timestamp  time.Time         `json:"timestamp"`
}

// Submission tracks a DA submission for later verification.
type Submission struct {
	ID          string    `json:"id"`
	EventType   EventType `json:"event_type"`
	Namespace   string    `json:"namespace"`
	BlockHeight uint64    `json:"block_height"`
	SubmittedAt time.Time `json:"submitted_at"`
	Verified    bool      `json:"verified"`
}

// PublisherConfig holds configuration for the 0G DA audit publisher.
type PublisherConfig struct {
	// ChainRPC is the 0G Chain JSON-RPC endpoint.
	ChainRPC string
	// ChainID is the chain identifier (Galileo: 16602).
	ChainID int64
	// DAContractAddress is the DA Entrance contract on 0G Chain.
	// Galileo default: 0xE75A073dA5bb7b0eC622170Fd268f35E675a957B
	DAContractAddress string
	// PrivateKey is the hex-encoded private key for signing.
	PrivateKey string
	// Namespace is the DA namespace for this agent's audit events.
	Namespace string
	// MaxRetries is the number of retry attempts for failed submissions.
	MaxRetries int

	// Endpoint is a legacy field for backward compat with REST mode.
	Endpoint string
}
