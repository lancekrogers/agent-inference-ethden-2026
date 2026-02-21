package compute

import (
	"errors"
	"time"
)

// Sentinel errors for compute operations.
var (
	ErrJobPending = errors.New("compute: job is still pending")
	ErrJobFailed  = errors.New("compute: job execution failed")
	ErrNoModels   = errors.New("compute: no models available")
	ErrBrokerDown = errors.New("compute: broker is unreachable")
)

// JobStatus represents the state of an inference job.
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
)

// JobRequest describes an inference job to submit to 0G Compute.
type JobRequest struct {
	ModelID     string            `json:"model_id"`
	Input       string            `json:"input"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
	Temperature float64           `json:"temperature,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// JobResult contains the output of a completed inference job.
type JobResult struct {
	JobID      string        `json:"job_id"`
	Status     JobStatus     `json:"status"`
	Output     string        `json:"output"`
	ModelID    string        `json:"model_id"`
	TokensUsed int           `json:"tokens_used"`
	Duration   time.Duration `json:"duration"`
	Error      string        `json:"error,omitempty"`
}

// Model describes an available AI model on the 0G compute network.
type Model struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	ServiceType string `json:"service_type,omitempty"`
	URL         string `json:"url,omitempty"`
}

// BrokerConfig holds configuration for the 0G Compute broker.
type BrokerConfig struct {
	// ChainRPC is the 0G Chain JSON-RPC endpoint.
	ChainRPC string
	// ChainID is the chain identifier (Galileo: 16602).
	ChainID int64
	// ServingContractAddress is the serving contract for provider discovery.
	ServingContractAddress string
	// PrivateKey is the hex-encoded private key for signing.
	PrivateKey string

	// Endpoint is a fallback HTTP endpoint if no chain registry is available.
	Endpoint string
	// ProviderAddress is the default provider address to use.
	ProviderAddress string
	// PollInterval is how often to check for job completion.
	PollInterval time.Duration
	// PollTimeout is the maximum time to wait for a job to complete.
	PollTimeout time.Duration
}

// chatRequest is the OpenAI-compatible request format used by 0G serving.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	ID      string         `json:"id"`
	Choices []chatChoice   `json:"choices"`
	Usage   chatUsage      `json:"usage"`
	Model   string         `json:"model"`
	Error   *chatRespError `json:"error,omitempty"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
	Index   int         `json:"index"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatRespError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}
