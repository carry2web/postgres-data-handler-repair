# State-Changes Gap Analysis

This document explains the root cause analysis of block gaps in PostgreSQL databases synced from DeSo state-changes files.

## Problem Summary

When using `postgres-data-handler` to sync blockchain data from state-changes files into PostgreSQL, you may discover gaps (missing blocks) in your database. This analysis tool helps determine whether:

1. **State-changes files are incomplete** (backend node issue)
2. **postgres-data-handler is dropping blocks** (processing bug)

## Root Cause Findings

### Analysis Result: State-Changes Has Gaps

Our investigation found that **state-changes files have the same gaps** as the PostgreSQL database, proving that:

- ✅ **postgres-data-handler works correctly** - it processes all blocks present in state-changes
- ❌ **Backend node wrote incomplete state-changes** - blocks never made it to the source files
- 🔧 **Solution required**: HTTP API repair to fetch missing blocks from the network

### Why State-Changes Has Gaps

Analysis of `core/lib/state_change_syncer.go` revealed several failure modes:

#### 1. **Silent Flush Failures** (Most Critical)

```go
if !event.Succeeded {
    glog.V(2).Infof("Deleting unflushed bytes for id: %s", flushId)
    delete(stateChangeSyncer.UnflushedCommittedBytes, flushId)
    return nil  // ← DATA PERMANENTLY LOST, NO RETRY
}
```

**Issue**: When BadgerDB flush fails, state-change entries are **permanently deleted** from memory with only a debug-level log. No retry mechanism exists.

#### 2. **Disk Write Errors**

```go
_, err := flushFile.Write(unflushedBytes.StateChangeBytes)
if err != nil {
    return fmt.Errorf("Error writing to state change file: %v", err)
}
```

**Common scenarios**:
- Disk full during blockchain sync
- I/O timeouts (slow/failing disk)
- Network-mounted storage (NFS/CIFS) disconnects
- File system corruption

#### 3. **Write Ordering Race Condition**

State-changes are written **AFTER** BadgerDB commits. If BadgerDB succeeds but state-change write fails:
- Block exists in blockchain ✅
- Block missing from state-changes ❌

#### 4. **Process Crashes**

Node crashes/restarts between block acceptance and state-change flush lose those blocks from state-changes permanently.

### Typical Gap Pattern

Large gaps (2-3 million blocks) typically occur during:
- Initial blockchain sync (highest I/O load)
- Disk space exhaustion events
- System maintenance/restarts
- Storage performance degradation

## State-Changes Gap Analyzer Tool

### Location

```
cmd/analyze_state_changes/analyze.go
```

### Features

- ✅ Scans entire state-changes.bin and index files
- ✅ Identifies all missing block heights
- ✅ Progress tracking with ETA
- ✅ Milestone logging every 1 million blocks
- ✅ Dual output: console + log file
- ✅ Separate detailed gaps file
- ✅ Handles large files (500GB+)

### Usage

```bash
cd cmd/analyze_state_changes
go run analyze.go
```

**Configuration**: Set `STATE_CHANGE_DIR` in `.env` file:

```env
STATE_CHANGE_DIR=/opt/volumes/backend/state-changes
```

### Output Files

Created in `STATE_CHANGE_DIR`:

1. **`state-changes-analysis.log`** - Complete analysis log with timestamps
2. **`state-changes-gaps-detailed.txt`** - List of all gaps (one per line)

### Sample Output

