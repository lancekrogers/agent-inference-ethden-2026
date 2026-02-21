// Package da integrates with 0G Data Availability for publishing
// inference audit trails.
//
// Uses go-ethereum to interact with the DA Entrance contract on 0G Chain.
// Testnet DA entrance: 0xE75A073dA5bb7b0eC622170Fd268f35E675a957B (Galileo)
package da

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog"
)

const daABIJSON = `[
  {
    "name": "submitOriginalData",
    "type": "function",
    "inputs": [
      {"name": "data", "type": "bytes"}
    ],
    "outputs": []
  },
  {
    "name": "DataSubmit",
    "type": "event",
    "inputs": [
      {"name": "sender", "type": "address", "indexed": true},
      {"name": "dataRoot", "type": "bytes32", "indexed": true},
      {"name": "epoch", "type": "uint256", "indexed": false},
      {"name": "quorumId", "type": "uint256", "indexed": false}
    ]
  },
  {
    "name": "isDataAvailable",
    "type": "function",
    "stateMutability": "view",
    "inputs": [
      {"name": "dataRoot", "type": "bytes32"}
    ],
    "outputs": [
      {"name": "available", "type": "bool"}
    ]
  }
]`

var daABI = mustParseABI(daABIJSON)

func mustParseABI(raw string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic("da: invalid ABI: " + err.Error())
	}
	return parsed
}

// AuditPublisher posts inference audit events to 0G Data Availability.
type AuditPublisher interface {
	Publish(ctx context.Context, event AuditEvent) (string, error)
	Verify(ctx context.Context, submissionID string) (bool, error)
}

type publisher struct {
	cfg      PublisherConfig
	backend  zerog.ChainBackend
	contract *bind.BoundContract
	key      *ecdsa.PrivateKey
}

// NewPublisher creates a new AuditPublisher using the DA Entrance contract.
func NewPublisher(cfg PublisherConfig, backend zerog.ChainBackend, key *ecdsa.PrivateKey) AuditPublisher {
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "inference-audit"
	}

	contractAddr := common.HexToAddress(cfg.DAContractAddress)
	bc := bind.NewBoundContract(contractAddr, daABI, backend, backend, backend)

	return &publisher{
		cfg:      cfg,
		backend:  backend,
		contract: bc,
		key:      key,
	}
}

func (p *publisher) Publish(ctx context.Context, event AuditEvent) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("da: context cancelled before publish: %w", err)
	}

	data, err := serializeEvent(event)
	if err != nil {
		return "", fmt.Errorf("da: serialize event %s: %w", event.Type, err)
	}

	subID, err := p.publishWithRetry(ctx, data)
	if err != nil {
		return "", fmt.Errorf("da: publish event %s: %w", event.Type, err)
	}

	return subID, nil
}

func (p *publisher) Verify(ctx context.Context, submissionID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("da: context cancelled before verify: %w", err)
	}

	dataRoot := common.HexToHash(submissionID)

	var results []interface{}
	err := p.contract.Call(&bind.CallOpts{Context: ctx}, &results, "isDataAvailable", dataRoot)
	if err != nil {
		return false, fmt.Errorf("da: verify call for %s: %w", submissionID, err)
	}

	if len(results) == 0 {
		return false, nil
	}

	available, ok := results[0].(bool)
	if !ok {
		return false, fmt.Errorf("da: unexpected verify result type")
	}

	return available, nil
}

func serializeEvent(event AuditEvent) ([]byte, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("da: serialization failed: %w", ErrSerializeFailed)
	}
	return data, nil
}

func (p *publisher) publishWithRetry(ctx context.Context, data []byte) (string, error) {
	var lastErr error
	for attempt := 0; attempt <= p.cfg.MaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("context cancelled on attempt %d: %w", attempt+1, err)
		}

		subID, err := p.submitToDA(ctx, data)
		if err == nil {
			return subID, nil
		}
		lastErr = err

		if attempt < p.cfg.MaxRetries {
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("context cancelled during backoff: %w", ctx.Err())
			case <-time.After(backoff):
			}
		}
	}
	return "", fmt.Errorf("all %d attempts failed: %w", p.cfg.MaxRetries+1, lastErr)
}

func (p *publisher) submitToDA(ctx context.Context, data []byte) (string, error) {
	opts, err := zerog.MakeTransactOpts(ctx, p.key, p.cfg.ChainID)
	if err != nil {
		return "", fmt.Errorf("create transact opts: %w", err)
	}

	tx, err := p.contract.Transact(opts, "submitOriginalData", data)
	if err != nil {
		return "", fmt.Errorf("submit tx: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, p.backend, tx)
	if err != nil {
		return "", fmt.Errorf("wait for tx %s: %w", tx.Hash().Hex(), err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return "", fmt.Errorf("tx reverted: %w", ErrSubmissionFailed)
	}

	subID, err := parseDataSubmitEvent(receipt)
	if err != nil {
		return "", err
	}

	return subID, nil
}

func parseDataSubmitEvent(receipt *types.Receipt) (string, error) {
	eventSig := daABI.Events["DataSubmit"].ID
	for _, log := range receipt.Logs {
		if len(log.Topics) >= 2 && log.Topics[0] == eventSig {
			dataRoot := log.Topics[1]
			return dataRoot.Hex(), nil
		}
	}
	return "", fmt.Errorf("da: DataSubmit event not found in receipt")
}
