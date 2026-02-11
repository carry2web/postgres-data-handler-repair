#!/usr/bin/env python3
"""
Measure the impact of repair by comparing database counts before/after
"""

import psycopg2
import json
import sys
import os
from datetime import datetime
from pathlib import Path

def load_db_config():
    """Load database configuration from data-handler.env file"""
    # Look for data-handler.env in current directory or parent
    env_file = Path('data-handler.env')
    if not env_file.exists():
        env_file = Path('../data-handler.env')
    if not env_file.exists():
        env_file = Path('../../data-handler.env')
    
    config = {
        'host': os.getenv('DB_HOST', 'localhost'),
        'port': int(os.getenv('DB_PORT', '5432')),
        'database': os.getenv('DB_NAME', 'deso_data'),
        'user': os.getenv('DB_USERNAME', 'postgres'),
        'password': os.getenv('DB_PASSWORD', 'postgres')
    }
    
    # Try to load from env file if it exists
    if env_file.exists():
        print(f"Loading database config from: {env_file}")
        with open(env_file) as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith('#') and '=' in line:
                    key, value = line.split('=', 1)
                    key = key.strip()
                    value = value.strip().strip('"').strip("'")
                    
                    if key == 'DB_HOST':
                        config['host'] = value
                    elif key == 'DB_PORT':
                        config['port'] = int(value)
                    elif key == 'DB_NAME':
                        config['database'] = value
                    elif key == 'DB_USERNAME':
                        config['user'] = value
                    elif key == 'DB_PASSWORD':
                        config['password'] = value
                    elif key == 'POSTGRES_URI':
                        # Parse postgres://user:pass@host:port/dbname
                        import re
                        match = re.match(r'postgres(?:ql)?://([^:]+):([^@]+)@([^:]+):(\d+)/(.+)', value)
                        if match:
                            config['user'] = match.group(1)
                            config['password'] = match.group(2)
                            config['host'] = match.group(3)
                            config['port'] = int(match.group(4))
                            config['database'] = match.group(5)
    else:
        print("No data-handler.env file found, using environment variables or defaults")
    
    return config

# Load database configuration
DB_CONFIG = load_db_config()

TABLES_TO_CHECK = [
    'follow_entry',
    'like_entry', 
    'diamond_entry',
    'post_entry',
    'post_association_entry',
    'user_association_entry',
    'nft_entry',
    'nft_bid_entry',
    'transaction_partitioned',
    'block'  # Note: block table doesn't have _entry suffix
]

def get_table_counts(conn):
    """Get row counts for all transaction tables"""
    counts = {}
    
    for table in TABLES_TO_CHECK:
        cursor = conn.cursor()
        try:
            cursor.execute(f"SELECT COUNT(*) FROM {table}")
            count = cursor.fetchone()[0]
            counts[table] = count
            print(f"  {table}: {count:,}")
        except Exception as e:
            # Rollback the failed transaction and continue
            conn.rollback()
            print(f"  {table}: SKIPPED - {str(e).split('LINE')[0].strip()}")
            counts[table] = None
        finally:
            cursor.close()
    
    return counts

def get_pg_stats(conn):
    """Get PostgreSQL insert/update/delete statistics"""
    cursor = conn.cursor()
    
    try:
        cursor.execute("""
            SELECT 
                relname,
                n_tup_ins,
                n_tup_upd,
                n_tup_del,
                n_live_tup
            FROM pg_stat_user_tables
            WHERE relname IN %s
            ORDER BY relname
        """, (tuple(TABLES_TO_CHECK),))
        
        stats = {}
        for row in cursor.fetchall():
            stats[row[0]] = {
                'inserts': row[1],
                'updates': row[2],
                'deletes': row[3],
                'live_rows': row[4]
            }
    except Exception as e:
        print(f"\nWarning: Could not fetch pg_stats: {e}")
        stats = {}
    finally:
        cursor.close()
    
    return stats

def save_snapshot(filename, counts, stats):
    """Save snapshot to JSON file"""
    snapshot = {
        'timestamp': datetime.now().isoformat(),
        'counts': counts,
        'pg_stats': stats
    }
    
    with open(filename, 'w') as f:
        json.dump(snapshot, f, indent=2)
    
    print(f"\n✓ Snapshot saved to: {filename}")

