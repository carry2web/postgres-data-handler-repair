-- Verification Script: Measure Repair Impact
-- Run BEFORE and AFTER smart reprocessing to see the difference

-- =============================================================================
-- CORE TRANSACTION COUNTS
-- =============================================================================

-- Total counts for each transaction type
SELECT 'follow_entry' as table_name, COUNT(*) as total_count FROM follow_entry
UNION ALL
SELECT 'like_entry', COUNT(*) FROM like_entry
UNION ALL
SELECT 'diamond_entry', COUNT(*) FROM diamond_entry
UNION ALL
SELECT 'post_entry', COUNT(*) FROM post_entry
UNION ALL
SELECT 'post_association_entry', COUNT(*) FROM post_association_entry
UNION ALL
SELECT 'user_association_entry', COUNT(*) FROM user_association_entry
UNION ALL
SELECT 'nft_entry', COUNT(*) FROM nft_entry
UNION ALL
SELECT 'nft_bid_entry', COUNT(*) FROM nft_bid_entry
ORDER BY table_name;

-- =============================================================================
-- TRANSACTION COUNTS BY BLOCK RANGE (if transaction table has block_height)
-- =============================================================================

-- Transactions in the test range (8606270 -> 8606370)
SELECT 
    COUNT(*) as transactions_in_range,
    MIN(block_height) as min_block,
    MAX(block_height) as max_block
FROM transaction_partitioned
WHERE block_height BETWEEN 8606270 AND 8606370;

-- =============================================================================
-- RECENT ACTIVITY (to distinguish from ongoing sync)
-- =============================================================================

-- Latest entries in each table (to see if new data is being added)
SELECT 'follow_entry' as table_name, 
       COUNT(*) FILTER (WHERE created_at > NOW() - INTERVAL '1 hour') as last_hour,
       COUNT(*) FILTER (WHERE created_at > NOW() - INTERVAL '1 day') as last_day
FROM follow_entry
UNION ALL
SELECT 'like_entry',
       COUNT(*) FILTER (WHERE created_at > NOW() - INTERVAL '1 hour'),
       COUNT(*) FILTER (WHERE created_at > NOW() - INTERVAL '1 day')
FROM like_entry
UNION ALL
SELECT 'diamond_entry',
       COUNT(*) FILTER (WHERE created_at > NOW() - INTERVAL '1 hour'),
       COUNT(*) FILTER (WHERE created_at > NOW() - INTERVAL '1 day')
FROM diamond_entry
UNION ALL
SELECT 'post_entry',
       COUNT(*) FILTER (WHERE created_at > NOW() - INTERVAL '1 hour'),
       COUNT(*) FILTER (WHERE created_at > NOW() - INTERVAL '1 day')
FROM post_entry;

-- =============================================================================
-- DATABASE STATISTICS
-- =============================================================================

-- Check insert/delete statistics from PostgreSQL
SELECT 
    schemaname,
    relname as table_name,
    n_tup_ins as total_inserts,
    n_tup_upd as total_updates,
    n_tup_del as total_deletes,
    n_live_tup as live_rows,
    n_dead_tup as dead_rows,
    last_autovacuum,
    last_autoanalyze
FROM pg_stat_user_tables
WHERE relname IN (
    'follow_entry', 
    'like_entry', 
    'diamond_entry', 
    'post_entry',
    'post_association_entry',
    'user_association_entry',
    'nft_entry',
    'nft_bid_entry',
    'transaction_partitioned',
    'block_entry'
)
ORDER BY relname;

-- =============================================================================
-- USAGE INSTRUCTIONS
-- =============================================================================

/*
HOW TO USE THIS SCRIPT:

1. BEFORE REPAIR:
   Run this script and save output to 'before_repair.txt':
   
   psql -h localhost -U postgres -d deso_data -f verify_repair_impact.sql > before_repair.txt

2. RUN REPAIR:
   Execute smart reprocessing on test range:
   
   USE_STATE_CHANGES=true SKIP_BLOCKS=true REPAIR_START_HEIGHT=8606270 REPAIR_END_HEIGHT=8606370 go run cmd/repair/repair.go

3. AFTER REPAIR:
   Run this script again and save to 'after_repair.txt':
   
   psql -h localhost -U postgres -d deso_data -f verify_repair_impact.sql > after_repair.txt

4. COMPARE:
   Check the difference in counts:
   
   - follow_entry should increase (new follows found in those 100 blocks)
   - like_entry should increase (new likes found)
   - diamond_entry should increase (new diamonds found)
   - post_entry should increase (new posts found)
   - post_association_entry should increase
   - user_association_entry should increase
   
   The 'total_inserts' from pg_stat_user_tables will also increase.

5. EXPECTED RESULTS:
   For 100 blocks, you should see:
   - Hundreds to thousands of new transaction entries
   - No change in block_entry count (blocks already exist)
   - Increase in pg_stat_user_tables.n_tup_ins for each transaction table

6. IF NO CHANGE:
   - Check repair tool logs for errors
   - Verify blocks 8606270-8606370 exist in state-change files
   - Verify those blocks had transactions (check DeSo block explorer)
   - Check if SKIP_BLOCKS flag is working correctly
*/
