package da

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/zgtest"
)

func daReceipt() *types.Receipt {
	eventSig := daABI.Events["DataSubmit"].ID
	dataRoot := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	return &types.Receipt{
		Status: types.ReceiptStatusSuccessful,
		Logs: []*types.Log{
			{
				Topics: []common.Hash{
					eventSig,
					common.BytesToHash(common.Address{}.Bytes()), // sender
					dataRoot,                                     // dataRoot
				},
				Data: common.LeftPadBytes(big.NewInt(1).Bytes(), 64), // epoch + quorumId
			},
		},
	}
}

func TestPublish_Success(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	backend := &zgtest.MockBackend{
		ReceiptFn: func(_ context.Context, _ common.Hash) (*types.Receipt, error) {
			return daReceipt(), nil
		},
	}

	p := NewPublisher(PublisherConfig{
		ChainID:           16602,
		DAContractAddress: "0xE75A073dA5bb7b0eC622170Fd268f35E675a957B",
		MaxRetries:        0,
	}, backend, key)

	subID, err := p.Publish(context.Background(), AuditEvent{
		Type:      EventTypeJobCompleted,
		AgentID:   "agent-1",
		JobID:     "job-100",
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subID == "" {
		t.Error("expected non-empty submission ID")
	}
}

func TestPublish_Retry(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	attempt := 0
	backend := &zgtest.MockBackend{
		SendTxFn: func(_ context.Context, _ *types.Transaction) error {
			attempt++
			if attempt < 3 {
				return errors.New("temporary failure")
			}
			return nil
		},
		ReceiptFn: func(_ context.Context, _ common.Hash) (*types.Receipt, error) {
			return daReceipt(), nil
		},
	}

	p := NewPublisher(PublisherConfig{
		ChainID:           16602,
		DAContractAddress: "0xE75A073dA5bb7b0eC622170Fd268f35E675a957B",
		MaxRetries:        3,
	}, backend, key)

	subID, err := p.Publish(context.Background(), AuditEvent{
		Type:      EventTypeResultStored,
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}
	if subID == "" {
		t.Error("expected non-empty submission ID")
	}
}

func TestPublish_AllRetriesFail(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	backend := &zgtest.MockBackend{
		SendTxFn: func(_ context.Context, _ *types.Transaction) error {
			return errors.New("persistent failure")
		},
	}

	p := NewPublisher(PublisherConfig{
		ChainID:           16602,
		DAContractAddress: "0xtest",
		MaxRetries:        1,
	}, backend, key)

	_, err = p.Publish(context.Background(), AuditEvent{
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

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	backend := &zgtest.MockBackend{}
	p := NewPublisher(PublisherConfig{
		ChainID:           16602,
		DAContractAddress: "0xtest",
	}, backend, key)

	_, err = p.Publish(ctx, AuditEvent{Type: EventTypeJobSubmitted, Timestamp: time.Now()})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestPublish_ChainDown(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	backend := &zgtest.MockBackend{
		Err: ErrDANodeUnreachable,
	}

	p := NewPublisher(PublisherConfig{
		ChainID:           16602,
		DAContractAddress: "0xtest",
		MaxRetries:        0,
	}, backend, key)

	_, err = p.Publish(context.Background(), AuditEvent{
		Type:      EventTypeJobSubmitted,
		Timestamp: time.Now(),
	})
	if err == nil {
		t.Fatal("expected error for unreachable chain")
	}
}

func TestVerify_Available(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	// ABI-encode a bool true response
	boolType, _ := abi.NewType("bool", "", nil)
	encoded, _ := abi.Arguments{{Type: boolType}}.Pack(true)

	backend := &zgtest.MockBackend{
		CallFn: func(_ context.Context, _ ethereum.CallMsg) ([]byte, error) {
			return encoded, nil
		},
	}

	p := NewPublisher(PublisherConfig{
		ChainID:           16602,
		DAContractAddress: "0xtest",
	}, backend, key)

	available, err := p.Verify(context.Background(), "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !available {
		t.Error("expected available to be true")
	}
}

func TestVerify_NotAvailable(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	boolType, _ := abi.NewType("bool", "", nil)
	encoded, _ := abi.Arguments{{Type: boolType}}.Pack(false)

	backend := &zgtest.MockBackend{
		CallFn: func(_ context.Context, _ ethereum.CallMsg) ([]byte, error) {
			return encoded, nil
		},
	}

	p := NewPublisher(PublisherConfig{
		ChainID:           16602,
		DAContractAddress: "0xtest",
	}, backend, key)

	available, err := p.Verify(context.Background(), "0xdeadbeef")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if available {
		t.Error("expected available to be false")
	}
}

func TestVerify_ChainDown(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	backend := &zgtest.MockBackend{
		Err: ErrDANodeUnreachable,
	}

	p := NewPublisher(PublisherConfig{
		ChainID:           16602,
		DAContractAddress: "0xtest",
	}, backend, key)

	_, err = p.Verify(context.Background(), "0xtest")
	if err == nil {
		t.Fatal("expected error for unreachable chain")
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
}
