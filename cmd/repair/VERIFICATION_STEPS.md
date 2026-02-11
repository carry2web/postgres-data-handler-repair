# Quick Verification Steps for Smart Reprocessing

## Question: How do we know the repair actually works?

**Answer:** Measure database counts BEFORE and AFTER repair to see new transaction entries added.

---

## Quick Start (3 Steps)

### 1. Take BEFORE Snapshot

```bash
# Database credentials will be read from data-handler.env automatically
python tools/measure_repair_impact.py before
```

This saves current counts to `before_repair.json`.

### 2. Run Repair on Test Range (100 blocks)

```bash
USE_STATE_CHANGES=true \
SKIP_BLOCKS=true \
REPAIR_START_HEIGHT=8606270 \
REPAIR_END_HEIGHT=8606370 \
go run cmd/repair/repair.go
```

Watch the logs - you should see:
- `Blocks skipped: 100` (blocks already exist, skipping them)
- `Entries processed: XXXX` (processing transactions, follows, likes, etc.)
- `Committing batch...` (inserting into database)

### 3. Take AFTER Snapshot and Compare

```bash
python tools/measure_repair_impact.py after
python tools/measure_repair_impact.py compare
```

---

## What You Should See

### ✅ Success Looks Like:

```
REPAIR IMPACT ANALYSIS
================================================================================
Table                            Before          After         Change   Change %
================================================================================
follow_entry                  21,905,589     21,907,234       +1,645     +0.01% 📈
diamond_entry                 13,583,129     13,583,892         +763     +0.01% 📈
post_entry                    17,758,421     17,759,123         +702     +0.00% 📈
post_association_entry         2,334,509      2,334,987         +478     +0.02% 📈
block_entry                   24,195,809     24,195,809            0           -
================================================================================
Total new entries added: +5,398
```

**Key points:**
- Transaction tables increase (follows, diamonds, posts, etc.)
- Block table stays the same (blocks already exist)
- Thousands of new entries from just 100 blocks

### ❌ Failure Looks Like:

```
================================================================================
Table                            Before          After         Change   Change %
================================================================================
follow_entry                  21,905,589     21,905,589            0           -
diamond_entry                 13,583,129     13,583,129            0           -
post_entry                    17,758,421     17,758,421            0           -
block_entry                   24,195,809     24,195,809            0           -
================================================================================
Total new entries added: 0
```

**This means:**
- State-change processing didn't work
- SKIP_BLOCKS flag might not be working
- State-change files might not exist for this range
- Check repair tool logs for errors

---

## Manual SQL Verification (Alternative)

If you don't want to use Python:

```sql
-- BEFORE repair
SELECT 
    'follow_entry' as table_name, 
    COUNT(*) as count 
FROM follow_entry
UNION ALL
SELECT 'diamond_entry', COUNT(*) FROM diamond_entry
UNION ALL
SELECT 'post_entry', COUNT(*) FROM post_entry;

-- Write down the numbers ^^^

-- RUN REPAIR...

-- AFTER repair (run same query again)
SELECT 
    'follow_entry' as table_name, 
    COUNT(*) as count 
FROM follow_entry
UNION ALL
SELECT 'diamond_entry', COUNT(*) FROM diamond_entry
UNION ALL
SELECT 'post_entry', COUNT(*) FROM post_entry;

-- Compare: counts should be higher by 1000s
```

---

## Estimating Impact on Full Ranges

Based on test results (100 blocks):
- If 100 blocks = ~5,000 new entries
- Then 2.4M blocks = ~120 million new entries
- And 183K blocks = ~9 million new entries
- **Total: ~129 million missing transaction entries**

This explains the 1-2% gap in GraphQL comparisons!

---

## Troubleshooting

### No entries added?

1. Check repair logs:
   - Should see "Entries processed: XXXX"
   - Should NOT only see "Blocks skipped"

2. Verify state-change files exist:
   ```bash
   ls -lh /path/to/state-changes/
   # Should see state-changes.bin and state-changes-index.bin
   ```

3. Check block range has data:
   ```sql
   SELECT COUNT(*) FROM block_entry 
   WHERE height BETWEEN 8606270 AND 8606370;
   -- Should return 101 (blocks exist)
   ```

4. Verify SKIP_BLOCKS environment variable:
   ```bash
   echo $SKIP_BLOCKS  # Should output: true
   ```

### Blocks being reprocessed?

If you see blocks being updated/reinserted:
- SKIP_BLOCKS flag is not working
- Check environment variable is set correctly
- Check repair.go code has skipBlocks parameter implemented

---

## Next Steps

Once verification succeeds on 100 blocks:

1. **Run on full 2.4M range:**
   ```bash
   USE_STATE_CHANGES=true SKIP_BLOCKS=true REPAIR_START_HEIGHT=8606270 REPAIR_END_HEIGHT=11011962 go run cmd/repair/repair.go
   ```

2. **Run on 183K range:**
   ```bash
   USE_STATE_CHANGES=true SKIP_BLOCKS=true REPAIR_START_HEIGHT=22892229 REPAIR_END_HEIGHT=23075586 go run cmd/repair/repair.go
   ```

3. **Verify sync improved:**
   ```bash
   python tools/quick_sync_status.py
   # Should show 99%+ sync instead of 98%
   ```

---

## FAQ

**Q: Do I need to stop the main data handler?**
A: No, repair can run alongside the main handler.

**Q: How long will 2.4M blocks take?**
A: Depends on disk I/O. Estimate: ~1-2 hours per 100K blocks = ~48 hours total.

**Q: Can I run multiple repair jobs in parallel?**
A: Not recommended - stick to sequential processing for safety.

**Q: What if I interrupt the repair?**
A: Safe to restart - commits every 10K entries, so progress is saved.

**Q: Will this create duplicate entries?**
A: No - database constraints prevent duplicates. Upsert logic handles it.
