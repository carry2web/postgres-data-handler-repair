# Block Gap Repair Tool

This repair tool detects and fills missing blocks in your PostgreSQL database by fetching them from a DeSo node API.

---

## Overview

The repair tool is designed to:
- **Detect gaps** in your block database automatically using SQL window functions
- **Fetch missing blocks** from a DeSo node API endpoint in parallel
- **Process blocks in streaming batches** for immediate progress visibility
- **Support manual range specification** for edge cases or forced re-syncs
- **Commit regularly** (every 10k blocks) for crash resilience

---

## Features

### Automatic Gap Detection
The tool queries your database to find missing block height ranges:
- Detects gaps between existing blocks
- Detects missing blocks from height 0 (if applicable)
- Processes all detected gaps sequentially

### Streaming Batch Processing
- Fetches blocks in **50,000-block batches**
- Processes and commits in **10,000-block batches**
- Shows progress every 1,000 blocks
- **First commit within ~5 minutes** instead of waiting for entire gap

### Parallel Processing
- Configurable worker count (default: 100, recommended: 200)
- Workers fetch blocks concurrently from the node API
- Automatically manages database connection pool

### Manual Range Mode
- Specify exact height range to process
- Bypasses gap detection and existence checks
- Useful for:
  - Initial sync from block 0
  - Forced re-processing of corrupted blocks
  - Re-syncing specific height ranges

---

## Quick Start

### 1. Using Docker Compose (Recommended)

**Automatic gap detection:**
```bash
docker-compose -f repair-compose.yml up
```

**Manual range specification:**
```bash
# Add to repair-compose.yml environment section:
REPAIR_START_HEIGHT=0
REPAIR_END_HEIGHT=16999999

docker-compose -f repair-compose.yml up
```

### 2. Build and Run Manually

```bash
# Build
docker build -f Dockerfile.repair -t deso-repair .

# Run with environment variables
docker run --rm \
  --network run_internal \
  -e DB_HOST=postgres \
  -e DB_PORT=5432 \
  -e DB_NAME=deso \
  -e DB_USERNAME=admin \
  -e DB_PASSWORD=password \
  -e NODE_URL=http://backend:17001 \
  -e REPAIR_WORKERS=200 \
  deso-repair
```

---

## Configuration

### Required Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DB_HOST` | PostgreSQL host | `postgres` |
| `DB_PORT` | PostgreSQL port | `5432` |
| `DB_NAME` | Database name | `deso` |
| `DB_USERNAME` | Database user | `admin` |
| `DB_PASSWORD` | Database password | (required) |
| `NODE_URL` | DeSo node API endpoint | `http://localhost:17001` |

### Optional Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `REPAIR_WORKERS` | Number of parallel workers | `100` |
| `REPAIR_START_HEIGHT` | Manual start height | (auto-detect) |
| `REPAIR_END_HEIGHT` | Manual end height | (auto-detect) |
| `LOG_QUERIES` | Enable SQL query logging | `false` |
| `IS_TESTNET` | Use testnet parameters | `false` |

---

## Usage Examples

### Example 1: Automatic Gap Detection and Repair

```bash
docker-compose -f repair-compose.yml up
```

**Output:**
```
2026/02/06 10:21:17 Found 59 gap(s)
2026/02/06 10:21:17 Gap: 8606270 -> 11011962 (2405693 blocks)
2026/02/06 10:21:17 Gap: 22892229 -> 23075586 (183358 blocks)
2026/02/06 10:21:17 Processing gap: 8606270 -> 11011962 (2405693 blocks)
2026/02/06 10:21:17 Using parallel API processing (200 workers) for gap...
2026/02/06 10:21:17 Fetching batch: heights 8606270 -> 8656269
2026/02/06 10:23:59 Processing 50000 fetched blocks...
2026/02/06 10:24:49 Progress: 1000/2405693 blocks processed
2026/02/06 10:29:23 ✓ Committed: 10000/2405693 blocks (0.42%)
```

### Example 2: Manual Range for Initial Sync

If you have blocks 17M+ but missing 0-17M:

```yaml
# repair-compose.yml
services:
  repair:
    environment:
      REPAIR_START_HEIGHT: 0
      REPAIR_END_HEIGHT: 16999999
      REPAIR_WORKERS: 200
```

### Example 3: Re-process Specific Corrupted Range

To force re-sync blocks 5M-5.1M:

```bash
REPAIR_START_HEIGHT=5000000 \
REPAIR_END_HEIGHT=5100000 \
docker-compose -f repair-compose.yml up
```

---

## Performance

### Timing Estimates

With 200 workers and good network:
- **Fetch rate**: ~200 blocks/second
- **50k batch fetch time**: ~2.5 minutes
- **Processing time**: ~1-2 minutes per 10k blocks
- **First commit**: ~5 minutes
- **Total time for 2.4M blocks**: ~18-24 hours

