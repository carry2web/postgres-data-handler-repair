# Reprocess Gaps with Full Transaction Data

## Problem Overview

You have TWO types of gaps:

1. **Large repaired ranges** (2.4M + 183K blocks): Blocks exist in DB but transactions NOT processed
   - Blocks: 8606270-11011962 (2.4M)
   - Blocks: 22892229-23075586 (183K)
   - These exist in state-change files WITH full transaction data
   - Repair tool only inserted blocks, not the transactions

2. **State-change file gaps** (~9K blocks): Blocks missing from state-change files entirely
   - Must be fetched from API

## Solutions

### For Large Repaired Ranges (Use State-Change Files)

```bash
# Process 2.4M range - skip blocks (already in DB), process transactions only
USE_STATE_CHANGES=true \
SKIP_BLOCKS=true \
REPAIR_START_HEIGHT=8606270 \
REPAIR_END_HEIGHT=11011962 \
go run cmd/repair/repair.go

# Process 183K range
USE_STATE_CHANGES=true \
SKIP_BLOCKS=true \
REPAIR_START_HEIGHT=22892229 \
REPAIR_END_HEIGHT=23075586 \
go run cmd/repair/repair.go
```

This will:
- Read from state-change files (has all transaction data)
- Skip `EncoderTypeBlock` entries (already in DB)
- Process follows, likes, diamonds, posts, etc.

### For State-Change File Gaps (Use API)

```bash
# Process ~9K gaps that don't exist in state-change files
GAP_FILE=state-changes-gaps.txt \
go run cmd/repair/repair.go
```

### Test with Small Sample First

```bash
# Test state-change processing on small range
USE_STATE_CHANGES=true \
SKIP_BLOCKS=true \
REPAIR_START_HEIGHT=8606270 \
REPAIR_END_HEIGHT=8606370 \
go run cmd/repair/repair.go
```

## Verification: How to Measure Impact

**IMPORTANT:** Before running repair on large ranges, verify it works on small sample!

### Method 1: Quick Python Script (Recommended)

```bash
# 1. Install dependency
pip install psycopg2-binary

# 2. Database credentials will be read from data-handler.env automatically

# 3. Take BEFORE snapshot
python tools/measure_repair_impact.py before

# 4. Run repair on test range (100 blocks)
USE_STATE_CHANGES=true SKIP_BLOCKS=true REPAIR_START_HEIGHT=8606270 REPAIR_END_HEIGHT=8606370 go run cmd/repair/repair.go

# 5. Take AFTER snapshot
python tools/measure_repair_impact.py after

# 6. Compare and see the impact
python tools/measure_repair_impact.py compare
```

**Expected output:**
```
REPAIR IMPACT ANALYSIS
================================================================================
Table                            Before          After         Change   Change %
================================================================================
follow_entry                  21,905,589     21,907,234       +1,645     +0.01% 📈
like_entry                    45,234,123     45,235,891       +1,768     +0.00% 📈
diamond_entry                 13,583,129     13,583,892         +763     +0.01% 📈
post_entry                    17,758,421     17,759,123         +702     +0.00% 📈
post_association_entry         2,334,509      2,334,987         +478     +0.02% 📈
user_association_entry           186,525        186,567          +42     +0.02% 📈
block_entry                   24,195,809     24,195,809            0           -
================================================================================
Total new entries added: +5,398
```

### Method 2: SQL Verification

```bash
# Run BEFORE repair
psql -h localhost -U postgres -d deso_data -f tools/verify_repair_impact.sql > before.txt

# Run repair
USE_STATE_CHANGES=true SKIP_BLOCKS=true REPAIR_START_HEIGHT=8606270 REPAIR_END_HEIGHT=8606370 go run cmd/repair/repair.go

# Run AFTER repair  
psql -h localhost -U postgres -d deso_data -f tools/verify_repair_impact.sql > after.txt

# Compare files
diff before.txt after.txt
```

### Method 3: Simple SQL Count

```sql
-- Before repair
SELECT COUNT(*) FROM follow_entry;  -- Note this number
SELECT COUNT(*) FROM diamond_entry; -- Note this number
SELECT COUNT(*) FROM post_entry;    -- Note this number

-- Run repair...

-- After repair (should be higher)
SELECT COUNT(*) FROM follow_entry;  -- Should increase by 1000s
SELECT COUNT(*) FROM diamond_entry; -- Should increase
SELECT COUNT(*) FROM post_entry;    -- Should increase
```

### What to Look For

