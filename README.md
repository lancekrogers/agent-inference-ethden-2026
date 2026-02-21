# agent-inference

AI inference routing agent for the 0G decentralized compute ecosystem.

Part of the [ETHDenver 2026 Agent Economy](../README.md) submission.

## Overview

Routes AI inference jobs through a multi-stage pipeline: receives task assignments from the coordinator via Hedera Consensus Service (HCS), dispatches GPU compute to 0G Compute via REST API, persists results to 0G Storage, mints encrypted ERC-7857 iNFTs on 0G Chain via go-ethereum, and publishes audit events to 0G Data Availability (DA). Reports task completion back to the coordinator via HCS.

## Built with Obedience Corp

This project is part of an [Obedience Corp](https://obediencecorp.com) campaign — built and planned using **camp** (campaign management) and **fest** (festival methodology). This repository, its git history, and the planning artifacts in `festivals/` are a live example of these tools in action.

The agent connects to the **obey daemon** for task coordination and event routing via `OBEY_DAEMON_SOCKET`.

## System Context

```
                    ┌─────────────┐
           tasks    │ Coordinator │    tasks
          ┌────────>│  (Hedera)   │<────────┐
          │         └─────────────┘         │
          │               │                 │
          │          assignments             │
          │               │                 │
    ┌─────┴─────┐         │         ┌───────┴──────┐
    │ Inference │         │         │  DeFi Agent  │
    │   (0G)    │ <-------┘         │   (Base)     │
    └───────────┘                   └──────────────┘
     ^ you are here
```

## Pipeline

```
Task Assignment (HCS) -> 0G Compute (GPU) -> 0G Storage -> iNFT Mint (0G Chain) -> 0G DA (Audit) -> Result Report (HCS)
```

## Quick Start

```bash
cp .env.example .env   # fill in Hedera + daemon values
just build
just run
```

## Prerequisites

- Go 1.24+
- Hedera testnet account ([portal.hedera.com](https://portal.hedera.com))
- 0G testnet access (Compute, Storage, Chain, DA endpoints)

## Configuration

| Variable | Description |
|----------|-------------|
| `HEDERA_ACCOUNT_ID` | Hedera testnet account (0.0.xxx) |
| `HEDERA_PRIVATE_KEY` | Hedera private key |
| `OBEY_DAEMON_SOCKET` | Path to obey daemon Unix socket |

0G configuration (compute endpoint, storage endpoint, chain RPC, DA endpoint, signing key) is loaded via the agent config -- see `internal/agent/config.go` for the full list.

## Project Structure

```
cmd/agent-inference/       Entry point, dependency wiring
internal/
  agent/                   Agent lifecycle, config, pipeline orchestration
  hcs/                     HCS publish/subscribe transport (Hiero SDK)
  daemon/                  Daemon client
  zerog/
    compute/               0G Compute broker (GPU jobs via OpenAI-compatible REST API)
    storage/               0G Storage client (data persistence)
    inft/                  ERC-7857 iNFT minter (encrypted metadata on 0G Chain)
    da/                    0G Data Availability publisher (audit trail)
    zgtest/                0G test mocks
```

## Development

```bash
just build      # Build binary to bin/
just run        # Run the agent
just test       # Run tests
just lint       # golangci-lint
just fmt        # gofmt
just clean      # Remove build artifacts
```

## Architecture

`main.go` connects to 0G Chain, loads the signing key, then injects the compute broker, storage client, iNFT minter, DA publisher, and HCS handler into the agent. The agent subscribes to HCS for task assignments and processes each through the full pipeline. Health heartbeats with uptime metrics are broadcast periodically. Graceful shutdown on SIGINT/SIGTERM with context cancellation propagated through all stages.

## License

Apache-2.0
