# Minimal Repair Tool Docker Compose

This is a standalone compose file for running only the repair tool.

## Prerequisites

1. Your `data-handler.env` file with database and state-change directory settings
2. Access to state-change files (read-only mount)
3. Database must already be running and accessible

## Configuration

Edit `repair-compose.yml` and update the volume path to point to your state-change directory:

```yaml
volumes:
  - /your/actual/path/to/state-changes:/ss/state-changes:ro
```

## Build

```bash
docker compose -f repair-compose.yml build
```

## Run

```bash
# Run once (container exits when complete)
docker compose -f repair-compose.yml up

# Run in foreground with logs
docker compose -f repair-compose.yml up repair

# Run and remove container after
docker compose -f repair-compose.yml run --rm repair
```

## Environment Variables

All configuration is loaded from `data-handler.env`:
- `DB_HOST`
- `DB_PORT`
- `DB_NAME`
- `DB_USERNAME`
- `DB_PASSWORD`
- `STATE_CHANGE_DIR` (should be `/ss/state-changes` to match the volume mount)
- `IS_TESTNET`
- `REGTEST` (if applicable)
- `LOG_QUERIES` (optional)

## Example data-handler.env

```bash
DB_HOST=your-db-host
DB_PORT=5432
DB_NAME=postgres
DB_USERNAME=postgres
DB_PASSWORD=your-password
STATE_CHANGE_DIR=/ss/state-changes
IS_TESTNET=false
LOG_QUERIES=false
```
