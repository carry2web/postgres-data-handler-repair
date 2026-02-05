# Postgres Data Handler - Repair Tool

This tool repairs missing blocks in your Postgres database by processing state-change files from the DeSo node filesystem.

## Overview

The repair tool detects gaps in the block table and fills them by reading state-change files directly from the node's filesystem. This approach is significantly faster and more reliable than fetching blocks via API.

## Running with Docker Compose

The repair service is configured as a **profile** in the docker-compose file, which means it doesn't start automatically with the other services. This prevents it from running continuously alongside the data handler.

### Run the Repair Tool Once

To run the repair tool once (it will detect gaps, repair them, and exit):

```bash
docker-compose -f local.docker-compose.yml --profile repair up repair
```

### Run with Build

If you've made changes to the code and need to rebuild:

```bash
docker-compose -f local.docker-compose.yml --profile repair up --build repair
```

### View Logs

To follow the repair process logs:

```bash
docker-compose -f local.docker-compose.yml --profile repair logs -f repair
```

### Run in Detached Mode

To run in the background:

```bash
docker-compose -f local.docker-compose.yml --profile repair up -d repair
```

## Running Standalone (without Docker)

You can also run the repair tool directly:

```bash
cd cmd/repair
go run repair.go
```

Make sure your `.env` file contains:
- `DB_HOST`
- `DB_PORT`
- `DB_NAME`
- `DB_USERNAME`
- `DB_PASSWORD`
- `STATE_CHANGE_DIR` - Path to the state-change files directory
- `IS_TESTNET` - Set to true for testnet, false for mainnet
- `REGTEST` - Set to true if using regtest
- `LOG_QUERIES` - Optional, set to true to see SQL queries

## How It Works

1. **Gap Detection**: Runs a SQL query to find missing block ranges in the database
2. **File Processing**: For each missing block height, reads the corresponding `state-changes-<height>` file
3. **Batch Processing**: Groups state-change entries by encoder type and processes them in batches
4. **Transaction Management**: Wraps each gap repair in a database transaction for consistency

## Requirements

- The DeSo node must be writing state-change files to `STATE_CHANGE_DIR`
- State-change files must exist for the missing block heights
- The repair tool must have read access to the state-change directory (shared volume in Docker)
- Database credentials must have write permissions

## Performance

The state-change file approach is **10-100x faster** than API-based repair because:
- No network overhead
- Reads optimized binary files
- Processes complete state changes in batches
- Uses the same proven mechanism as the main data handler

## Troubleshooting

### Missing State-Change Files

If you see warnings about missing state-change files:
```
WARNING: Failed to process state-change file for height X
Ensure the state-change file exists at: /ss/state-changes/state-changes-X
```

This means the DeSo node doesn't have the state-change file for that block height. Options:
1. Wait for the node to generate the file (if syncing)
2. Check if the node's `STATE_CHANGE_DIR` is correctly configured
3. Verify the volume mount is correct

### Database Connection Issues

Ensure the database service is healthy before running repair:
```bash
docker-compose -f local.docker-compose.yml ps db-ss
```

### Permission Issues

The repair container runs as root by default. If you have permission issues with mounted volumes, you may need to adjust volume permissions.

## Environment Variables

All environment variables are shared with the main data handler:

| Variable | Description | Required |
|----------|-------------|----------|
| `DB_HOST` | Postgres hostname | Yes |
| `DB_PORT` | Postgres port | Yes |
| `DB_NAME` | Database name | Yes |
| `DB_USERNAME` | Database user | Yes |
| `DB_PASSWORD` | Database password | Yes |
| `STATE_CHANGE_DIR` | Path to state-change files | Yes |
| `IS_TESTNET` | Testnet mode (true/false) | No |
| `REGTEST` | Regtest mode (true/false) | No |
| `LOG_QUERIES` | Log SQL queries (true/false) | No |

## Example Output

```
2026/02/05 10:30:00 Using state change directory: /ss/state-changes
2026/02/05 10:30:01 Found 2 gap(s)
2026/02/05 10:30:01 Gap: 1000 -> 1050
2026/02/05 10:30:01 Gap: 2000 -> 2005
2026/02/05 10:30:01 Processing gap: 1000 -> 1050 (51 blocks)
2026/02/05 10:30:01 Processing height 1000...
2026/02/05 10:30:01 Successfully processed 245 state-change entries for height 1000
2026/02/05 10:30:02 Processing height 1001...
2026/02/05 10:30:02 Successfully processed 189 state-change entries for height 1001
...
2026/02/05 10:30:45 Successfully repaired gap 1000 -> 1050
2026/02/05 10:30:45 Processing gap: 2000 -> 2005 (6 blocks)
...
2026/02/05 10:30:52 Successfully repaired gap 2000 -> 2005
2026/02/05 10:30:52 Repair completed successfully
```

## Docker Compose Configuration

The repair service uses a **profile** to prevent it from starting automatically:

```yaml
repair:
  build:
    context: ..
    dockerfile: postgres-data-handler/Dockerfile.repair
  environment:
    # Shares same environment as pdh service
  volumes:
    - ss_volume:/ss  # Shared state-change files
  depends_on:
    db-ss:
      condition: service_healthy
  profiles:
    - repair  # Only starts when explicitly requested
```

This design ensures:
- The repair tool doesn't interfere with the running data handler
- Both services share the same volumes and environment
- You can run repair on-demand when needed
- Clean separation of concerns
