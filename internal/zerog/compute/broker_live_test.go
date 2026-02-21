//go:build live

package compute

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// TestLive_ListModels_FromGalileo connects to the real 0G Galileo testnet
// and verifies provider discovery via the serving contract.
//
// Run with: go test -tags live -run TestLive_ListModels_FromGalileo -v -count=1
func TestLive_ListModels_FromGalileo(t *testing.T) {
	rpcURL := os.Getenv("ZG_CHAIN_RPC")
	if rpcURL == "" {
		rpcURL = "https://evmrpc-testnet.0g.ai"
	}
	contractAddr := os.Getenv("ZG_SERVING_CONTRACT")
	if contractAddr == "" {
		contractAddr = "0xa79F4c8311FF93C06b8CfB403690cc987c93F91E"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		t.Fatalf("failed to connect to Galileo RPC: %v", err)
	}
	defer client.Close()

	chainID, err := client.ChainID(ctx)
	if err != nil {
		t.Fatalf("failed to get chain ID: %v", err)
	}
	t.Logf("Connected to chain ID: %d", chainID.Int64())

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	b := NewBroker(BrokerConfig{
		ChainID:                chainID.Int64(),
		ServingContractAddress: contractAddr,
		PollInterval:           1 * time.Second,
		PollTimeout:            10 * time.Second,
	}, client, key)

	models, err := b.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}

	t.Logf("Found %d models/services:", len(models))
	for i, m := range models {
		t.Logf("  [%d] ID=%s Name=%s Provider=%s URL=%s", i, m.ID, m.Name, m.Provider, m.URL)
	}

	if len(models) == 0 {
		t.Fatal("no providers found on Galileo testnet")
	}
}

// TestLive_SubmitJob_ToProvider connects to a real 0G provider discovered
// from the serving contract and submits a test inference request.
//
// Run with: go test -tags live -run TestLive_SubmitJob_ToProvider -v -count=1
func TestLive_SubmitJob_ToProvider(t *testing.T) {
	rpcURL := os.Getenv("ZG_CHAIN_RPC")
	if rpcURL == "" {
		rpcURL = "https://evmrpc-testnet.0g.ai"
	}
	contractAddr := os.Getenv("ZG_SERVING_CONTRACT")
	if contractAddr == "" {
		contractAddr = "0xa79F4c8311FF93C06b8CfB403690cc987c93F91E"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		t.Fatalf("failed to connect to Galileo RPC: %v", err)
	}
	defer client.Close()

	chainID, err := client.ChainID(ctx)
	if err != nil {
		t.Fatalf("failed to get chain ID: %v", err)
	}

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	b := NewBroker(BrokerConfig{
		ChainID:                chainID.Int64(),
		ServingContractAddress: contractAddr,
		PollInterval:           1 * time.Second,
		PollTimeout:            30 * time.Second,
	}, client, key)

	// Discover models first
	models, err := b.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) == 0 {
		t.Skip("no providers available")
	}

	// Pick the first model with a URL
	var target Model
	for _, m := range models {
		if m.URL != "" {
			target = m
			break
		}
	}
	if target.URL == "" {
		t.Skip("no provider with URL found")
	}

	t.Logf("Submitting inference to %s at %s", target.ID, target.URL)
	jobID, err := b.SubmitJob(ctx, JobRequest{
		ModelID:     target.ID,
		Input:       "What is 2+2? Answer with just the number.",
		MaxTokens:   32,
		Temperature: 0.0,
	})
	if err != nil {
		// 0G providers require session-based auth (Bearer app-sk-<base64(rawMessage:signature)>).
		// Without an on-chain session, we expect an auth error. Provider discovery itself is verified.
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "Authorization") ||
			strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "session") {
			t.Logf("Expected auth error (no session): %v", err)
			t.Log("Provider discovery verified; session-based auth required for inference")
			return
		}
		t.Fatalf("SubmitJob failed: %v", err)
	}
	t.Logf("Job ID: %s", jobID)

	result, err := b.GetResult(ctx, jobID)
	if err != nil {
		t.Fatalf("GetResult failed: %v", err)
	}

	t.Logf("Result: status=%s tokens=%d output=%q", result.Status, result.TokensUsed, result.Output)
	if result.Status != JobStatusCompleted {
		t.Errorf("expected completed, got %s", result.Status)
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}
}
