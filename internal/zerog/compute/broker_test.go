package compute

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/zgtest"
)

func newTestBroker(t *testing.T, backend *zgtest.MockBackend, httpEndpoint string) ComputeBroker {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return NewBroker(BrokerConfig{
		ChainID:                16602,
		ServingContractAddress: "0x0000000000000000000000000000000000000001",
		Endpoint:               httpEndpoint,
		PollInterval:           10 * time.Millisecond,
		PollTimeout:            1 * time.Second,
	}, backend, key)
}

// encodedServiceCount returns ABI-encoded uint256 for getServiceCount.
func encodedServiceCount(n int) []byte {
	uint256Type, _ := abi.NewType("uint256", "", nil)
	data, _ := abi.Arguments{{Type: uint256Type}}.Pack(big.NewInt(int64(n)))
	return data
}

// encodedService returns ABI-encoded outputs for getService.
func encodedService(provider common.Address, name, svcType, url, model string) []byte {
	addrType, _ := abi.NewType("address", "", nil)
	strType, _ := abi.NewType("string", "", nil)
	args := abi.Arguments{
		{Type: addrType},
		{Type: strType},
		{Type: strType},
		{Type: strType},
		{Type: strType},
	}
	data, _ := args.Pack(provider, name, svcType, url, model)
	return data
}

