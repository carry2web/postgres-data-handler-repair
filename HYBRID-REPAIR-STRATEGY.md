# Hybrid Repair Strategy

## Overview

The repair tool now uses a hybrid strategy for filling gaps in the PostgreSQL database:

1. **Small gaps (≤100 blocks)**: Sequential API calls
2. **Medium gaps (101-10,000 blocks)**: Parallel API calls with 50 workers
3. **Large gaps (>10,000 blocks)**: Direct state-consumer file reading (10-100x faster)

## Why Hybrid?

### API Approach (Small/Medium Gaps)
- **Pros**: Works for any block, no filesystem dependencies
- **Cons**: HTTP overhead, JSON parsing, slower for bulk operations
- **Use case**: Ideal for scattered missing blocks or small gaps

### State-Consumer File Reading (Large Gaps)
- **Pros**: 
  - 10-100x faster than API for bulk operations
  - Blocks already fully validated by node
  - All BLS fields pre-populated
  - Direct disk I/O, no network overhead
  - Hash is provided in KeyBytes (no computation needed)
- **Cons**: Requires read access to state-change files
- **Use case**: Perfect for large continuous gaps (e.g., 2.4M blocks)

## How It Works

### State-Consumer File Structure

The DeSo node maintains two files:

1. **state-changes.index** (`/db/state-changes.index`)
   - Array of uint64 byte offsets
   - Position for height H: `H × 8 bytes`
   - Value: Byte position in data file

2. **state-changes.bin** (`/db/state-changes.bin`)
   - Binary StateChangeEntry objects
   - Format: `[uvarint length][entry bytes]`
   - Each entry contains fully populated block

### Reading Process

```
1. Read index file at position (height × 8) → uint64 byte position
2. Seek data file to byte position
3. Read uvarint for entry length
4. Read entry bytes
5. Decode with lib.DecodeEntry()
6. Process via HandleEntryBatch()
```

## Performance Comparison

For a 2.4M block gap (8606270 → 11011962):

- **Sequential API**: ~480 hours (20 days) at 1 block/sec
- **Parallel API (50 workers)**: ~13 hours at 50 blocks/sec
- **State-consumer**: **~40 minutes** at 1000 blocks/sec (estimated)

## Configuration

### Environment Variables

```yaml
NODE_URL: http://backend:17001           # For API calls
STATE_CHANGE_DIR: /db                    # Where state-change files are located
```

### Docker Volume

```yaml
volumes:
  - /opt/volumes/backend:/db:ro          # Read-only mount of state-change files
```

## Implementation Details

### New Functions

1. **openStateChangeFiles(stateChangeDir)**: Opens index and data files
2. **readBlockFromStateChange(indexFile, dataFile, height)**: Reads specific block
3. **processGapFromStateChange(stateChangeDir, start, end, pdh)**: Processes large gap

### Gap Processing Logic

```go
if blockCount <= 100 {
    // Sequential API
    for each block: processBlockFromAPI()
} else if blockCount <= 10000 {
    // Parallel API (50 workers)
    processGapParallel()
} else {
    // State-consumer file reading
    processGapFromStateChange()
}
```

## Example Output

```
Found 3 gap(s)
Gap: 63011 -> 63011 (1 blocks)
Gap: 192368 -> 192368 (1 blocks)
Gap: 8606270 -> 11011962 (2405693 blocks)

Processing gap: 63011 -> 63011 (1 blocks)
Using sequential API processing for small gap...
Successfully repaired gap 63011 -> 63011

Processing gap: 192368 -> 192368 (1 blocks)
Using sequential API processing for small gap...
Successfully repaired gap 192368 -> 192368

Processing gap: 8606270 -> 11011962 (2405693 blocks)
Using state-consumer file reading for large gap (10-100x faster)...
Opening state-change files from /db
Processing blocks 8606270 -> 11011962 from state-change files
Progress: 1000/2405693 blocks (0.04%)
Progress: 2000/2405693 blocks (0.08%)
...
Successfully processed 2405693 blocks from state-change files
Successfully repaired gap 8606270 -> 11011962
```

## Advantages Over Computing Hashes

1. **No BLS signature field requirements**: API-provided blocks lacked necessary BLS fields for hash computation
2. **No PoW/PoS differences**: State-consumer entries handle both seamlessly
3. **Pre-validated**: Node has already validated these blocks
4. **Hash included**: KeyBytes contains the block hash (no computation needed)
5. **Faster**: Direct binary read vs HTTP + JSON + decode + compute

## Testing on Hetzner

To deploy and test:

```bash
# On Hetzner server
cd postgres-data-handler
git pull
docker compose -f repair-compose.yml build
docker compose -f repair-compose.yml up

# Monitor progress
docker compose -f repair-compose.yml logs -f repair
```

## References

- State-consumer implementation: `../state-consumer/consumer/consumer.go:663`
- Index reading pattern: `retrieveFileIndexForDbOperation()`
- Main data-handler: Also uses KeyBytes from StateChangeEntry (never computes hashes)
