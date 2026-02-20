package compute

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSubmitJob_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/services/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("unexpected model: %s", req.Model)
		}

		resp := chatResponse{
			ID: "job-123",
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: "hello"}, Index: 0},
			},
			Usage: chatUsage{TotalTokens: 10},
			Model: "test-model",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	b := NewBroker(BrokerConfig{Endpoint: srv.URL})
	jobID, err := b.SubmitJob(context.Background(), JobRequest{
		ModelID: "test-model",
		Input:   "say hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jobID != "job-123" {
		t.Errorf("expected job-123, got %s", jobID)
	}
}

func TestSubmitJob_BrokerDown(t *testing.T) {
	b := NewBroker(BrokerConfig{Endpoint: "http://127.0.0.1:1"})
	_, err := b.SubmitJob(context.Background(), JobRequest{
		ModelID: "test-model",
		Input:   "hello",
	})
	if err == nil {
		t.Fatal("expected error for unreachable broker")
	}
}

func TestSubmitJob_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := chatResponse{
			Error: &chatRespError{Message: "model not found", Type: "invalid_request"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	b := NewBroker(BrokerConfig{Endpoint: srv.URL})
	_, err := b.SubmitJob(context.Background(), JobRequest{
		ModelID: "bad-model",
		Input:   "hello",
	})
	if err == nil {
		t.Fatal("expected error for API error response")
	}
}

func TestSubmitJob_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := NewBroker(BrokerConfig{Endpoint: "http://example.com"})
	_, err := b.SubmitJob(ctx, JobRequest{ModelID: "m", Input: "x"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestGetResult_Completed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			ID: "job-456",
			Choices: []chatChoice{
				{Message: chatMessage{Role: "assistant", Content: "result data"}, Index: 0},
			},
			Usage: chatUsage{TotalTokens: 25},
			Model: "test-model",
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	b := NewBroker(BrokerConfig{
		Endpoint:     srv.URL,
		PollInterval: 10 * time.Millisecond,
		PollTimeout:  1 * time.Second,
	})

	result, err := b.GetResult(context.Background(), "job-456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != JobStatusCompleted {
		t.Errorf("expected completed, got %s", result.Status)
	}
	if result.Output != "result data" {
		t.Errorf("expected 'result data', got %q", result.Output)
	}
	if result.TokensUsed != 25 {
		t.Errorf("expected 25 tokens, got %d", result.TokensUsed)
	}
}

func TestGetResult_ContextCancelled(t *testing.T) {
	// Server that always returns pending (404)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	b := NewBroker(BrokerConfig{
		Endpoint:     srv.URL,
		PollInterval: 10 * time.Millisecond,
		PollTimeout:  5 * time.Second,
	})

	_, err := b.GetResult(ctx, "job-789")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestGetResult_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	b := NewBroker(BrokerConfig{
		Endpoint:     srv.URL,
		PollInterval: 10 * time.Millisecond,
		PollTimeout:  50 * time.Millisecond,
	})

	_, err := b.GetResult(context.Background(), "job-timeout")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestGetResult_Failed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := chatResponse{
			Error: &chatRespError{Message: "out of memory", Type: "server_error"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	b := NewBroker(BrokerConfig{
		Endpoint:     srv.URL,
		PollInterval: 10 * time.Millisecond,
		PollTimeout:  1 * time.Second,
	})

	_, err := b.GetResult(context.Background(), "job-fail")
	if err == nil {
		t.Fatal("expected error for failed job")
	}
}

func TestListModels_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/services/list" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		services := []serviceEntry{
			{Provider: "0xabc", Name: "Qwen 2.5", ServiceType: "chatbot", Model: "qwen-2.5-7b", URL: "https://provider1.example.com"},
			{Provider: "0xdef", Name: "GPT-OSS", ServiceType: "chatbot", Model: "gpt-oss-20b", URL: "https://provider2.example.com"},
		}
		json.NewEncoder(w).Encode(services)
	}))
	defer srv.Close()

	b := NewBroker(BrokerConfig{Endpoint: srv.URL})
	models, err := b.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "qwen-2.5-7b" {
		t.Errorf("expected qwen-2.5-7b, got %s", models[0].ID)
	}
	if models[1].Provider != "0xdef" {
		t.Errorf("expected 0xdef, got %s", models[1].Provider)
	}
}

func TestListModels_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]serviceEntry{})
	}))
	defer srv.Close()

	b := NewBroker(BrokerConfig{Endpoint: srv.URL})
	_, err := b.ListModels(context.Background())
	if err != ErrNoModels {
		t.Errorf("expected ErrNoModels, got %v", err)
	}
}

func TestListModels_Cached(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		services := []serviceEntry{
			{Provider: "0xabc", Name: "Model1", Model: "m1"},
		}
		json.NewEncoder(w).Encode(services)
	}))
	defer srv.Close()

	b := NewBroker(BrokerConfig{Endpoint: srv.URL})

	// First call should hit the server
	models1, err := b.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models1) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models1))
	}

	// Second call should use cache
	models2, err := b.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models2) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models2))
	}

	if callCount != 1 {
		t.Errorf("expected 1 server call (cached), got %d", callCount)
	}
}

func TestListModels_BrokerDown(t *testing.T) {
	b := NewBroker(BrokerConfig{Endpoint: "http://127.0.0.1:1"})
	_, err := b.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable broker")
	}
}

func TestListModels_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := NewBroker(BrokerConfig{Endpoint: "http://example.com"})
	_, err := b.ListModels(ctx)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