func TestSubmitJob_Success(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
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
			json.NewEncoder(w).Encode(resp)
		case "/api/services/list":
			type svcEntry struct {
				Provider    string `json:"providerAddress"`
				Name        string `json:"name"`
				ServiceType string `json:"serviceType"`
				URL         string `json:"url"`
				Model       string `json:"model"`
			}
			services := []svcEntry{
				{Provider: "0xabc", Name: "Test", ServiceType: "chatbot", URL: srv.URL, Model: "test-model"},
			}
			json.NewEncoder(w).Encode(services)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	backend := &zgtest.MockBackend{}
	b := newTestBroker(t, backend, srv.URL)

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

func TestSubmitJob_APIError(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/services/list":
			type svcEntry struct {
				Provider    string `json:"providerAddress"`
				Name        string `json:"name"`
				ServiceType string `json:"serviceType"`
				URL         string `json:"url"`
				Model       string `json:"model"`
			}
			services := []svcEntry{
				{Provider: "0xabc", Name: "Test", ServiceType: "chatbot", URL: srv.URL, Model: "bad-model"},
			}
			json.NewEncoder(w).Encode(services)
		default:
			resp := chatResponse{
				Error: &chatRespError{Message: "model not found", Type: "invalid_request"},
			}
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer srv.Close()

	backend := &zgtest.MockBackend{}
	b := newTestBroker(t, backend, srv.URL)

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

	backend := &zgtest.MockBackend{}
	b := newTestBroker(t, backend, "http://example.com")

	_, err := b.SubmitJob(ctx, JobRequest{ModelID: "m", Input: "x"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestGetResult_Completed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	backend := &zgtest.MockBackend{}
	b := newTestBroker(t, backend, srv.URL)

	// Submit first to populate cache
	jobID, err := b.SubmitJob(context.Background(), JobRequest{
		ModelID: "test-model",
		Input:   "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := b.GetResult(context.Background(), jobID)
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
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	backend := &zgtest.MockBackend{}
	b := newTestBroker(t, backend, "http://example.com")

	_, err := b.GetResult(ctx, "job-nonexistent")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestGetResult_Timeout(t *testing.T) {
	backend := &zgtest.MockBackend{}
	key, _ := crypto.GenerateKey()
	b := NewBroker(BrokerConfig{
		ChainID:                16602,
		ServingContractAddress: "0x0000000000000000000000000000000000000001",
		PollInterval:           10 * time.Millisecond,
		PollTimeout:            50 * time.Millisecond,
	}, backend, key)

	_, err := b.GetResult(context.Background(), "job-timeout")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestListModels_FromChain(t *testing.T) {
	provider := common.HexToAddress("0xabc")

	callCount := 0
	backend := &zgtest.MockBackend{
		CallFn: func(_ context.Context, call ethereum.CallMsg) ([]byte, error) {
			callCount++
			if callCount == 1 {
				return encodedServiceCount(2), nil
			}
			if callCount == 2 {
				return encodedService(provider, "Qwen 2.5", "chatbot", "https://p1.example.com", "qwen-2.5-7b"), nil
			}
			return encodedService(common.HexToAddress("0xdef"), "GPT-OSS", "chatbot", "https://p2.example.com", "gpt-oss-20b"), nil
		},
	}

	key, _ := crypto.GenerateKey()
	b := NewBroker(BrokerConfig{
		ChainID:                16602,
		ServingContractAddress: "0x0000000000000000000000000000000000000001",
	}, backend, key)

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
	if models[1].URL != "https://p2.example.com" {
		t.Errorf("expected p2 URL, got %s", models[1].URL)
	}
}

func TestListModels_FallbackHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type serviceEntry struct {
			Provider    string `json:"providerAddress"`
			Name        string `json:"name"`
			ServiceType string `json:"serviceType"`
			URL         string `json:"url"`
			Model       string `json:"model"`
		}
		services := []serviceEntry{
			{Provider: "0xabc", Name: "Model1", ServiceType: "chatbot", Model: "m1", URL: "https://p.example.com"},
		}
		json.NewEncoder(w).Encode(services)
	}))
	defer srv.Close()

	// Chain fails, should fall back to HTTP
	backend := &zgtest.MockBackend{
		CallFn: func(_ context.Context, _ ethereum.CallMsg) ([]byte, error) {
			return nil, ErrBrokerDown
		},
	}

	key, _ := crypto.GenerateKey()
	b := NewBroker(BrokerConfig{
		ChainID:                16602,
		ServingContractAddress: "0x0000000000000000000000000000000000000001",
		Endpoint:               srv.URL,
	}, backend, key)

	models, err := b.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].ID != "m1" {
		t.Errorf("expected m1, got %s", models[0].ID)
	}
}

func TestListModels_Empty(t *testing.T) {
	backend := &zgtest.MockBackend{
		CallFn: func(_ context.Context, _ ethereum.CallMsg) ([]byte, error) {
			return encodedServiceCount(0), nil
		},
	}

	key, _ := crypto.GenerateKey()
	b := NewBroker(BrokerConfig{
		ChainID:                16602,
		ServingContractAddress: "0x0000000000000000000000000000000000000001",
	}, backend, key)

	_, err := b.ListModels(context.Background())
	if err != ErrNoModels {
		t.Errorf("expected ErrNoModels, got %v", err)
	}
}

func TestListModels_Cached(t *testing.T) {
	callCount := 0
	backend := &zgtest.MockBackend{
		CallFn: func(_ context.Context, _ ethereum.CallMsg) ([]byte, error) {
			callCount++
			if callCount == 1 {
				return encodedServiceCount(1), nil
			}
			return encodedService(common.HexToAddress("0xabc"), "Model1", "chatbot", "https://p.example.com", "m1"), nil
		},
	}

	key, _ := crypto.GenerateKey()
	b := NewBroker(BrokerConfig{
		ChainID:                16602,
		ServingContractAddress: "0x0000000000000000000000000000000000000001",
	}, backend, key)

	models1, err := b.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models1) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models1))
	}

	// Reset call counter - second ListModels should use cache
	prevCount := callCount
	models2, err := b.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models2) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models2))
	}
	if callCount != prevCount {
		t.Errorf("expected cached result (no new calls), got %d additional calls", callCount-prevCount)
	}
}

func TestListModels_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	backend := &zgtest.MockBackend{}
	key, _ := crypto.GenerateKey()
	b := NewBroker(BrokerConfig{
		ChainID:                16602,
		ServingContractAddress: "0x0000000000000000000000000000000000000001",
	}, backend, key)

	_, err := b.ListModels(ctx)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