def compare_snapshots(before_file, after_file):
    """Compare two snapshots and show differences"""
    try:
        with open(before_file) as f:
            before = json.load(f)
        with open(after_file) as f:
            after = json.load(f)
    except FileNotFoundError as e:
        print(f"Error: {e}")
        return
    
    print(f"\n{'='*80}")
    print(f"REPAIR IMPACT ANALYSIS")
    print(f"{'='*80}\n")
    print(f"Before: {before['timestamp']}")
    print(f"After:  {after['timestamp']}\n")
    print(f"{'='*80}")
    print(f"{'Table':<30} {'Before':>15} {'After':>15} {'Change':>15} {'Change %':>12}")
    print(f"{'='*80}")
    
    total_new_entries = 0
    
    for table in TABLES_TO_CHECK:
        before_count = before['counts'].get(table, 0)
        after_count = after['counts'].get(table, 0)
        
        if before_count is None or after_count is None:
            continue
        
        change = after_count - before_count
        change_pct = (change / before_count * 100) if before_count > 0 else 0
        
        if change != 0:
            status = "📈" if change > 0 else "📉"
            print(f"{table:<30} {before_count:>15,} {after_count:>15,} {change:>+15,} {change_pct:>+11.2f}% {status}")
            total_new_entries += change
        else:
            print(f"{table:<30} {before_count:>15,} {after_count:>15,} {change:>15} {'-':>12}")
    
    print(f"{'='*80}")
    print(f"\nTotal new entries added: {total_new_entries:+,}")
    
    # Show pg_stats differences
    print(f"\n{'='*80}")
    print(f"PostgreSQL Insert Statistics")
    print(f"{'='*80}")
    print(f"{'Table':<30} {'New Inserts':>15} {'New Updates':>15} {'New Deletes':>15}")
    print(f"{'='*80}")
    
    for table in TABLES_TO_CHECK:
        if table not in before['pg_stats'] or table not in after['pg_stats']:
            continue
        
        before_stats = before['pg_stats'][table]
        after_stats = after['pg_stats'][table]
        
        insert_diff = after_stats['inserts'] - before_stats['inserts']
        update_diff = after_stats['updates'] - before_stats['updates']
        delete_diff = after_stats['deletes'] - before_stats['deletes']
        
        if insert_diff > 0 or update_diff > 0 or delete_diff > 0:
            print(f"{table:<30} {insert_diff:>+15,} {update_diff:>+15,} {delete_diff:>+15,}")
    
    print(f"{'='*80}\n")

def main():
    if len(sys.argv) < 2:
        print("""
Usage:
  1. Take BEFORE snapshot:
     python measure_repair_impact.py before
  
  2. Run repair with smart reprocessing:
     USE_STATE_CHANGES=true SKIP_BLOCKS=true REPAIR_START_HEIGHT=8606270 REPAIR_END_HEIGHT=8606370 go run cmd/repair/repair.go
  
  3. Take AFTER snapshot:
     python measure_repair_impact.py after
  
  4. Compare snapshots:
     python measure_repair_impact.py compare

Database connection will be read from data-handler.env file if present,
otherwise from environment variables or defaults.
""")
        sys.exit(1)
    
    command = sys.argv[1].lower()
    
    if command == "before":
        print("\n📸 Taking BEFORE snapshot...")
        print(f"{'='*80}\n")
        print(f"Connecting to: {DB_CONFIG['user']}@{DB_CONFIG['host']}:{DB_CONFIG['port']}/{DB_CONFIG['database']}\n")
        
        try:
            conn = psycopg2.connect(**DB_CONFIG)
            counts = get_table_counts(conn)
            stats = get_pg_stats(conn)
            conn.close()
            
            save_snapshot('before_repair.json', counts, stats)
            print("\n✅ Ready to run repair! Next step:")
            print("   USE_STATE_CHANGES=true SKIP_BLOCKS=true REPAIR_START_HEIGHT=8606270 REPAIR_END_HEIGHT=8606370 go run cmd/repair/repair.go")
        except Exception as e:
            print(f"\n❌ Error: {e}")
            print("\nMake sure data-handler.env file exists with correct database credentials.")
            sys.exit(1)
    
    elif command == "after":
        print("\n📸 Taking AFTER snapshot...")
        print(f"{'='*80}\n")
        print(f"Connecting to: {DB_CONFIG['user']}@{DB_CONFIG['host']}:{DB_CONFIG['port']}/{DB_CONFIG['database']}\n")
        
        try:
            conn = psycopg2.connect(**DB_CONFIG)
            counts = get_table_counts(conn)
            stats = get_pg_stats(conn)
            conn.close()
            
            save_snapshot('after_repair.json', counts, stats)
            print("\n✅ Ready to compare! Next step:")
            print("   python measure_repair_impact.py compare")
        except Exception as e:
            print(f"\n❌ Error: {e}")
            sys.exit(1)
    
    elif command == "compare":
        compare_snapshots('before_repair.json', 'after_repair.json')
    
    else:
        print(f"Unknown command: {command}")
        print("Use: before, after, or compare")
        sys.exit(1)

if __name__ == "__main__":
    main()
