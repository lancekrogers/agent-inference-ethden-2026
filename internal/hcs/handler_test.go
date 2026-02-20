package hcs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// mockTransport implements Transport for testing.
type mockTransport struct {
	publishErr error
	published  [][]byte
	messages   chan []byte
	subErr     chan error
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		published: make([][]byte, 0),
		messages:  make(chan []byte, 16),
		subErr:    make(chan error, 1),
	}
}

func (m *mockTransport) Publish(_ context.Context, _ string, data []byte) error {
	if m.publishErr != nil {
		return m.publishErr
	}
	m.published = append(m.published, data)
	return nil
}

func (m *mockTransport) Subscribe(_ context.Context, _ string) (<-chan []byte, <-chan error) {
	return m.messages, m.subErr
}

func TestEnvelope_RoundTrip(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{"key": "value"})
	env := Envelope{
		Type:        MessageTypeTaskAssignment,
		Sender:      "coordinator",
		Recipient:   "agent-1",
		TaskID:      "task-100",
		SequenceNum: 42,
		Timestamp:   time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC),
		Payload:     payload,
	}

	data, err := env.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := UnmarshalEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}

	if parsed.Type != MessageTypeTaskAssignment {
		t.Errorf("expected task_assignment, got %s", parsed.Type)
	}
	if parsed.Sender != "coordinator" {
		t.Errorf("expected coordinator, got %s", parsed.Sender)
	}
	if parsed.SequenceNum != 42 {
		t.Errorf("expected 42, got %d", parsed.SequenceNum)
	}
}

func TestTaskAssignment_RoundTrip(t *testing.T) {
	task := TaskAssignment{
		TaskID:  "task-1",
		ModelID: "qwen-2.5-7b",
		Input:   "test prompt",
		Priority: 5,
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}

	var parsed TaskAssignment
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed.TaskID != "task-1" {
		t.Errorf("expected task-1, got %s", parsed.TaskID)
	}
	if parsed.ModelID != "qwen-2.5-7b" {
		t.Errorf("expected qwen-2.5-7b, got %s", parsed.ModelID)
	}
}

func TestTaskResult_RoundTrip(t *testing.T) {
	result := TaskResult{
		TaskID:            "task-1",
		Status:            "completed",
		Output:            "inference result",
		DurationMs:        1500,
		StorageContentID:  "cid-123",
		INFTTokenID:       "token-456",
		AuditSubmissionID: "sub-789",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	var parsed TaskResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed.DurationMs != 1500 {
		t.Errorf("expected 1500, got %d", parsed.DurationMs)
	}
	if parsed.INFTTokenID != "token-456" {
		t.Errorf("expected token-456, got %s", parsed.INFTTokenID)
	}
}

func TestHealthStatus_RoundTrip(t *testing.T) {
	health := HealthStatus{
		AgentID:        "agent-1",
		Status:         "idle",
		UptimeSeconds:  3600,
		CompletedTasks: 10,
		FailedTasks:    1,
	}

	data, err := json.Marshal(health)
	if err != nil {
		t.Fatal(err)
	}

	var parsed HealthStatus
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed.CompletedTasks != 10 {
		t.Errorf("expected 10, got %d", parsed.CompletedTasks)
	}
}

func TestStartSubscription_ReceivesTask(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:   mt,
		TaskTopicID: "topic-1",
		AgentID:     "agent-1",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go h.StartSubscription(ctx)

	// Send a task assignment message
	payload, _ := json.Marshal(TaskAssignment{
		TaskID:  "task-100",
		ModelID: "qwen",
		Input:   "test",
	})
	env := Envelope{
		Type:    MessageTypeTaskAssignment,
		Sender:  "coordinator",
		Payload: payload,
	}
	data, _ := env.Marshal()
	mt.messages <- data

	select {
	case task := <-h.Tasks():
		if task.TaskID != "task-100" {
			t.Errorf("expected task-100, got %s", task.TaskID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for task")
	}
}

func TestStartSubscription_InvalidMessage(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:   mt,
		TaskTopicID: "topic-1",
		AgentID:     "agent-1",
	})

	ctx, cancel := context.WithCancel(context.Background())

	go h.StartSubscription(ctx)

	// Send invalid message
	mt.messages <- []byte("not json")

	// Send valid task after invalid
	payload, _ := json.Marshal(TaskAssignment{TaskID: "task-200"})
	env := Envelope{
		Type:    MessageTypeTaskAssignment,
		Sender:  "coordinator",
		Payload: payload,
	}
	data, _ := env.Marshal()
	mt.messages <- data

	select {
	case task := <-h.Tasks():
		if task.TaskID != "task-200" {
			t.Errorf("expected task-200, got %s", task.TaskID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout: valid task should have been received after invalid one")
	}

	cancel()
}

func TestStartSubscription_ContextCancelled(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:   mt,
		TaskTopicID: "topic-1",
		AgentID:     "agent-1",
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- h.StartSubscription(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for subscription to stop")
	}
}

func TestPublishResult_Success(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "agent-1",
	})

	err := h.PublishResult(context.Background(), TaskResult{
		TaskID: "task-1",
		Status: "completed",
		Output: "result data",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mt.published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(mt.published))
	}

	var env Envelope
	json.Unmarshal(mt.published[0], &env)
	if env.Type != MessageTypeTaskResult {
		t.Errorf("expected task_result, got %s", env.Type)
	}
	if env.Sender != "agent-1" {
		t.Errorf("expected agent-1, got %s", env.Sender)
	}
}

func TestPublishResult_Failed(t *testing.T) {
	mt := newMockTransport()
	mt.publishErr = errors.New("network error")
	h := NewHandler(HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "agent-1",
	})

	err := h.PublishResult(context.Background(), TaskResult{TaskID: "task-1"})
	if err == nil {
		t.Fatal("expected error for failed publish")
	}
}

func TestPublishHealth_Success(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "agent-1",
	})

	err := h.PublishHealth(context.Background(), HealthStatus{
		AgentID:        "agent-1",
		Status:         "idle",
		CompletedTasks: 5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mt.published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(mt.published))
	}

	var env Envelope
	json.Unmarshal(mt.published[0], &env)
	if env.Type != MessageTypeHeartbeat {
		t.Errorf("expected heartbeat, got %s", env.Type)
	}
}

func TestPublishResult_SequenceIncrement(t *testing.T) {
	mt := newMockTransport()
	h := NewHandler(HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "agent-1",
	})

	h.PublishResult(context.Background(), TaskResult{TaskID: "t1"})
	h.PublishResult(context.Background(), TaskResult{TaskID: "t2"})
	h.PublishHealth(context.Background(), HealthStatus{AgentID: "agent-1"})

	if len(mt.published) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(mt.published))
	}

	seqs := make([]uint64, 3)
	for i, data := range mt.published {
		var env Envelope
		json.Unmarshal(data, &env)
		seqs[i] = env.SequenceNum
	}

	if seqs[0] >= seqs[1] || seqs[1] >= seqs[2] {
		t.Errorf("sequence numbers should be monotonically increasing: %v", seqs)
	}
}
