#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Quick Data Gap Summary - Fast queries only for monitoring sync progress
"""

import requests
import json
import sys
from datetime import datetime

if sys.platform == 'win32':
    sys.stdout.reconfigure(encoding='utf-8')

SAFETYNET = "https://graphql.safetynet.social/graphql"
DESO_PROD = "https://graphql-prod.deso.com/graphql"

QUICK_QUERIES = {
    "Total Posts": """query { posts { totalCount } }""",
    "Total Accounts": """query { accounts { totalCount } }""",
    "Total Diamonds": """query { diamonds { totalCount } }""",
    "Total NFTs": """query { nfts { totalCount } }""",
    "Total Follows": """query { follows { totalCount } }""",
    "Total Likes": """query { likes { totalCount } }""",
    "Total Post Associations": """query { postAssociations { totalCount } }""",
    "Total User Associations": """query { userAssociations { totalCount } }""",
}

def query_endpoint(url, query):
    """Execute a query and return totalCount"""
    try:
        response = requests.post(
            url,
            json={"query": query},
            headers={"Content-Type": "application/json"},
            timeout=60
        )
        if response.status_code == 200:
            data = response.json()
            # Extract totalCount from the response
            for key in data.get('data', {}).values():
                if isinstance(key, dict) and 'totalCount' in key:
                    return key['totalCount']
        return None
    except Exception as e:
        return None

def main():
    print(f"\n{'='*80}")
    print(f"DeSo Core Transaction Sync Status - {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print(f"{'='*80}\n")
    print("Note: DeSo Prod has 30s query timeout - some queries may fail\n")
    
    results = []
    
    for name, query in QUICK_QUERIES.items():
        print(f"Querying {name}...", end=" ", flush=True)
        
        safetynet_count = query_endpoint(SAFETYNET, query)
        deso_count = query_endpoint(DESO_PROD, query)
        
        if safetynet_count is not None and deso_count is not None:
            diff = safetynet_count - deso_count
            pct = (diff / deso_count * 100) if deso_count > 0 else 0
            sync_pct = (safetynet_count / deso_count * 100) if deso_count > 0 else 0
            
            results.append({
                'name': name,
                'safetynet': safetynet_count,
                'deso': deso_count,
                'diff': diff,
                'pct': pct,
                'sync_pct': sync_pct
            })
            print(f"✓")
        else:
            print(f"✗ (timeout or error)")
    
    print(f"\n{'='*80}")
    print(f"{'Metric':<20} {'SafetyNet':>15} {'DeSo Prod':>15} {'Difference':>15} {'Gap %':>10} {'Synced %':>10}")
    print(f"{'='*80}")
    
    for r in results:
        status = "✓" if r['sync_pct'] >= 99 else "⚠"
        print(f"{r['name']:<20} {r['safetynet']:>15,} {r['deso']:>15,} {r['diff']:>+15,} {r['pct']:>+9.2f}% {r['sync_pct']:>9.2f}% {status}")
    
    print(f"{'='*80}\n")
    
    # Summary
    avg_sync = sum(r['sync_pct'] for r in results) / len(results) if results else 0
    print(f"Average Sync Status: {avg_sync:.2f}%")
    print(f"Queries Successful: {len(results)}/{len(QUICK_QUERIES)}")
    
    total_gap = sum(abs(r['diff']) for r in results)
    print(f"Total Records Gap: {total_gap:,}")
    print(f"\nNote: Accounts showing +8% may indicate data inconsistency rather than gap.\n")

if __name__ == "__main__":
    main()
