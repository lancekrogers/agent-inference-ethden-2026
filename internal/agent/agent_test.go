package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/hcs"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/compute"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/da"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/inft"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/storage"
)

// Mock implementations for testing

type mockCompute struct {
	submitErr error
	resultErr error
	jobID     string
	result    *compute.JobResult
}

func (m *mockCompute) SubmitJob(_ context.Context, _ compute.JobRequest) (string, error) {
	return m.jobID, m.submitErr
}
func (m *mockCompute) GetResult(_ context.Context, _ string) (*compute.JobResult, error) {
	return m.result, m.resultErr
}
func (m *mockCompute) ListModels(_ context.Context) ([]compute.Model, error) {
	return nil, nil
}

type mockStorage struct {
	uploadErr error
	contentID string
}

func (m *mockStorage) Upload(_ context.Context, _ []byte, _ storage.Metadata) (string, error) {
	return m.contentID, m.uploadErr
}
func (m *mockStorage) Download(_ context.Context, _ string) ([]byte, error) { return nil, nil }
func (m *mockStorage) List(_ context.Context, _ string) ([]storage.Metadata, error) {
	return nil, nil
}

type mockMinter struct {
	mintErr error
	tokenID string
}

func (m *mockMinter) Mint(_ context.Context, _ inft.MintRequest) (string, error) {
	return m.tokenID, m.mintErr
}
func (m *mockMinter) UpdateMetadata(_ context.Context, _ string, _ inft.EncryptedMeta) error {
	return nil
}
func (m *mockMinter) GetStatus(_ context.Context, _ string) (*inft.INFTStatus, error) {
	return nil, nil
}

type mockAudit struct {
	publishErr error
	subID      string
}

func (m *mockAudit) Publish(_ context.Context, _ da.AuditEvent) (string, error) {
	return m.subID, m.publishErr
}
func (m *mockAudit) Verify(_ context.Context, _ string) (bool, error) { return true, nil }

type mockTransport struct {
	published [][]byte
	messages  chan []byte
	subErr    chan error
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		published: make([][]byte, 0),
		messages:  make(chan []byte, 16),
		subErr:    make(chan error, 1),
	}
}
func (m *mockTransport) Publish(_ context.Context, _ string, data []byte) error {
	m.published = append(m.published, data)
	return nil
}
func (m *mockTransport) Subscribe(_ context.Context, _ string) (<-chan []byte, <-chan error) {
	return m.messages, m.subErr
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testConfig() Config {
	return Config{
		AgentID:        "test-agent",
		HealthInterval: time.Hour, // prevent health messages during tests
	}
}

func TestProcessTask_Success(t *testing.T) {
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport:     mt,
		ResultTopicID: "result-topic",
		AgentID:       "test-agent",
	})

	a := New(
		testConfig(),
		testLogger(),
		&mockCompute{jobID: "job-1", result: &compute.JobResult{
			JobID: "job-1", Status: compute.JobStatusCompleted, Output: "hello",
		}},
		&mockStorage{contentID: "cid-123"},
		&mockMinter{tokenID: "token-456"},
		&mockAudit{subID: "audit-789"},
		handler,
	)

	err := a.processTask(context.Background(), hcs.TaskAssignment{
		TaskID:  "task-100",
		ModelID: "test-model",
		Input:   "test input",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.completedTasks.Load() != 1 {
		t.Errorf("expected 1 completed task, got %d", a.completedTasks.Load())
	}
	// Verify result was published (2 audit + 1 result = at least 1 published)
	if len(mt.published) < 1 {
		t.Error("expected at least 1 published message")
	}
}

func TestProcessTask_ComputeFails(t *testing.T) {
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport: mt, ResultTopicID: "r", AgentID: "a",
	})

	a := New(
		testConfig(), testLogger(),
		&mockCompute{submitErr: errors.New("compute down")},
		&mockStorage{}, &mockMinter{}, &mockAudit{}, handler,
	)

	err := a.processTask(context.Background(), hcs.TaskAssignment{TaskID: "t1"})
	if err == nil {
		t.Fatal("expected error when compute fails")
	}
}

func TestProcessTask_StorageFails(t *testing.T) {
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport: mt, ResultTopicID: "r", AgentID: "a",
	})

	a := New(
		testConfig(), testLogger(),
		&mockCompute{jobID: "j1", result: &compute.JobResult{
			Status: compute.JobStatusCompleted, Output: "out",
		}},
		&mockStorage{uploadErr: errors.New("storage down")},
		&mockMinter{}, &mockAudit{}, handler,
	)

	err := a.processTask(context.Background(), hcs.TaskAssignment{TaskID: "t1"})
	if err == nil {
		t.Fatal("expected error when storage fails")
	}
}

func TestProcessTask_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport: mt, ResultTopicID: "r", AgentID: "a",
	})

	a := New(
		testConfig(), testLogger(),
		&mockCompute{submitErr: context.Canceled},
		&mockStorage{}, &mockMinter{}, &mockAudit{}, handler,
	)

	err := a.processTask(ctx, hcs.TaskAssignment{TaskID: "t1"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestRun_ReceivesAndProcesses(t *testing.T) {
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport:     mt,
		TaskTopicID:   "task-topic",
		ResultTopicID: "result-topic",
		AgentID:       "test-agent",
	})

	a := New(
		testConfig(), testLogger(),
		&mockCompute{jobID: "j1", result: &compute.JobResult{
			Status: compute.JobStatusCompleted, Output: "out",
		}},
		&mockStorage{contentID: "cid"},
		&mockMinter{tokenID: "tok"},
		&mockAudit{subID: "aud"},
		handler,
	)

	ctx, cancel := context.WithCancel(context.Background())

	// Send a task after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		payload, _ := json.Marshal(hcs.TaskAssignment{
			TaskID: "task-run", ModelID: "m1", Input: "hello",
		})
		env := hcs.Envelope{
			Type:    hcs.MessageTypeTaskAssignment,
			Sender:  "coordinator",
			Payload: payload,
		}
		data, _ := env.Marshal()
		mt.messages <- data
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := a.Run(ctx)
	if err != nil && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.completedTasks.Load() != 1 {
		t.Errorf("expected 1 completed task, got %d", a.completedTasks.Load())
	}
}

func TestRun_GracefulShutdown(t *testing.T) {
	mt := newMockTransport()
	handler := hcs.NewHandler(hcs.HandlerConfig{
		Transport: mt, TaskTopicID: "t", ResultTopicID: "r", AgentID: "a",
	})

	a := New(testConfig(), testLogger(),
		&mockCompute{}, &mockStorage{}, &mockMinter{}, &mockAudit{}, handler,
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- a.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for graceful shutdown")
	}
}

func TestLoadConfig_RequiredFields(t *testing.T) {
	os.Unsetenv("INFERENCE_AGENT_ID")
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error when INFERENCE_AGENT_ID is missing")
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("INFERENCE_AGENT_ID", "test-123")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AgentID != "test-123" {
		t.Errorf("expected test-123, got %s", cfg.AgentID)
	}
	if cfg.DaemonAddr != "localhost:9090" {
		t.Errorf("expected localhost:9090, got %s", cfg.DaemonAddr)
	}
	if cfg.HealthInterval != 30*time.Second {
		t.Errorf("expected 30s, got %v", cfg.HealthInterval)
	}
}
