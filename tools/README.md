# State-Changes Gap Analysis Tool

This tool analyzes the `state-changes.bin` and `state-changes-index.bin` files to detect gaps in block data.

## Purpose

Diagnose whether gaps in your PostgreSQL block table are due to:
- Missing blocks in the state-changes file itself
- Issues with the data handler processing
- Problems with the backend node writing state-changes

## Requirements

- Python 3.6 or higher (no external dependencies needed)
- Access to the state-changes files

## Usage

### On Your Hetzner Server (Recommended)

Since your state-changes files are on the Hetzner server, SSH in and run:

```bash
# SSH to your Hetzner server
ssh your-server

# Navigate to where you have the script or copy it
cd /path/to/postgres-data-handler/tools

# Run with explicit path
python3 analyze_state_changes.py /opt/volumes/backend/state-changes

# Or with environment variable
STATE_CHANGE_DIR=/opt/volumes/backend/state-changes python3 analyze_state_changes.py
```

### Copy Script to Server

If you need to copy the script to your server:

```bash
# From your local machine
scp tools/analyze_state_changes.py your-server:/tmp/

# Then SSH and run
ssh your-server
python3 /tmp/analyze_state_changes.py /opt/volumes/backend/state-changes
```

### Via Docker Container

If running inside a Docker container with access to state-changes volume:

```bash
docker run --rm \
  -v /opt/volumes/backend:/db:ro \
  -v $(pwd)/tools:/tools \
  python:3.11-slim \
  python /tools/analyze_state_changes.py /db/state-changes
```

## What It Analyzes

1. **File Integrity**
   - Checks if index and data files exist
   - Shows file sizes

2. **Entry Scanning**
   - Counts total entries in the index
   - Identifies different entry types (blocks, transactions, etc.)
   - Extracts block heights from block entries

3. **Gap Detection**
   - Finds missing block heights in sequential ranges
   - Reports gap start/end and number of missing blocks
   - Shows percentage of missing data

4. **Recent Blocks**
   - Lists the last 20 blocks found
   - Helps verify the file is current

## Output Example

```
🔬 DeSo State-Changes Gap Analyzer
======================================================================

📂 Analyzing state-changes in: /opt/volumes/backend/state-changes

📊 Index file size: 135,450,624 bytes (0.13 GB)
📊 Data file size: 45,678,912,345 bytes (42.54 GB)
📊 Total entries in index: 16,931,328

🔍 Scanning entries for blocks...
  Progress: 5,000,000/16,931,328 (29.5%) - 1,234,567 blocks found
  ...

======================================================================
📋 ANALYSIS RESULTS
======================================================================

📦 Entry Types Found:
  Block: 1,500,000
  Transaction: 10,000,000
  Post: 2,500,000
  ...

✅ Total blocks found: 1,500,000
📍 Min block height: 0
📍 Max block height: 1,500,500
📊 Expected blocks (continuous range): 1,500,501
🔴 Missing blocks: 501

======================================================================
🔍 GAP ANALYSIS
======================================================================

⚠️  Found 3 gap(s):

  Gap #  1: heights    125,000 →    125,100 (     101 blocks missing)
  Gap #  2: heights    850,000 →    850,200 (     201 blocks missing)
  Gap #  3: heights  1,200,000 →  1,200,198 (     199 blocks missing)

  Total missing from gaps: 501 blocks

======================================================================
✨ Analysis complete!
======================================================================
```

## Interpreting Results

### No Gaps Found ✅
```
✅ No gaps found! State-changes file has complete sequential blocks.
```
- State-changes file is complete
- Issue is likely in data handler processing or PostgreSQL
- Check data handler logs for processing errors

### Gaps Found ⚠️
```
⚠️  Found 15 gap(s):
  Gap #  1: heights 100,000 → 105,000 (5,000 blocks missing)
```
- Backend node failed to write some blocks to state-changes
- Possible causes:
  - Node crashes during sync
  - Disk space issues during write
  - Node restart with incomplete flush
- **Solution**: Use the repair tool to fetch missing blocks via API

### Many Small Gaps
- Indicates intermittent issues
- Possibly node restarts or network issues during sync

### Large Continuous Gap
- Node was down for extended period
- Or state-changes file was truncated/corrupted

## Next Steps Based on Results

1. **If state-changes is complete**: 
   - Check data handler logs
   - Look for processing errors in specific height ranges
   - Check PostgreSQL for failed inserts

2. **If state-changes has gaps**:
   - Run the repair tool: `docker-compose -f repair-compose.yml up`
   - Repair tool will fetch missing blocks via node API
   - Consider investigating why node is not writing complete state-changes

3. **If many recent gaps**:
   - Check node health and logs
   - Verify disk space
   - Check if node is syncing properly

## Troubleshooting

### "File not found" error
- Verify the path to state-changes directory
- Check if backend container is running and writing files
- Ensure volume mounts are correct

### "No blocks found"
- State-changes file may be empty or corrupted
- Check if backend node is configured to write state-changes
- Verify `--state-change-dir` flag in backend node startup

### Script runs very slowly
- Large state-changes files (40GB+) take time to scan
- Progress is shown every 100k entries
- Typical scan: ~5-10 minutes for 10M+ entries

## Performance Notes

- Scans ~100k entries per second on SSD
- Memory usage: ~50-100MB regardless of file size
- CPU usage: Single core, ~50-70% utilization during scan

## Technical Details

The tool:
1. Reads the index file (8 bytes per entry, little-endian uint64 offsets)
2. For each entry, reads the data file at the specified offset
3. Parses the StateChangeEntry binary format:
   - operation_type (1 byte)
   - is_reverted (1 byte)
   - encoder_type (1 byte) - 2 = Block entry
   - Variable length key, encoder, and ancestral data
   - flush_id (16 byte UUID)
   - block_height (8 byte uint64, little-endian)
4. Tracks all block heights found
5. Identifies gaps in sequential block numbers
