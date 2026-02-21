// Package zgtest provides test helpers for zerog package testing.
package zgtest

import (
	"context"
	"math/big"
	"sync"
	"sync/atomic"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// MockBackend implements zerog.ChainBackend for testing.
// Configure the exported function fields to control test behavior.
type MockBackend struct {
	mu    sync.Mutex
	nonce atomic.Uint64

	// CallFn handles eth_call requests. Set to return ABI-encoded results.
	CallFn func(ctx context.Context, call ethereum.CallMsg) ([]byte, error)

	// SendTxFn intercepts sent transactions. Nil = accept silently.
	SendTxFn func(ctx context.Context, tx *types.Transaction) error

	// ReceiptFn returns a transaction receipt. Nil = return default success receipt.
	ReceiptFn func(ctx context.Context, txHash common.Hash) (*types.Receipt, error)

	// Err sets a global error returned by all methods.
	Err error
}

func (m *MockBackend) CodeAt(_ context.Context, _ common.Address, _ *big.Int) ([]byte, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return []byte{0x01}, nil
}

func (m *MockBackend) CallContract(ctx context.Context, call ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	if m.CallFn != nil {
		return m.CallFn(ctx, call)
	}
	return nil, nil
}

func (m *MockBackend) HeaderByNumber(_ context.Context, _ *big.Int) (*types.Header, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return &types.Header{
		Number:  big.NewInt(1),
		BaseFee: big.NewInt(1e9),
	}, nil
}

func (m *MockBackend) PendingCodeAt(_ context.Context, _ common.Address) ([]byte, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return []byte{0x01}, nil
}

func (m *MockBackend) PendingNonceAt(_ context.Context, _ common.Address) (uint64, error) {
	if m.Err != nil {
		return 0, m.Err
	}
	return m.nonce.Add(1) - 1, nil
}

func (m *MockBackend) SuggestGasPrice(_ context.Context) (*big.Int, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return big.NewInt(1e9), nil
}

func (m *MockBackend) SuggestGasTipCap(_ context.Context) (*big.Int, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return big.NewInt(1e8), nil
}

func (m *MockBackend) EstimateGas(_ context.Context, _ ethereum.CallMsg) (uint64, error) {
	if m.Err != nil {
		return 0, m.Err
	}
	return 100000, nil
}

func (m *MockBackend) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	if m.Err != nil {
		return m.Err
	}
	if m.SendTxFn != nil {
		return m.SendTxFn(ctx, tx)
	}
	return nil
}

func (m *MockBackend) FilterLogs(_ context.Context, _ ethereum.FilterQuery) ([]types.Log, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return nil, nil
}

func (m *MockBackend) SubscribeFilterLogs(_ context.Context, _ ethereum.FilterQuery, _ chan<- types.Log) (ethereum.Subscription, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return &stubSub{}, nil
}

func (m *MockBackend) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	if m.ReceiptFn != nil {
		return m.ReceiptFn(ctx, txHash)
	}
	return &types.Receipt{
		Status: types.ReceiptStatusSuccessful,
		TxHash: txHash,
		Logs:   []*types.Log{},
	}, nil
}

type stubSub struct{}

func (s *stubSub) Unsubscribe()      {}
func (s *stubSub) Err() <-chan error  { return make(chan error) }
