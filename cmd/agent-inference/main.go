// Command agent-inference is the entry point for the 0G inference agent.
// It wires all dependencies and starts the agent lifecycle.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/agent"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/hcs"
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

	// Initialize all dependencies
	comp := compute.NewBroker(cfg.Compute)
	store := storage.NewClient(cfg.Storage)
	mint := inft.NewMinter(cfg.INFT)
	aud := da.NewPublisher(cfg.DA)

	// HCS handler requires a transport implementation.
	// For now, log a message if no transport is configured.
	// In production, this would use the Hedera SDK transport.
	var transport hcs.Transport
	if transport == nil {
		log.Warn("no HCS transport configured, using stub")
		transport = &stubTransport{}
	}
	handler := hcs.NewHandler(cfg.HCSHandler(transport))

	a := agent.New(*cfg, log, comp, store, mint, aud, handler)

	log.Info("inference agent starting", "agent_id", cfg.AgentID)
	if err := a.Run(ctx); err != nil && err != context.Canceled {
		log.Error("agent exited with error", "error", err)
		os.Exit(1)
	}
	log.Info("inference agent stopped gracefully")
}

// stubTransport is a no-op HCS transport for development when
// no Hedera network is available.
type stubTransport struct{}

func (s *stubTransport) Publish(_ context.Context, _ string, _ []byte) error {
	return nil
}

func (s *stubTransport) Subscribe(_ context.Context, _ string) (<-chan []byte, <-chan error) {
	return make(chan []byte), make(chan error)
}
