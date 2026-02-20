package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpload_SmallFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var req uploadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		decoded, _ := base64.StdEncoding.DecodeString(req.Data)
		if string(decoded) != "hello world" {
			t.Errorf("unexpected data: %s", string(decoded))
		}
		if req.Name != "test.txt" {
			t.Errorf("unexpected name: %s", req.Name)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(uploadResponse{
			ContentID: "cid-abc123",
			Size:      int64(len(decoded)),
		})
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{Endpoint: srv.URL})
	contentID, err := c.Upload(context.Background(), []byte("hello world"), Metadata{
		Name: "test.txt",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if contentID != "cid-abc123" {
		t.Errorf("expected cid-abc123, got %s", contentID)
	}
}

func TestUpload_LargeFile(t *testing.T) {
	chunkCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req uploadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		chunkCount++
		if req.TotalChunks != 3 {
			t.Errorf("expected 3 total chunks, got %d", req.TotalChunks)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(uploadResponse{
			ContentID: "cid-large",
		})
	}))
	defer srv.Close()

	// Create data that requires 3 chunks at 10 bytes each
	data := make([]byte, 25)
	for i := range data {
		data[i] = byte('a' + i%26)
	}

	c := NewClient(ClientConfig{
		Endpoint:         srv.URL,
		DefaultChunkSize: 10,
	})
	contentID, err := c.Upload(context.Background(), data, Metadata{Name: "big.bin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if contentID != "cid-large" {
		t.Errorf("expected cid-large, got %s", contentID)
	}
	if chunkCount != 3 {
		t.Errorf("expected 3 chunks uploaded, got %d", chunkCount)
	}
}

func TestUpload_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := NewClient(ClientConfig{Endpoint: "http://example.com"})
	_, err := c.Upload(ctx, []byte("data"), Metadata{Name: "test"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestUpload_NodeDown(t *testing.T) {
	c := NewClient(ClientConfig{Endpoint: "http://127.0.0.1:1"})
	_, err := c.Upload(context.Background(), []byte("data"), Metadata{Name: "test"})
	if err == nil {
		t.Fatal("expected error for unreachable node")
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

	c := NewClient(ClientConfig{Endpoint: srv.URL})
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

	c := NewClient(ClientConfig{Endpoint: srv.URL})
	_, err := c.Download(context.Background(), "cid-missing")
	if err == nil {
		t.Fatal("expected error for missing content")
	}
}

func TestDownload_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := NewClient(ClientConfig{Endpoint: "http://example.com"})
	_, err := c.Download(ctx, "cid-123")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestList_WithResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("prefix") != "inference/" {
			t.Errorf("unexpected prefix: %s", r.URL.Query().Get("prefix"))
		}
		resp := listResponse{
			Items: []Metadata{
				{ContentID: "cid-1", Name: "inference/result-1", Size: 100},
				{ContentID: "cid-2", Name: "inference/result-2", Size: 200},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{Endpoint: srv.URL})
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
		json.NewEncoder(w).Encode(listResponse{Items: []Metadata{}})
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{Endpoint: srv.URL})
	items, err := c.List(context.Background(), "empty/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestList_NodeDown(t *testing.T) {
	c := NewClient(ClientConfig{Endpoint: "http://127.0.0.1:1"})
	_, err := c.List(context.Background(), "test/")
	if err == nil {
		t.Fatal("expected error for unreachable node")
	}
}