✅ **Success indicators:**
- Transaction entry counts increase (follows, likes, diamonds, posts)
- Block count stays the same (blocks already exist)
- PostgreSQL pg_stat_user_tables shows new inserts
- Repair logs show "entries processed" (not just "blocks skipped")

❌ **Failure indicators:**
- No change in transaction counts
- Only "blocks skipped" in logs, no "entries processed"
- Errors about missing state-change files
- SKIP_BLOCKS flag not working (blocks being reprocessed)

### Estimating Full Range Impact

If 100 blocks adds ~5,000 transaction entries:
- 2.4M blocks = ~120 million entries
- 183K blocks = ~9 million entries
- Total: ~129 million missing transaction entries to be added

## Environment Variables

**Required:**
- `POSTGRES_URI` or `DB_HOST`, `DB_PORT`, `DB_USERNAME`, `DB_PASSWORD`
- `NODE_URL` - DeSo node API endpoint (default: `http://localhost:17001`)

**Gap Selection (pick one):**
- `GAP_FILE=state-changes-gaps.txt` - Load gaps from file (recommended)
- `REPAIR_START_HEIGHT` + `REPAIR_END_HEIGHT` - Manual range
- Neither - Auto-detect gaps from database

**Options:**
- `REPAIR_WORKERS` - Number of parallel workers (default: 100)

## Expected Output

```
2026/02/11 20:00:00 Loaded 9037 gap(s) from file: state-changes-gaps.txt
2026/02/11 20:00:00   Gap 1: 24195810 -> 24195811 (2 blocks)
...
2026/02/11 20:00:00 Processing gap: 24195810 -> 24195811 (2 blocks)
2026/02/11 20:00:01 Fetched block 24195810 with 42 transactions
2026/02/11 20:00:01 Successfully processed block 24195810 via API (42 transactions)
2026/02/11 20:00:02 Fetched block 24195811 with 38 transactions
2026/02/11 20:00:02 Successfully processed block 24195811 via API (38 transactions)
2026/02/11 20:00:02 Successfully repaired gap 24195810 -> 24195811
...
2026/02/11 20:15:23 Repair completed successfully
```

## What Gets Processed

The repair tool now properly processes:
- ✓ Block metadata (header, hash, timestamp, height)
- ✓ **ALL transactions** in `block.Txns[]`
  - Posts
  - Profiles
  - Follows
  - Likes
  - Diamonds
  - NFTs
  - Messages
  - Associations
  - All other transaction types

## How It Works

1. **Fetch from API**: `fetchBlockByHeight()` gets the full block with all transactions
2. **Create State Change**: `EncoderTypeBlock` + `OperationType=Upsert`
3. **Route to Handler**: `pdh.HandleEntryBatch()` → `BlockBatchOperation()`
4. **Process Everything**: `bulkInsertBlockEntry()` extracts and processes all transactions

From [entries/block.go](entries/block.go#L164-L193):
```go
for jj, transaction := range block.Txns {
    // Extract each transaction from the block
    pgTransactionEntry, err := TransactionEncoderToPGStruct(...)
    // Process follows, likes, posts, diamonds, etc.
}
```

## Verify Results

After reprocessing, check transaction counts:

```sql
-- Check blocks exist
SELECT COUNT(*) FROM block WHERE height BETWEEN 24195810 AND 24195979;

-- Check transactions were extracted
SELECT COUNT(*) FROM transaction_partitioned WHERE block_height BETWEEN 24195810 AND 24195979;

-- Check specific transaction types
SELECT COUNT(*) FROM post WHERE block_height BETWEEN 24195810 AND 24195979;
SELECT COUNT(*) FROM follow WHERE block_height BETWEEN 24195810 AND 24195979;
```

Or use the GraphQL comparison tool:

```powershell
.\.venv\Scripts\python.exe tools\quick_sync_status.py
```

Your gaps should be significantly reduced!

## Performance

- **Small gaps (≤100 blocks)**: Sequential processing
- **Large gaps (>100 blocks)**: Parallel processing with 100 workers
- **Speed**: ~5-10 blocks/second (depending on transaction density)
- **Time estimate**:
  - 100 blocks: 10-20 seconds
  - 1,000 blocks: 2-3 minutes
  - 9,037 blocks: 15-30 minutes

## Why This Works

**The Problem:**
- State-change files had gaps (read/write errors during sync)
- Those blocks were never written to disk
- Only the API has this data

**The Solution:**
- Fetch blocks from API (only source available)
- Use `OperationType=Upsert` to trigger full processing
- `bulkInsertBlockEntry` extracts ALL transactions from the block
- Processes posts, follows, likes, diamonds, etc. from transaction data

This closes the 1-2% data gap!