### Tuning

**Increase workers for faster fetching:**
```yaml
REPAIR_WORKERS: 300  # Requires more DB connections
```

**Database connections required:**
- Formula: `REPAIR_WORKERS + 20`
- Ensure PostgreSQL `max_connections` is sufficient

### Monitoring Progress

**SQL query to check progress:**
```sql
SELECT 
  COUNT(*) as filled,
  ROUND(COUNT(*) * 100.0 / 2405693, 2) as percent_complete
FROM block 
WHERE height >= 8606270 AND height <= 11011962;
```

**Look for commit logs:**
```
✓ Committed: 10000/2405693 blocks (0.42%)
✓ Committed: 20000/2405693 blocks (0.83%)
```

---

## Architecture

### Processing Modes

**Small Gaps (≤100 blocks):**
- Sequential processing
- Simple and reliable for small repairs

**Large Gaps (>100 blocks):**
- Parallel worker pool
- Streaming batch processing
- Regular commits every 10k blocks

### Batch Processing Flow

```
┌─────────────────────────────────────────┐
│  Fetch 50k blocks (parallel workers)    │
└────────────┬────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────┐
│  Process in 10k chunks                   │
│  ├─ Process 10k → Commit                 │
│  ├─ Process 10k → Commit                 │
│  ├─ Process 10k → Commit                 │
│  ├─ Process 10k → Commit                 │
│  └─ Process 10k → Commit                 │
└────────────┬────────────────────────────┘
             │
             ▼
┌─────────────────────────────────────────┐
│  Fetch next 50k blocks                   │
└─────────────────────────────────────────┘
```

### Database Operations

**UPSERT strategy:**
- Uses `ON CONFLICT` for both `block_hash` and `badger_key`
- Safe for re-processing existing blocks
- Updates rows if they already exist

**Transaction management:**
- New transaction per 10k batch
- Automatic rollback on errors
- Savepoints for atomicity

---

## Troubleshooting

### No gaps detected but blocks are missing

**Cause:** Missing blocks from height 0

**Solution:** Use manual range mode:
```bash
REPAIR_START_HEIGHT=0
REPAIR_END_HEIGHT=<your_first_block_height - 1>
```

### Connection pool exhausted

**Cause:** Too many workers for available DB connections

**Solution:** Reduce workers or increase PostgreSQL `max_connections`:
```yaml
# PostgreSQL config
max_connections = 400  # For 300 workers

# Repair config
REPAIR_WORKERS: 250
```

### API timeout errors

**Cause:** Node under heavy load or slow network

**Solution:** 
1. Reduce worker count
2. Increase timeout (edit `fetchBlockByHeight` timeout)
3. Check node health: `curl http://backend:17001/api/v0/health-check`

### Progress stops at X blocks

**Cause:** Worker goroutine crashed or deadlock

**Solution:** 
1. Check logs for ERROR messages
2. Restart repair container (already committed blocks are safe)
3. If repeated, reduce `REPAIR_WORKERS`

### Duplicate key violations

**Cause:** Multiple repair instances running or race condition

**Solution:**
1. Ensure only one repair container is running
2. Check `docker ps | grep repair`
3. UPSERT handles conflicts, so this shouldn't block progress

---

## Integration with Main Data Handler

The repair tool can run **side-by-side** with the main data handler:

```bash
# Start main handler (ongoing sync)
docker-compose -f local.docker-compose.yml up -d

# Run repair in parallel (historical gaps)
docker-compose -f repair-compose.yml up
```

**Benefits:**
- Main handler continues syncing new blocks
- Repair fills historical gaps
- Both use same database with UPSERT safety
- No conflicts due to different height ranges

---

## Development

### Building

```bash
docker build -f Dockerfile.repair -t deso-repair .
```

### Testing

```bash
# Test with small gap
REPAIR_START_HEIGHT=100000 \
REPAIR_END_HEIGHT=100100 \
go run cmd/repair/repair.go
```

### Code Structure

```
cmd/repair/
  repair.go           # Main repair logic
    - detectGaps()    # SQL-based gap detection
    - fetchBlockByHeight()  # API block fetcher
    - processGapParallel()  # Streaming batch processor
    - processBlockFromAPI() # Single block processor
```

---

## Contributing

Fork and improvements welcome:
- [GitHub Repository](https://github.com/carry2web/postgres-data-handler-repair)

**Key areas for contribution:**
- Performance optimizations
- Additional monitoring/metrics
- Resume from checkpoint on crash
- Parallel gap processing

---

## License

Same as parent project: [deso-protocol/postgres-data-handler](https://github.com/deso-protocol/postgres-data-handler)
