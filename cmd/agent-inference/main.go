// Command agent-inference is the entry point for the 0G inference agent.
// It wires all dependencies and starts the agent lifecycle.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	hiero "github.com/hiero-ledger/hiero-sdk-go/v2/sdk"

	"github.com/lancekrogers/agent-coordinator-ethden-2026/pkg/daemon"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/agent"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/hcs"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/compute"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/da"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/inft"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/storage"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := agent.LoadConfig()
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Connect to 0G Chain
	chainClient, err := zerog.DialClient(ctx, cfg.INFT.ChainRPC)
	if err != nil {
		log.Error("failed to connect to 0G Chain", "error", err)
		os.Exit(1)
	}

	// Load signing key
	chainKey, err := zerog.LoadKey(cfg.INFT.PrivateKey)
	if err != nil {
		log.Error("failed to load chain private key", "error", err)
		os.Exit(1)
	}

	// Initialize all dependencies with shared chain connection
	comp := compute.NewBroker(cfg.Compute, chainClient, chainKey)
	store := storage.NewClient(cfg.Storage, chainClient, chainKey)
	mint := inft.NewMinter(cfg.INFT, chainClient, chainKey)
	aud := da.NewPublisher(cfg.DA, chainClient, chainKey)

	// Initialize HCS transport with Hedera SDK
	transport := initHCSTransport(log)
	handler := hcs.NewHandler(cfg.HCSHandler(transport))

	// Connect to daemon runtime (optional — agent works standalone if unavailable).
	daemonClient := connectDaemon(log, cfg.DaemonAddr)
	defer daemonClient.Close()

	a := agent.New(*cfg, log, daemonClient, comp, store, mint, aud, handler)

	log.Info("inference agent starting", "agent_id", cfg.AgentID)
	if err := a.Run(ctx); err != nil && err != context.Canceled {
		log.Error("agent exited with error", "error", err)
		os.Exit(1)
	}
	log.Info("inference agent stopped gracefully")
}

func initHCSTransport(log *slog.Logger) hcs.Transport {
	accountIDStr := os.Getenv("HEDERA_ACCOUNT_ID")
	privateKeyStr := os.Getenv("HEDERA_PRIVATE_KEY")

	if accountIDStr == "" || privateKeyStr == "" {
		log.Warn("HEDERA_ACCOUNT_ID or HEDERA_PRIVATE_KEY not set, HCS transport disabled")
		return &fallbackTransport{log: log}
	}

	accountID, err := hiero.AccountIDFromString(accountIDStr)
	if err != nil {
		log.Error("failed to parse HEDERA_ACCOUNT_ID", "error", err)
		return &fallbackTransport{log: log}
	}

	privateKey, err := hiero.PrivateKeyFromString(privateKeyStr)
	if err != nil {
		log.Error("failed to parse HEDERA_PRIVATE_KEY", "error", err)
		return &fallbackTransport{log: log}
	}

	hederaClient := hiero.ClientForTestnet()
	hederaClient.SetOperator(accountID, privateKey)

	log.Info("HCS transport initialized", "account_id", accountIDStr)
	return hcs.NewHCSTransport(hcs.HCSTransportConfig{Client: hederaClient})
}

// fallbackTransport is a no-op HCS transport used when Hedera credentials are unavailable.
type fallbackTransport struct {
	log *slog.Logger
}

func (f *fallbackTransport) Publish(_ context.Context, topicID string, data []byte) error {
	f.log.Debug("fallback HCS publish", "topic", topicID, "bytes", len(data))
	return nil
}

func (f *fallbackTransport) Subscribe(_ context.Context, _ string) (<-chan []byte, <-chan error) {
	return make(chan []byte), make(chan error)
}

func connectDaemon(log *slog.Logger, addr string) daemon.DaemonClient {
	daemonCfg := daemon.DefaultConfig()
	daemonCfg.Address = addr

	client, err := daemon.NewGRPCClient(context.Background(), daemonCfg)
	if err != nil {
		log.Warn("daemon connection failed, running standalone", "error", err)
		return daemon.Noop()
	}
	return client
}
