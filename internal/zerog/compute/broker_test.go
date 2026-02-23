package compute

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
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

type serviceTestData struct {
	Provider common.Address
	Name     string
	URL      string
	Model    string
}

// encodedAllServices returns ABI-encoded outputs for getAllServices.
// Matches the 11-field Service struct from the 0G InferenceServing contract.
func encodedAllServices(services []serviceTestData, total int) []byte {
	tupleType, _ := abi.NewType("tuple[]", "", []abi.ArgumentMarshaling{
		{Name: "provider", Type: "address"},
		{Name: "name", Type: "string"},
		{Name: "url", Type: "string"},
		{Name: "inputPrice", Type: "uint256"},
		{Name: "outputPrice", Type: "uint256"},
		{Name: "updatedAt", Type: "uint256"},
		{Name: "model", Type: "string"},
		{Name: "verifiability", Type: "string"},
		{Name: "content", Type: "string"},
		{Name: "signer", Type: "address"},
		{Name: "occupied", Type: "bool"},
	})
	uint256Type, _ := abi.NewType("uint256", "", nil)

	type svcStruct struct {
		Provider      common.Address
		Name          string
		Url           string
		InputPrice    *big.Int
		OutputPrice   *big.Int
		UpdatedAt     *big.Int
		Model         string
		Verifiability string
		Content       string
		Signer        common.Address
		Occupied      bool
	}

	svcs := make([]svcStruct, len(services))
	for i, s := range services {
		svcs[i] = svcStruct{
			Provider:      s.Provider,
			Name:          s.Name,
			Url:           s.URL,
			InputPrice:    big.NewInt(0),
			OutputPrice:   big.NewInt(0),
			UpdatedAt:     big.NewInt(0),
			Model:         s.Model,
			Verifiability: "none",
			Content:       "",
			Signer:        common.Address{},
			Occupied:      true,
		}
	}

	args := abi.Arguments{
		{Type: tupleType},
		{Type: uint256Type},
	}
	data, _ := args.Pack(svcs, big.NewInt(int64(total)))
	return data
}

func TestSubmitJob_Success(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/proxy/chat/completions":
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

	backend := &zgtest.MockBackend{
		CallFn: func(_ context.Context, call ethereum.CallMsg) ([]byte, error) {
			return encodedAllServices([]serviceTestData{
				{Provider: provider, Name: "Qwen 2.5", URL: "https://p1.example.com", Model: "qwen-2.5-7b"},
				{Provider: common.HexToAddress("0xdef"), Name: "GPT-OSS", URL: "https://p2.example.com", Model: "gpt-oss-20b"},
			}, 2), nil
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
			return encodedAllServices(nil, 0), nil
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
			return encodedAllServices([]serviceTestData{
				{Provider: common.HexToAddress("0xabc"), Name: "Model1", URL: "https://p.example.com", Model: "m1"},
			}, 1), nil
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

func TestSubmitJob_AuthHeader(t *testing.T) {
	var gotAuth string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/proxy/chat/completions":
			gotAuth = r.Header.Get("Authorization")
			resp := chatResponse{
				ID:      "job-auth",
				Choices: []chatChoice{{Message: chatMessage{Role: "assistant", Content: "ok"}}},
				Model:   "test-model",
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
			json.NewEncoder(w).Encode([]svcEntry{
				{Provider: "0xabc", Name: "Test", URL: srv.URL, Model: "test-model"},
			})
		}
	}))
	defer srv.Close()

	backend := &zgtest.MockBackend{}
	b := newTestBroker(t, backend, srv.URL)

	_, err := b.SubmitJob(context.Background(), JobRequest{ModelID: "test-model", Input: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth == "" {
		t.Fatal("expected Authorization header to be set")
	}
	if !strings.HasPrefix(gotAuth, "Bearer app-sk-") {
		t.Errorf("unexpected auth format: %s", gotAuth)
	}
}

func TestSubmitJob_RetryOn401(t *testing.T) {
	calls := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/proxy/chat/completions":
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			resp := chatResponse{
				ID:      "job-retry",
				Choices: []chatChoice{{Message: chatMessage{Role: "assistant", Content: "ok"}}},
				Model:   "test-model",
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
			json.NewEncoder(w).Encode([]svcEntry{
				{Provider: "0xabc", Name: "Test", URL: srv.URL, Model: "test-model"},
			})
		}
	}))
	defer srv.Close()

	backend := &zgtest.MockBackend{}
	b := newTestBroker(t, backend, srv.URL)

	jobID, err := b.SubmitJob(context.Background(), JobRequest{ModelID: "test-model", Input: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jobID != "job-retry" {
		t.Errorf("expected job-retry, got %s", jobID)
	}
	if calls != 2 {
		t.Errorf("expected 2 HTTP calls (initial + retry), got %d", calls)
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
