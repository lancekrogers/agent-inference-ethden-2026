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
	// ModelID identifies which AI model to run.
	ModelID string `json:"model_id"`

	// Input is the prompt or data to send to the model.
	Input string `json:"input"`

	// MaxTokens limits the output length for text generation models.
	MaxTokens int `json:"max_tokens,omitempty"`

	// Temperature controls randomness (0.0 to 1.0).
	Temperature float64 `json:"temperature,omitempty"`

	// Metadata contains optional key-value pairs for tracking.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// JobResult contains the output of a completed inference job.
type JobResult struct {
	// JobID is the unique identifier for this job.
	JobID string `json:"job_id"`

	// Status is the current state of the job.
	Status JobStatus `json:"status"`

	// Output is the model's response.
	Output string `json:"output"`

	// ModelID identifies which model produced the result.
	ModelID string `json:"model_id"`

	// TokensUsed is the number of tokens consumed.
	TokensUsed int `json:"tokens_used"`

	// Duration is how long the inference took.
	Duration time.Duration `json:"duration"`

	// Error contains error details if the job failed.
	Error string `json:"error,omitempty"`
}

// Model describes an available AI model on the 0G compute network.
type Model struct {
	// ID is the unique identifier for this model.
	ID string `json:"id"`

	// Name is the human-readable model name.
	Name string `json:"name"`

	// Provider identifies the model provider address.
	Provider string `json:"provider"`

	// ServiceType is the type of service (e.g., "chatbot", "speech-to-text").
	ServiceType string `json:"service_type,omitempty"`

	// URL is the provider's serving endpoint.
	URL string `json:"url,omitempty"`
}

// BrokerConfig holds configuration for the 0G Compute broker connection.
type BrokerConfig struct {
	// Endpoint is the 0G Compute serving API base URL.
	// For testnet: use the 0G compute starter kit sidecar URL.
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

// chatMessage is a single message in the OpenAI chat format.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse is the OpenAI-compatible response from 0G serving.
type chatResponse struct {
	ID      string         `json:"id"`
	Choices []chatChoice   `json:"choices"`
	Usage   chatUsage      `json:"usage"`
	Model   string         `json:"model"`
	Error   *chatRespError `json:"error,omitempty"`
}

// chatChoice is a single completion choice.
type chatChoice struct {
	Message chatMessage `json:"message"`
	Index   int         `json:"index"`
}

// chatUsage tracks token consumption.
type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// chatRespError represents an API error response.
type chatRespError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// serviceEntry represents a service from the 0G service listing.
type serviceEntry struct {
	Provider    string `json:"providerAddress"`
	Name        string `json:"name"`
	ServiceType string `json:"serviceType"`
	URL         string `json:"url"`
	Model       string `json:"model"`
}
