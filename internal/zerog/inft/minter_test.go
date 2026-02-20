package inft

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestMint_Success(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		json.NewDecoder(r.Body).Decode(&req)

		callCount++
		switch req.Method {
		case "eth_sendTransaction":
			json.NewEncoder(w).Encode(rpcResponse{
				JSONRPC: "2.0",
				Result:  json.RawMessage(`"0xabc123"`),
				ID:      1,
			})
		case "eth_getTransactionReceipt":
			json.NewEncoder(w).Encode(rpcResponse{
				JSONRPC: "2.0",
				Result:  json.RawMessage(`{"status": "0x1"}`),
				ID:      1,
			})
		}
	}))
	defer srv.Close()

	key := testKey(t)
	m := NewMinter(MinterConfig{
		ChainRPC:        srv.URL,
		ChainID:         16602,
		ContractAddress: "0xtest",
		PrivateKey:      "0xprivkey",
		EncryptionKey:   key,
		EncryptionKeyID: "key-1",
	})

	tokenID, err := m.Mint(context.Background(), MintRequest{
		Name:           "Test iNFT",
		Description:    "Inference result",
		InferenceJobID: "job-100",
		ResultHash:     "abc123",
		PlaintextMeta:  map[string]string{"model": "test"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokenID == "" {
		t.Error("expected non-empty token ID")
	}
}

func TestMint_ChainUnreachable(t *testing.T) {
	key := testKey(t)
	m := NewMinter(MinterConfig{
		ChainRPC:        "http://127.0.0.1:1",
		ContractAddress: "0xtest",
		EncryptionKey:   key,
		EncryptionKeyID: "key-1",
	})

	_, err := m.Mint(context.Background(), MintRequest{
		Name:          "Test",
		PlaintextMeta: map[string]string{"k": "v"},
	})
	if err == nil {
		t.Fatal("expected error for unreachable chain")
	}
}

func TestMint_InsufficientGas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32000, Message: "insufficient funds"},
			ID:      1,
		})
	}))
	defer srv.Close()

	key := testKey(t)
	m := NewMinter(MinterConfig{
		ChainRPC:        srv.URL,
		ContractAddress: "0xtest",
		EncryptionKey:   key,
		EncryptionKeyID: "key-1",
	})

	_, err := m.Mint(context.Background(), MintRequest{
		Name:          "Test",
		PlaintextMeta: map[string]string{"k": "v"},
	})
	if err == nil {
		t.Fatal("expected error for insufficient gas")
	}
}

func TestMint_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	key := testKey(t)
	m := NewMinter(MinterConfig{
		ChainRPC:        "http://example.com",
		ContractAddress: "0xtest",
		EncryptionKey:   key,
		EncryptionKeyID: "key-1",
	})

	_, err := m.Mint(ctx, MintRequest{
		Name:          "Test",
		PlaintextMeta: map[string]string{"k": "v"},
	})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestUpdateMetadata_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		json.NewDecoder(r.Body).Decode(&req)

		switch req.Method {
		case "eth_sendTransaction":
			json.NewEncoder(w).Encode(rpcResponse{
				JSONRPC: "2.0",
				Result:  json.RawMessage(`"0xdef456"`),
				ID:      1,
			})
		case "eth_getTransactionReceipt":
			json.NewEncoder(w).Encode(rpcResponse{
				JSONRPC: "2.0",
				Result:  json.RawMessage(`{"status": "0x1"}`),
				ID:      1,
			})
		}
	}))
	defer srv.Close()

	m := NewMinter(MinterConfig{
		ChainRPC:        srv.URL,
		ContractAddress: "0xtest",
	})

	err := m.UpdateMetadata(context.Background(), "token-1", EncryptedMeta{
		Ciphertext: []byte("encrypted"),
		Nonce:      []byte("nonce"),
		KeyID:      "key-1",
		Algorithm:  "AES-256-GCM",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetStatus_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(rpcResponse{
			JSONRPC: "2.0",
			Result:  json.RawMessage(`"0x0000000000000000000000001234"`),
			ID:      1,
		})
	}))
	defer srv.Close()

	m := NewMinter(MinterConfig{
		ChainRPC:        srv.URL,
		ChainID:         16602,
		ContractAddress: "0xcontract",
	})

	status, err := m.GetStatus(context.Background(), "token-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.TokenID != "token-1" {
		t.Errorf("expected token-1, got %s", status.TokenID)
	}
	if status.ChainID != 16602 {
		t.Errorf("expected chain 16602, got %d", status.ChainID)
	}
}

func TestGetStatus_TokenNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(rpcResponse{
			JSONRPC: "2.0",
			Result:  json.RawMessage(`"0x"`),
			ID:      1,
		})
	}))
	defer srv.Close()

	m := NewMinter(MinterConfig{
		ChainRPC:        srv.URL,
		ContractAddress: "0xcontract",
	})

	_, err := m.GetStatus(context.Background(), "missing-token")
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}
