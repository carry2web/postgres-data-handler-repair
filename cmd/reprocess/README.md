# Reprocess Blocks Tool

Reprocesses specific block heights from state-change files to extract and process ALL state changes (transactions) within those blocks.

## Problem

The repair tool only processed block metadata, not the transactions within the blocks. This tool fixes that by reprocessing blocks from state-change files to get all follows, likes, diamonds, posts, etc.

## Usage

### For the two gap blocks you processed:

```bash
# Reprocess block 24468175
REPROCESS_START_HEIGHT=24468175 \
REPROCESS_END_HEIGHT=24468175 \
DELETE_EXISTING_BLOCKS=true \
go run cmd/reprocess/reprocess.go

# Reprocess block 24485724
REPROCESS_START_HEIGHT=24485724 \
REPROCESS_END_HEIGHT=24485724 \
DELETE_EXISTING_BLOCKS=true \
go run cmd/reprocess/reprocess.go

# Or reprocess both in one run:
REPROCESS_START_HEIGHT=24468175 \
REPROCESS_END_HEIGHT=24485724 \
DELETE_EXISTING_BLOCKS=true \
go run cmd/reprocess/reprocess.go
```

### Or using Docker:

```bash
docker-compose run --rm postgres-data-handler \
  /app/reprocess \
  --reprocess-start-height=24468175 \
  --reprocess-end-height=24468175 \
  --delete-existing-blocks=true
```

## Configuration

Required environment variables:
- `POSTGRES_URI` - Database connection string
- `STATE_CHANGE_DIR` - Path to state-change files (default: /db)
- `REPROCESS_START_HEIGHT` - First block height to reprocess
- `REPROCESS_END_HEIGHT` - Last block height to reprocess (defaults to START if not specified)

Optional:
- `DELETE_EXISTING_BLOCKS=true` - Delete and reprocess (recommended)
- `DELETE_EXISTING_BLOCKS=false` - Process over existing data (may cause issues)

## What It Does

1. **Deletes existing block data** (if DELETE_EXISTING_BLOCKS=true)
2. **Scans state-change files** for entries matching the block height range
3. **Processes ALL state changes** including:
   - Block metadata
   - Posts
   - Profiles
   - Follows/Unfollows
   - Likes/Unlikes
   - Diamonds
   - NFTs
   - Messages
   - Associations
   - All other transaction types
4. **Commits in batches** (every 10,000 entries)
5. **Verifies blocks exist** after processing

## Expected Output

```
2026/02/11 19:50:00 Connected to database
2026/02/11 19:50:00 State-change directory: /db
2026/02/11 19:50:00 Reprocessing blocks 24468175 -> 24468175 (1 blocks)
2026/02/11 19:50:00 Deleting existing blocks 24468175 -> 24468175 from database...
2026/02/11 19:50:00 Deleted 1 block records
2026/02/11 19:50:00 Opened state-change files
2026/02/11 19:50:00 Scanning state-change files for blocks in range...
2026/02/11 19:50:01 Processed block 24468175 (1247 state changes processed so far)
2026/02/11 19:50:01 Reprocessing complete!
2026/02/11 19:50:01   Blocks processed: 1
2026/02/11 19:50:01   Total state changes processed: 1247
2026/02/11 19:50:01   Entries skipped (outside range): 98734562
2026/02/11 19:50:01   Total entries scanned: 98735809
2026/02/11 19:50:01 ✓ Block 24468175 present in database
2026/02/11 19:50:01 Done!
```

## After Reprocessing

Run the comparison tool again to verify data is now complete:

```bash
.\.venv\Scripts\python.exe tools\quick_sync_status.py
```

The gaps should be significantly smaller after reprocessing.

## Building

```bash
# Add to Makefile
reprocess:
	go build -o build/reprocess cmd/reprocess/reprocess.go

# Build
make reprocess

# Run
./build/reprocess
```

## Important Notes

1. **Requires state-change files** - This won't work with API-only blocks
2. **Will scan entire state-change file** - Can take time for large files
3. **Use DELETE_EXISTING_BLOCKS=true** - Ensures clean reprocessing
4. **Processes all transaction types** - Unlike the repair tool which only processed blocks
5. **Safe to run multiple times** - With DELETE_EXISTING_BLOCKS=true, it's idempotent

## Why This Matters

The repair tool inserted:
- ✓ Block metadata (~1KB per block)
- ✗ Transactions (follows, likes, etc.) - MISSING!

This tool inserts:
- ✓ Block metadata
- ✓ **ALL transactions and state changes** (~100KB+ per block)

This should close the 1-2% data gap you're seeing!