```
=== State-Changes Gap Analysis Started at 2026-02-10T01:17:08Z ===
Analyzing state-changes in: /opt/volumes/backend/state-changes
Log file: /opt/volumes/backend/state-changes/state-changes-analysis.log
Index file size: 9,499,399,136 bytes
Data file size: 513,029,483,520 bytes
Total entries in index: 1,187,424,868

Scanning entries for block heights...

Progress: 100000/1187424868 entries (0.01%) - 22043 blocks found - Elapsed: 5s - ETA: 16h32m
Progress: 200000/1187424868 entries (0.02%) - 44127 blocks found - Elapsed: 10s - ETA: 16h28m
...
Progress: 1000000000/1187424868 entries (84.22%) - 23456789 blocks found - Elapsed: 3h15m - ETA: 36m
  └─ Milestone: Found 1000000 blocks (total: 23456789)

=== Analysis Results ===
Total blocks found: 26,745,123
Min block height: 1
Max block height: 29,358,144
Expected blocks (continuous range): 29,358,144
Missing blocks: 2,613,021 (8.91%)

=== Checking for gaps ===
✗ Found 53,889 gaps (total 2,613,021 blocks missing)

  Gap 1: heights 150 -> 152 (3 blocks missing)
  Gap 2: heights 8450 -> 8451 (2 blocks missing)
  ...
  (showing first 100 and last 100 gaps)
  ... (53,689 more gaps omitted from console, see state-changes-gaps-detailed.txt) ...

Complete gap list written to: /opt/volumes/backend/state-changes/state-changes-gaps-detailed.txt

=== Last 10 blocks in state-changes ===
  Height 29358135 (entry index: 1187423974)
  Height 29358136 (entry index: 1187424066)
  ...
  Height 29358144 (entry index: 1187424786)

=== Analysis Complete ===
Total time: 3h52m
Entries scanned: 1,187,424,868
Blocks found: 26,745,123
Gaps found: 53,889
```

## Repair Strategy

### If State-Changes Has Gaps (Most Common)

**You must use the HTTP API repair tool:**

```bash
docker-compose -f repair-compose.yml up
```

This will:
1. Query PostgreSQL for missing blocks
2. Fetch blocks from DeSo node API
3. Insert into PostgreSQL via postgres-data-handler

**Timeline**: Days to weeks for millions of blocks, depending on:
- API rate limits
- `threadLimit` configuration
- Network bandwidth

### If State-Changes Is Complete (Rare)

**Build a state-changes-based repair tool:**

This would be much faster (hours instead of days) because you can:
1. Scan state-changes once for missing blocks
2. Insert directly via postgres-data-handler's entry processing
3. Avoid API rate limits entirely

## Performance Notes

### Analyzer Performance

- **Large files**: ~4 hours for 500GB state-changes + 10GB index
- **Memory usage**: ~2-3GB for 30M blocks (in-memory map)
- **Disk I/O**: Sequential reads, minimal seeks

### Optimization Tips

1. Run on SSD/NVMe for faster scan
2. Don't run while node is actively syncing (file growth)
3. Use tmux/screen for long-running scans
4. Monitor available disk space for log files

## Prevention for New Nodes

To avoid gaps in state-changes on new node deployments:

1. **Monitor disk space** during initial sync
2. **Use local SSD storage** (not network-mounted)
3. **Enable verbose logging**: `-v=2` flag to catch flush failures
4. **Set up alerts** for disk I/O errors
5. **Regular validation**: Run analyzer periodically

## Technical Details

### File Format

**state-changes.bin**: Sequential entries, each prefixed with varint length
- Entry format: `[operation][reverted][encoder_type][key][encoder][ancestral][flush_id][height][block?]`

**state-changes-index.bin**: 8-byte offsets (little-endian uint64)
- Each entry points to corresponding position in data file

### Decoder Logic

Uses `lib.DecodeFromBytes()` from DeSo core to properly decode:
- Varint encoding (Protocol Buffers style)
- Block height fields
- Encoder types (2 = Block, 3 = Transaction, etc.)

## Related Files

- **Core code**: `../core/lib/state_change_syncer.go` - State-change writer
- **Main tool**: `cmd/analyze_state_changes/analyze.go` - Gap analyzer
- **Repair tool**: `cmd/repair/repair.go` - HTTP API-based gap filler
- **Data handler**: `handler/data_handler.go` - Entry processor

## Contributing

If you find additional root causes or have improvements for the analyzer, please submit a PR.

## License

Same as postgres-data-handler main project.
