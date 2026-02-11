#!/usr/bin/env python3
"""
Check database insert success rates by comparing state change logs
"""

import sys

print("""
To diagnose the 1-2% gap, check these in your database:

1. Check for constraint violation errors:
   SELECT COUNT(*) FROM pg_stat_database_conflicts WHERE datname = 'your_db';

2. Check recent insert rates:
   SELECT 
     schemaname,
     tablename,
     n_tup_ins as inserts,
     n_tup_del as deletes,
     n_live_tup as live_rows
   FROM pg_stat_user_tables
   WHERE tablename IN ('follow_entry', 'like_entry', 'diamond_entry', 
                        'post_association_entry', 'user_association_entry')
   ORDER BY tablename;

3. Check for failed transactions:
   SELECT 
     datname,
     xact_commit,
     xact_rollback,
     ROUND(100.0 * xact_rollback / NULLIF(xact_commit + xact_rollback, 0), 2) as rollback_pct
   FROM pg_stat_database
   WHERE datname = 'your_db';

4. Compare latest block heights directly in database:
   SELECT MAX(height) FROM block_entry;

5. Check delete operation counts (should be significant):
   SELECT n_tup_del FROM pg_stat_user_tables 
   WHERE tablename = 'follow_entry';
   
   If deletes = 0, unfollows aren't being processed!

6. Check for duplicate key violations in logs:
   grep -i "duplicate key" /var/log/postgresql/*.log

Most Likely: The gap is from NEW BLOCKS added after your repair finished.
Solution: This is EXPECTED! Run repair continuously to stay in sync.
""")
