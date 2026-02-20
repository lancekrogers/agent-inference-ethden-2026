package da

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPublish_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/da/submit" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(daResponse{
			SubmissionID: "sub-123",
			BlockHeight:  42,
			Status:       "confirmed",
		})
	}))
	defer srv.Close()

	p := NewPublisher(PublisherConfig{Endpoint: srv.URL, MaxRetries: 0})
	subID, err := p.Publish(context.Background(), AuditEvent{
		Type:      EventTypeJobCompleted,
		AgentID:   "agent-1",
		JobID:     "job-100",
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subID != "sub-123" {
		t.Errorf("expected sub-123, got %s", subID)
	}
}

func TestPublish_Retry(t *testing.T) {
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempt++
		if attempt < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(daResponse{SubmissionID: "sub-retry"})
	}))
	defer srv.Close()

	p := NewPublisher(PublisherConfig{Endpoint: srv.URL, MaxRetries: 3})
	subID, err := p.Publish(context.Background(), AuditEvent{
		Type:      EventTypeResultStored,
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}
	if subID != "sub-retry" {
		t.Errorf("expected sub-retry, got %s", subID)
	}
	if attempt != 3 {
		t.Errorf("expected 3 attempts, got %d", attempt)
	}
}

func TestPublish_AllRetriesFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := NewPublisher(PublisherConfig{Endpoint: srv.URL, MaxRetries: 1})
	_, err := p.Publish(context.Background(), AuditEvent{
		Type:      EventTypeJobFailed,
		Timestamp: time.Now(),
	})
	if err == nil {
		t.Fatal("expected error after all retries fail")
	}
}

func TestPublish_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := NewPublisher(PublisherConfig{Endpoint: "http://example.com"})
	_, err := p.Publish(ctx, AuditEvent{Type: EventTypeJobSubmitted, Timestamp: time.Now()})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestPublish_NodeDown(t *testing.T) {
	p := NewPublisher(PublisherConfig{Endpoint: "http://127.0.0.1:1", MaxRetries: 0})
	_, err := p.Publish(context.Background(), AuditEvent{
		Type:      EventTypeJobSubmitted,
		Timestamp: time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for unreachable node")
	}
}

func TestVerify_Available(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/da/verify/sub-123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(daVerifyResponse{Available: true, Status: "confirmed"})
	}))
	defer srv.Close()

	p := NewPublisher(PublisherConfig{Endpoint: srv.URL})
	available, err := p.Verify(context.Background(), "sub-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !available {
		t.Error("expected available to be true")
	}
}

func TestVerify_NotAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(daVerifyResponse{Available: false, Status: "pending"})
	}))
	defer srv.Close()

	p := NewPublisher(PublisherConfig{Endpoint: srv.URL})
	available, err := p.Verify(context.Background(), "sub-pending")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if available {
		t.Error("expected available to be false")
	}
}

func TestVerify_NodeDown(t *testing.T) {
	p := NewPublisher(PublisherConfig{Endpoint: "http://127.0.0.1:1"})
	_, err := p.Verify(context.Background(), "sub-123")
	if err == nil {
		t.Fatal("expected error for unreachable node")
	}
}

func TestSerializeEvent_Deterministic(t *testing.T) {
	event := AuditEvent{
		Type:    EventTypeJobCompleted,
		AgentID: "agent-1",
		JobID:   "job-100",
		Details: map[string]string{"model": "qwen", "tokens": "50"},
	}

	data1, err := serializeEvent(event)
	if err != nil {
		t.Fatal(err)
	}

	data2, err := serializeEvent(event)
	if err != nil {
		t.Fatal(err)
	}

	if string(data1) != string(data2) {
		t.Error("serialization is not deterministic")
	}
}

func TestSerializeEvent_AllFields(t *testing.T) {
	event := AuditEvent{
		Type:       EventTypeINFTMinted,
		AgentID:    "agent-1",
		TaskID:     "task-1",
		JobID:      "job-1",
		InputHash:  "hash-in",
		OutputHash: "hash-out",
		StorageRef: "cid-123",
		INFTRef:    "token-1",
		Details:    map[string]string{"key": "value"},
		Timestamp:  time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC),
	}

	data, err := serializeEvent(event)
	if err != nil {
		t.Fatal(err)
	}

	var parsed AuditEvent
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed.Type != EventTypeINFTMinted {
		t.Errorf("expected inft_minted, got %s", parsed.Type)
	}
	if parsed.StorageRef != "cid-123" {
		t.Errorf("expected cid-123, got %s", parsed.StorageRef)
	}
	if parsed.INFTRef != "token-1" {
		t.Errorf("expected token-1, got %s", parsed.INFTRef)
	}
}
