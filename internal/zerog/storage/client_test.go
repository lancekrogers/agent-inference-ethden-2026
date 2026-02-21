package storage

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/zgtest"
)

func testSetup(t *testing.T) (*zgtest.MockBackend, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	backend := &zgtest.MockBackend{
		ReceiptFn: func(_ context.Context, txHash common.Hash) (*types.Receipt, error) {
			return &types.Receipt{
				Status: types.ReceiptStatusSuccessful,
				TxHash: txHash,
				Logs:   []*types.Log{},
			}, nil
		},
	}
	return backend, key
}

func TestUpload_Success(t *testing.T) {
	backend, key := testSetup(t)

	// Storage node accepts upload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{
		ChainID:             16602,
		FlowContractAddress: "0x22E03a6A89B950F1c82ec5e74F8eCa321a105296",
		StorageNodeEndpoint: srv.URL,
	}, backend, key)

	data := []byte("hello world")
	contentID, err := c.Upload(context.Background(), data, Metadata{Name: "test.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Content ID should be hex of SHA-256 hash
	expected := sha256.Sum256(data)
	expectedHex := common.Bytes2Hex(expected[:])
	if contentID != expectedHex {
		t.Errorf("expected content ID %s, got %s", expectedHex, contentID)
	}
}

func TestUpload_ChainOnly(t *testing.T) {
	backend, key := testSetup(t)

	// No storage node endpoint - chain-only mode
	c := NewClient(ClientConfig{
		ChainID:             16602,
		FlowContractAddress: "0x22E03a6A89B950F1c82ec5e74F8eCa321a105296",
	}, backend, key)

	contentID, err := c.Upload(context.Background(), []byte("test data"), Metadata{Name: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if contentID == "" {
		t.Error("expected non-empty content ID")
	}
}

func TestUpload_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	backend, key := testSetup(t)
	c := NewClient(ClientConfig{
		ChainID:             16602,
		FlowContractAddress: "0xtest",
	}, backend, key)

	_, err := c.Upload(ctx, []byte("data"), Metadata{Name: "test"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestUpload_ChainError(t *testing.T) {
	backend, key := testSetup(t)
	backend.Err = ErrNodeDown

	c := NewClient(ClientConfig{
		ChainID:             16602,
		FlowContractAddress: "0xtest",
	}, backend, key)

	_, err := c.Upload(context.Background(), []byte("data"), Metadata{Name: "test"})
	if err == nil {
		t.Fatal("expected error for chain failure")
	}
}

func TestDownload_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/storage/cid-123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Write([]byte("stored data"))
	}))
	defer srv.Close()

	backend, key := testSetup(t)
	c := NewClient(ClientConfig{
		StorageNodeEndpoint: srv.URL,
	}, backend, key)

	data, err := c.Download(context.Background(), "cid-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "stored data" {
		t.Errorf("expected 'stored data', got %q", string(data))
	}
}

func TestDownload_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	backend, key := testSetup(t)
	c := NewClient(ClientConfig{
		StorageNodeEndpoint: srv.URL,
	}, backend, key)

	_, err := c.Download(context.Background(), "cid-missing")
	if err == nil {
		t.Fatal("expected error for missing content")
	}
}

func TestDownload_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	backend, key := testSetup(t)
	c := NewClient(ClientConfig{
		StorageNodeEndpoint: "http://example.com",
	}, backend, key)

	_, err := c.Download(ctx, "cid-123")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestDownload_NoEndpoint(t *testing.T) {
	backend, key := testSetup(t)
	c := NewClient(ClientConfig{}, backend, key)

	_, err := c.Download(context.Background(), "cid-123")
	if err == nil {
		t.Fatal("expected error for missing endpoint")
	}
}

func TestList_WithResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("prefix") != "inference/" {
			t.Errorf("unexpected prefix: %s", r.URL.Query().Get("prefix"))
		}
		resp := struct {
			Items []Metadata `json:"items"`
		}{
			Items: []Metadata{
				{ContentID: "cid-1", Name: "inference/result-1", Size: 100},
				{ContentID: "cid-2", Name: "inference/result-2", Size: 200},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	backend, key := testSetup(t)
	c := NewClient(ClientConfig{
		StorageNodeEndpoint: srv.URL,
	}, backend, key)

	items, err := c.List(context.Background(), "inference/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ContentID != "cid-1" {
		t.Errorf("expected cid-1, got %s", items[0].ContentID)
	}
}

func TestList_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := struct {
			Items []Metadata `json:"items"`
		}{Items: []Metadata{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	backend, key := testSetup(t)
	c := NewClient(ClientConfig{
		StorageNodeEndpoint: srv.URL,
	}, backend, key)

	items, err := c.List(context.Background(), "empty/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestList_NoEndpoint(t *testing.T) {
	backend, key := testSetup(t)
	c := NewClient(ClientConfig{}, backend, key)

	_, err := c.List(context.Background(), "test/")
	if err == nil {
		t.Fatal("expected error for missing endpoint")
	}
}
