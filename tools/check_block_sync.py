#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Check block height synchronization between SafetyNet and DeSo Prod
"""

import requests
import json
import sys
from datetime import datetime

if sys.platform == 'win32':
    sys.stdout.reconfigure(encoding='utf-8')

SAFETYNET = "https://graphql.safetynet.social/graphql"
DESO_PROD = "https://graphql-prod.deso.com/graphql"

def query_endpoint(url, query):
    """Execute a query and return the result"""
    try:
        response = requests.post(
            url,
            json={"query": query},
            headers={"Content-Type": "application/json"},
            timeout=60
        )
        if response.status_code == 200:
            return response.json()
        return {"error": f"{response.status_code} - {response.text[:200]}"}
    except Exception as e:
        return {"error": str(e)}

def main():
    print(f"\n{'='*80}")
    print(f"DeSo Block Synchronization Check - {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print(f"{'='*80}\n")
    
    # Query for latest blocks
    latest_block_query = """
        query LatestBlocks {
          blocks(first: 1, orderBy: HEIGHT_DESC) {
            nodes {
              height
              timestamp
            }
          }
        }
    """
    
    print("Querying latest block heights...\n")
    
    sn_result = query_endpoint(SAFETYNET, latest_block_query)
    dp_result = query_endpoint(DESO_PROD, latest_block_query)
    
    if "error" in sn_result:
        print(f"❌ SafetyNet Error: {sn_result['error']}")
        sn_height = None
    else:
        sn_height = sn_result.get('data', {}).get('blocks', {}).get('nodes', [{}])[0].get('height')
        sn_time = sn_result.get('data', {}).get('blocks', {}).get('nodes', [{}])[0].get('timestamp')
        if sn_height:
            print(f"✓ SafetyNet Latest Block: {sn_height:,}")
            print(f"  Timestamp: {sn_time}")
        else:
            print("❌ SafetyNet: No block data returned")
            sn_height = None
    
    print()
    
    if "error" in dp_result:
        print(f"❌ DeSo Prod Error: {dp_result['error']}")
        dp_height = None
    else:
        dp_height = dp_result.get('data', {}).get('blocks', {}).get('nodes', [{}])[0].get('height')
        dp_time = dp_result.get('data', {}).get('blocks', {}).get('nodes', [{}])[0].get('timestamp')
        if dp_height:
            print(f"✓ DeSo Prod Latest Block: {dp_height:,}")
            print(f"  Timestamp: {dp_time}")
        else:
            print("❌ DeSo Prod: No block data returned")
            dp_height = None
    
    print(f"\n{'='*80}")
    
    if sn_height and dp_height:
        diff = dp_height - sn_height
        pct = (diff / dp_height * 100) if dp_height > 0 else 0
        
        print(f"\nBlock Height Difference: {diff:,} blocks")
        print(f"Percentage Gap: {pct:.4f}%")
        
        if diff == 0:
            print("\n✅ Perfect Sync! Both at the same block height.")
        elif diff > 0:
            print(f"\n⚠️  SafetyNet is {diff:,} blocks behind DeSo Prod")
            print(f"    This explains approximately {pct:.2f}% of data differences")
            
            # Estimate transactions
            avg_txns_per_block = 40  # rough estimate
            est_missing_txns = diff * avg_txns_per_block
            print(f"    Estimated missing transactions: ~{est_missing_txns:,}")
        else:
            print(f"\n⚠️  SafetyNet is {abs(diff):,} blocks AHEAD of DeSo Prod (unusual!)")
    
    print(f"\n{'='*80}")
    
    # Also check if we can get total block counts
    print("\nChecking total block counts (may timeout)...")
    
    count_query = """
        query BlockCount {
          blocks {
            totalCount
          }
        }
    """
    
    print("SafetyNet: ", end="", flush=True)
    sn_count = query_endpoint(SAFETYNET, count_query)
    if "error" not in sn_count:
        total = sn_count.get('data', {}).get('blocks', {}).get('totalCount')
        print(f"{total:,} blocks")
    else:
        print("Timeout or error")
    
    print("DeSo Prod: ", end="", flush=True)
    dp_count = query_endpoint(DESO_PROD, count_query)
    if "error" not in dp_count:
        total = dp_count.get('data', {}).get('blocks', {}).get('totalCount')
        print(f"{total:,} blocks")
    else:
        print("Timeout or error (expected due to 30s limit)")
    
    print(f"\n{'='*80}\n")

if __name__ == "__main__":
    main()
