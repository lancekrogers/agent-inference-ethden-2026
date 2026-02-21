package inft

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/zgtest"
)

func testKey(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	encKey := make([]byte, 32)
	if _, err := rand.Read(encKey); err != nil {
		t.Fatal(err)
	}
	return key, encKey
}

func mintReceipt(toAddr common.Address, tokenID int64) *types.Receipt {
	transferSig := contractABI.Events["Transfer"].ID
	return &types.Receipt{
		Status: types.ReceiptStatusSuccessful,
		Logs: []*types.Log{
			{
				Topics: []common.Hash{
					transferSig,
					common.BytesToHash(common.Address{}.Bytes()),
					common.BytesToHash(toAddr.Bytes()),
					common.BigToHash(big.NewInt(tokenID)),
				},
			},
		},
	}
}

func TestMint_Success(t *testing.T) {
	key, encKey := testKey(t)
	addr := crypto.PubkeyToAddress(key.PublicKey)

	backend := &zgtest.MockBackend{
		ReceiptFn: func(_ context.Context, _ common.Hash) (*types.Receipt, error) {
			return mintReceipt(addr, 42), nil
		},
	}

	m := NewMinter(MinterConfig{
		ChainID:         16602,
		ContractAddress: "0x1234567890abcdef1234567890abcdef12345678",
		EncryptionKey:   encKey,
		EncryptionKeyID: "key-1",
	}, backend, key)

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
	if tokenID != "42" {
		t.Errorf("expected token ID 42, got %s", tokenID)
	}
}

func TestMint_ChainUnreachable(t *testing.T) {
	key, encKey := testKey(t)

	backend := &zgtest.MockBackend{
		Err: ErrChainUnreachable,
	}

	m := NewMinter(MinterConfig{
		ChainID:         16602,
		ContractAddress: "0x1234567890abcdef1234567890abcdef12345678",
		EncryptionKey:   encKey,
		EncryptionKeyID: "key-1",
	}, backend, key)

	_, err := m.Mint(context.Background(), MintRequest{
		Name:          "Test",
		PlaintextMeta: map[string]string{"k": "v"},
	})
	if err == nil {
		t.Fatal("expected error for unreachable chain")
	}
}

func TestMint_TxReverted(t *testing.T) {
	key, encKey := testKey(t)

	backend := &zgtest.MockBackend{
		ReceiptFn: func(_ context.Context, txHash common.Hash) (*types.Receipt, error) {
			return &types.Receipt{
				Status: types.ReceiptStatusFailed,
				TxHash: txHash,
			}, nil
		},
	}

	m := NewMinter(MinterConfig{
		ChainID:         16602,
		ContractAddress: "0x1234567890abcdef1234567890abcdef12345678",
		EncryptionKey:   encKey,
		EncryptionKeyID: "key-1",
	}, backend, key)

	_, err := m.Mint(context.Background(), MintRequest{
		Name:          "Test",
		PlaintextMeta: map[string]string{"k": "v"},
	})
	if err == nil {
		t.Fatal("expected error for reverted tx")
	}
}

func TestMint_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	key, encKey := testKey(t)
	backend := &zgtest.MockBackend{}

	m := NewMinter(MinterConfig{
		ChainID:         16602,
		ContractAddress: "0x1234567890abcdef1234567890abcdef12345678",
		EncryptionKey:   encKey,
		EncryptionKeyID: "key-1",
	}, backend, key)

	_, err := m.Mint(ctx, MintRequest{
		Name:          "Test",
		PlaintextMeta: map[string]string{"k": "v"},
	})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestUpdateMetadata_Success(t *testing.T) {
	key, _ := testKey(t)

	backend := &zgtest.MockBackend{
		ReceiptFn: func(_ context.Context, txHash common.Hash) (*types.Receipt, error) {
			return &types.Receipt{
				Status: types.ReceiptStatusSuccessful,
				TxHash: txHash,
			}, nil
		},
	}

	m := NewMinter(MinterConfig{
		ChainID:         16602,
		ContractAddress: "0x1234567890abcdef1234567890abcdef12345678",
	}, backend, key)

	err := m.UpdateMetadata(context.Background(), "1", EncryptedMeta{
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
	key, _ := testKey(t)
	testAddr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	// ABI-encode an address return value
	addrType, _ := abi.NewType("address", "", nil)
	encoded, _ := abi.Arguments{{Type: addrType}}.Pack(testAddr)

	backend := &zgtest.MockBackend{
		CallFn: func(_ context.Context, _ ethereum.CallMsg) ([]byte, error) {
			return encoded, nil
		},
	}

	m := NewMinter(MinterConfig{
		ChainID:         16602,
		ContractAddress: "0xcontract",
	}, backend, key)

	status, err := m.GetStatus(context.Background(), "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.TokenID != "1" {
		t.Errorf("expected token ID 1, got %s", status.TokenID)
	}
	if status.ChainID != 16602 {
		t.Errorf("expected chain 16602, got %d", status.ChainID)
	}
}

func TestGetStatus_TokenNotFound(t *testing.T) {
	key, _ := testKey(t)

	// Return zero address = token not found
	addrType, _ := abi.NewType("address", "", nil)
	encoded, _ := abi.Arguments{{Type: addrType}}.Pack(common.Address{})

	backend := &zgtest.MockBackend{
		CallFn: func(_ context.Context, _ ethereum.CallMsg) ([]byte, error) {
			return encoded, nil
		},
	}

	m := NewMinter(MinterConfig{
		ChainID:         16602,
		ContractAddress: "0xcontract",
	}, backend, key)

	_, err := m.GetStatus(context.Background(), "999")
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}
