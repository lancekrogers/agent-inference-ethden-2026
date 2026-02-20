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
	// Type identifies the kind of event.
	Type EventType `json:"type"`

	// AgentID identifies the inference agent that produced this event.
	AgentID string `json:"agent_id"`

	// TaskID links to the coordinator task that triggered this action.
	TaskID string `json:"task_id,omitempty"`

	// JobID links to the 0G compute job (if applicable).
	JobID string `json:"job_id,omitempty"`

	// InputHash is the hash of input data (privacy-preserving).
	InputHash string `json:"input_hash,omitempty"`

	// OutputHash is the hash of output data.
	OutputHash string `json:"output_hash,omitempty"`

	// StorageRef is the 0G Storage content ID for full data.
	StorageRef string `json:"storage_ref,omitempty"`

	// INFTRef is the iNFT token ID if one was minted.
	INFTRef string `json:"inft_ref,omitempty"`

	// Details contains event-specific data.
	Details map[string]string `json:"details,omitempty"`

	// Timestamp is when the event occurred.
	Timestamp time.Time `json:"timestamp"`
}

// Submission tracks a DA submission for later verification.
type Submission struct {
	// ID is the submission identifier returned by 0G DA.
	ID string `json:"id"`

	// EventType is the type of audit event that was submitted.
	EventType EventType `json:"event_type"`

	// Namespace is the DA namespace used.
	Namespace string `json:"namespace"`

	// BlockHeight is the DA block containing this submission.
	BlockHeight uint64 `json:"block_height"`

	// SubmittedAt is when the submission was made.
	SubmittedAt time.Time `json:"submitted_at"`

	// Verified indicates whether availability has been confirmed.
	Verified bool `json:"verified"`
}

// PublisherConfig holds configuration for the 0G DA audit publisher.
type PublisherConfig struct {
	// Endpoint is the 0G DA node URL.
	// Testnet: 0G DA entrance contract at 0xE75A073dA5bb7b0eC622170Fd268f35E675a957B
	Endpoint string

	// Namespace is the DA namespace for this agent's audit events.
	Namespace string

	// MaxRetries is the number of retry attempts for failed submissions.
	MaxRetries int
}

// daRequest is the submission payload for 0G DA.
type daRequest struct {
	Data      string `json:"data"`
	Namespace string `json:"namespace"`
}

// daResponse is the response from a DA submission.
type daResponse struct {
	SubmissionID string `json:"submission_id"`
	BlockHeight  uint64 `json:"block_height"`
	Status       string `json:"status"`
}

// daVerifyResponse is the response from a DA verification query.
type daVerifyResponse struct {
	Available bool   `json:"available"`
	Status    string `json:"status"`
}
