# agent-inference

Inference agent — routes inference to 0G Compute (REST API), stores results on 0G Storage, maintains ERC-7857 iNFT on 0G Chain via go-ethereum, uses 0G DA for audit trail. Receives tasks from coordinator via HCS.

## Build

```bash
just build   # Build binary to bin/
just run     # Run the agent
just test    # Run tests
```

## Structure

- `cmd/` — Entry point
- `internal/` — Private packages
- `justfile` — Build recipes

## Development

- Follow Go conventions from root CLAUDE.md
- Always pass context.Context as first parameter for I/O
- Use the project's error framework, not fmt.Errorf
- Keep files under 500 lines, functions under 50 lines
