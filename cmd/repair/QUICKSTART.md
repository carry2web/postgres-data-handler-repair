# Quick Start Guide - Repair Tool

## Running the Repair Tool

The repair tool has been integrated as a Docker service that runs alongside your existing data handler.

### Using Make (Recommended)

The simplest way to run the repair tool:

```bash
# Run repair once
make repair

# Run repair with rebuild
make repair-build

# Run repair locally (without Docker)
make repair-local
```

### Using Docker Compose Directly

```bash
# Run repair once
docker compose -f local.docker-compose.yml --profile repair up repair

# Run with rebuild
docker compose -f local.docker-compose.yml --profile repair up --build repair

# Run in background
docker compose -f local.docker-compose.yml --profile repair up -d repair

# View logs
docker compose -f local.docker-compose.yml --profile repair logs -f repair
```

## What Gets Created

1. **Dockerfile.repair** - Specialized Dockerfile that builds the repair binary
2. **repair service** in docker-compose - Configured with profile to prevent auto-start
3. **Makefile commands** - Easy shortcuts for common operations

## Key Features

- **Profile-based**: Uses Docker Compose profiles so it doesn't start automatically
- **Shared environment**: Uses the same `.env` and volumes as the main data handler
- **On-demand**: Only runs when you explicitly invoke it
- **No interference**: Won't conflict with the running data handler

## Example Workflow

1. Start your normal services:
   ```bash
   make dev-env
   ```

2. If you detect missing blocks, run repair:
   ```bash
   make repair
   ```

3. The repair tool will:
   - Detect gaps in block table
   - Process state-change files for missing blocks
   - Exit when complete

4. Continue using your data handler normally

## Configuration

The repair service shares all environment variables with `pdh`:
- Same database connection
- Same `STATE_CHANGE_DIR` (shares the volume)
- Same network settings
- Same testnet/mainnet configuration

No additional configuration needed!

## Troubleshooting

If the repair tool can't find state-change files, verify:
```bash
# Check if volumes are mounted correctly
docker compose -f local.docker-compose.yml --profile repair config

# Check if state-change files exist
docker compose -f local.docker-compose.yml exec deso ls -la /ss/state-changes/
```

For more details, see [cmd/repair/README.md](README.md)
