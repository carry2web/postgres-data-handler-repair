#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
GraphQL Endpoint Comparison Tool

Compares query results between two GraphQL endpoints to identify data discrepancies.
Useful for validating data synchronization between instances.
"""

import requests
import json
import sys
import os
from typing import Dict, Any, List, Tuple
from datetime import datetime
import argparse
from deepdiff import DeepDiff
from colorama import init, Fore, Style

# Initialize colorama for cross-platform colored output
init(autoreset=True)

# Fix Windows console encoding for Unicode characters
if sys.platform == 'win32':
    sys.stdout.reconfigure(encoding='utf-8')

# Global log file handle
log_file = None

def log_print(message: str, also_to_console: bool = True):
    """Print to both console and log file."""
    if also_to_console:
        print(message)
    if log_file:
        # Strip color codes for log file
        import re
        clean_message = re.sub(r'\x1b\[[0-9;]*m', '', message)
        log_file.write(clean_message + '\n')
        log_file.flush()

class GraphQLComparator:
    def __init__(self, endpoint1: str, endpoint2: str, endpoint1_name: str = "SafetyNet", endpoint2_name: str = "DeSo Prod"):
        self.endpoint1 = endpoint1
        self.endpoint2 = endpoint2
        self.endpoint1_name = endpoint1_name
        self.endpoint2_name = endpoint2_name
        
    def execute_query(self, endpoint: str, query: str, variables: Dict[str, Any] = None) -> Tuple[Dict[str, Any], float]:
        """Execute a GraphQL query and return the response and execution time."""
        start_time = datetime.now()
        
        # Clean up the query - remove extra whitespace
        clean_query = " ".join(query.split())
        
        try:
            payload = {
                "query": clean_query,
                "variables": variables or {}
            }
            
            headers = {
                "Content-Type": "application/json",
                "Accept": "application/json",
                "User-Agent": "GraphQL-Comparator/1.0"
            }
            
            response = requests.post(
                endpoint,
                json=payload,
                headers=headers,
                timeout=30
            )
            
            elapsed = (datetime.now() - start_time).total_seconds()
            
            # Don't raise for status immediately - let's see what we got
            if response.status_code != 200:
                return {
                    "error": f"{response.status_code} Error: {response.reason}",
                    "details": response.text[:500]  # First 500 chars of error
                }, elapsed
            
            return response.json(), elapsed
            
        except requests.exceptions.RequestException as e:
            elapsed = (datetime.now() - start_time).total_seconds()
            return {"error": str(e)}, elapsed
        except json.JSONDecodeError as e:
            elapsed = (datetime.now() - start_time).total_seconds()
            return {"error": f"JSON decode error: {str(e)}", "response": response.text[:500]}, elapsed
    
    def compare_responses(self, result1: Dict, result2: Dict) -> Dict[str, Any]:
        """Compare two GraphQL responses and return differences."""
        diff = DeepDiff(result1, result2, ignore_order=True, verbose_level=2)
        return diff
    
    def print_comparison(self, query_name: str, query: str, variables: Dict, result1: Tuple, result2: Tuple):
        """Print a formatted comparison of query results."""
        response1, time1 = result1
        response2, time2 = result2
        
        log_print(f"\n{'='*80}")
        log_print(f"{Fore.CYAN}{Style.BRIGHT}Query: {query_name}{Style.RESET_ALL}")
        log_print(f"{'='*80}")
        
        log_print(f"\n{Fore.YELLOW}Variables:{Style.RESET_ALL}")
        log_print(json.dumps(variables, indent=2))
        
        log_print(f"\n{Fore.BLUE}Response Times:{Style.RESET_ALL}")
        log_print(f"  {self.endpoint1_name}: {time1:.3f}s")
        log_print(f"  {self.endpoint2_name}: {time2:.3f}s")
        log_print(f"  Difference: {abs(time1 - time2):.3f}s")
        
        # Check for errors
        has_error1 = "error" in response1 or ("errors" in response1)
        has_error2 = "error" in response2 or ("errors" in response2)
        
        if has_error1 or has_error2:
            log_print(f"\n{Fore.RED}{Style.BRIGHT}[ERROR] ERRORS DETECTED{Style.RESET_ALL}")
            if has_error1:
                log_print(f"\n{Fore.RED}{self.endpoint1_name} Error:{Style.RESET_ALL}")
                log_print(json.dumps(response1, indent=2))
            if has_error2:
                log_print(f"\n{Fore.RED}{self.endpoint2_name} Error:{Style.RESET_ALL}")
                log_print(json.dumps(response2, indent=2))
            return
        
        # Compare responses
        diff = self.compare_responses(response1, response2)
        
        if not diff:
            log_print(f"\n{Fore.GREEN}{Style.BRIGHT}[OK] RESULTS MATCH EXACTLY{Style.RESET_ALL}")
        else:
            log_print(f"\n{Fore.RED}{Style.BRIGHT}[!] DIFFERENCES FOUND{Style.RESET_ALL}")
            
            # Print detailed differences
            if "values_changed" in diff:
                log_print(f"\n{Fore.YELLOW}Value Changes:{Style.RESET_ALL}")
                for path, change in diff["values_changed"].items():
                    log_print(f"\n  Path: {Fore.CYAN}{path}{Style.RESET_ALL}")
                    log_print(f"    {self.endpoint1_name}: {Fore.RED}{change['old_value']}{Style.RESET_ALL}")
                    log_print(f"    {self.endpoint2_name}: {Fore.GREEN}{change['new_value']}{Style.RESET_ALL}")
                    
                    # Calculate difference for numeric values
                    if isinstance(change['old_value'], (int, float)) and isinstance(change['new_value'], (int, float)):
                        diff_val = change['new_value'] - change['old_value']
                        diff_pct = (diff_val / change['old_value'] * 100) if change['old_value'] != 0 else 0
                        log_print(f"    Difference: {diff_val} ({diff_pct:+.2f}%)")
            
            if "dictionary_item_added" in diff:
                log_print(f"\n{Fore.GREEN}Items in {self.endpoint2_name} only:{Style.RESET_ALL}")
                for item in diff["dictionary_item_added"]:
                    log_print(f"  + {item}")
            
            if "dictionary_item_removed" in diff:
                log_print(f"\n{Fore.RED}Items in {self.endpoint1_name} only:{Style.RESET_ALL}")
                for item in diff["dictionary_item_removed"]:
                    log_print(f"  - {item}")
            
            if "iterable_item_added" in diff:
                log_print(f"\n{Fore.GREEN}Array items in {self.endpoint2_name} only:{Style.RESET_ALL}")
                for path, items in diff["iterable_item_added"].items():
                    log_print(f"  {path}: {items}")
            
            if "iterable_item_removed" in diff:
                log_print(f"\n{Fore.RED}Array items in {self.endpoint1_name} only:{Style.RESET_ALL}")
                for path, items in diff["iterable_item_removed"].items():
                    log_print(f"  {path}: {items}")
        
        # Optionally print full responses for inspection
        if args.verbose:
            log_print(f"\n{Fore.BLUE}Full Response from {self.endpoint1_name}:{Style.RESET_ALL}")
            log_print(json.dumps(response1, indent=2))
            log_print(f"\n{Fore.BLUE}Full Response from {self.endpoint2_name}:{Style.RESET_ALL}")
            log_print(json.dumps(response2, indent=2))
    
    def run_comparison(self, query_name: str, query: str, variables: Dict[str, Any] = None):
        """Run a single query comparison."""
        log_print(f"\n{Fore.MAGENTA}Executing query on both endpoints...{Style.RESET_ALL}")
        
        result1 = self.execute_query(self.endpoint1, query, variables)
        result2 = self.execute_query(self.endpoint2, query, variables)
        
        self.print_comparison(query_name, query, variables or {}, result1, result2)


# Predefined test queries
TEST_QUERIES = {
    "account_stats": {
        "query": """
            query Accounts($first: Int, $condition: AccountCondition) {
              accounts(first: $first, condition: $condition) {
                nodes {
                  username
                  followers {
                    totalCount
                  }
                  following {
                    totalCount
                  }
                }
              }
            }
        """,
        "variables": {
            "first": 10,
            "condition": {
                "publicKey": "BC1YLh8heSjLGcmd7k8p2L4C63r4PhGCdESTcVNDDvTbrrP8NaidpTF"
            }
        }
    },
    "recent_posts": {
        "query": """
            query RecentPosts($first: Int) {
              posts(first: $first, orderBy: TIMESTAMP_DESC) {
                nodes {
                  posterPublicKey
                  body
                  timestamp
                }
                totalCount
              }
            }
        """,
        "variables": {
            "first": 5
        }
    },
    "top_profiles": {
        "query": """
            query TopProfiles($first: Int) {
              accounts(first: $first, orderBy: COIN_PRICE_DESO_NANOS_DESC) {
                nodes {
                  username
                  publicKey
                  coinPriceDesoNanos
                  followers {
                    totalCount
                  }
                }
              }
            }
        """,
        "variables": {
            "first": 10
        }
    },
    "user_posts": {
        "query": """
            query UserPosts($publicKey: String!, $first: Int) {
              posts(
                first: $first
                condition: { posterPublicKey: $publicKey }
                orderBy: TIMESTAMP_DESC
              ) {
                nodes {
                  body
                  timestamp
                }
                totalCount
              }
            }
        """,
        "variables": {
            "publicKey": "BC1YLh8heSjLGcmd7k8p2L4C63r4PhGCdESTcVNDDvTbrrP8NaidpTF",
            "first": 5
        }
    },
    "follower_stats": {
        "query": """
            query FollowerStats($publicKey: String!) {
              accounts(condition: { publicKey: $publicKey }) {
                nodes {
                  username
                  followers {
                    totalCount
                  }
                  following {
                    totalCount
                  }
                }
              }
            }
        """,
        "variables": {
            "publicKey": "BC1YLh8heSjLGcmd7k8p2L4C63r4PhGCdESTcVNDDvTbrrP8NaidpTF"
        }
    },
    "block_count": {
        "query": """
            query BlockCount {
              blocks {
                totalCount
              }
            }
        """,
        "variables": {}
    },
    "latest_blocks": {
        "query": """
            query LatestBlocks($first: Int) {
              blocks(first: $first, orderBy: HEIGHT_DESC) {
                nodes {
                  height
                  timestamp
                }
                totalCount
              }
            }
        """,
        "variables": {
            "first": 5
        }
    },
    "transaction_count": {
        "query": """
            query TransactionCount {
              transactions {
                totalCount
              }
            }
        """,
        "variables": {}
    },
    "recent_transactions": {
        "query": """
            query RecentTransactions($first: Int) {
              transactions(first: $first, orderBy: BLOCK_HEIGHT_DESC) {
                nodes {
                  blockHeight
                  txnType
                }
                totalCount
              }
            }
        """,
        "variables": {
            "first": 10
        }
    },
    "profile_count": {
        "query": """
            query ProfileCount {
              accounts(condition: { username: null }, filter: { username: { isNull: false } }) {
                totalCount
              }
            }
        """,
        "variables": {}
    },
    "total_accounts": {
        "query": """
            query TotalAccounts {
              accounts {
                totalCount
              }
            }
        """,
        "variables": {}
    },
    "top_profiles_by_price": {
        "query": """
            query TopProfilesByPrice($first: Int) {
              accounts(
                first: $first
                filter: { username: { isNull: false } }
                orderBy: COIN_PRICE_DESO_NANOS_DESC
              ) {
                nodes {
                  username
                  publicKey
                  coinPriceDesoNanos
                  followers {
                    totalCount
                  }
                }
                totalCount
              }
            }
        """,
        "variables": {
            "first": 10
        }
    },
    "like_count": {
        "query": """
            query LikeCount {
              likes {
                totalCount
              }
            }
        """,
        "variables": {}
    },
    "diamond_count": {
        "query": """
            query DiamondCount {
              diamonds {
                totalCount
              }
            }
        """,
        "variables": {}
    },
    "follow_count": {
        "query": """
            query FollowCount {
              follows {
                totalCount
              }
            }
        """,
        "variables": {}
    },
    "nft_count": {
        "query": """
            query NFTCount {
              nfts {
                totalCount
              }
            }
        """,
        "variables": {}
    },
    "post_associations_count": {
        "query": """
            query PostAssociationsCount {
              postAssociations {
                totalCount
              }
            }
        """,
        "variables": {}
    },
    "user_associations_count": {
        "query": """
            query UserAssociationsCount {
              userAssociations {
                totalCount
              }
            }
        """,
        "variables": {}
    },
    "recent_likes": {
        "query": """
            query RecentLikes($first: Int) {
              likes(first: $first, orderBy: LIKER_PKID_DESC) {
                nodes {
                  likerPkid
                  isUnlike
                }
                totalCount
              }
            }
        """,
        "variables": {
            "first": 10
        }
    },
    "recent_follows": {
        "query": """
            query RecentFollows($first: Int) {
              follows(first: $first, orderBy: FOLLOWER_PKID_DESC) {
                nodes {
                  followerPkid
                  followedPkid
                }
                totalCount
              }
            }
        """,
        "variables": {
            "first": 10
        }
    },
    "recent_diamonds": {
        "query": """
            query RecentDiamonds($first: Int) {
              diamonds(first: $first, orderBy: SENDER_PKID_DESC) {
                nodes {
                  senderPkid
                  receiverPkid
                  diamondLevel
                }
                totalCount
              }
            }
        """,
        "variables": {
            "first": 10
        }
    },
    "recent_post_associations": {
        "query": """
            query RecentPostAssociations($first: Int) {
              postAssociations(first: $first, orderBy: POST_HASH_DESC) {
                nodes {
                  postHash
                  associationType
                  associationValue
                }
                totalCount
              }
            }
        """,
        "variables": {
            "first": 10
        }
    },
    "recent_user_associations": {
        "query": """
            query RecentUserAssociations($first: Int) {
              userAssociations(first: $first, orderBy: TRANSACTOR_PKID_DESC) {
                nodes {
                  transactorPkid
                  targetUserPkid
                  associationType
                }
                totalCount
              }
            }
        """,
        "variables": {
            "first": 10
        }
    }
}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Compare GraphQL query results between two endpoints"
    )
    parser.add_argument(
        "--endpoint1",
        default="https://graphql.safetynet.social/graphql",
        help="First GraphQL endpoint (default: SafetyNet)"
    )
    parser.add_argument(
        "--endpoint2",
        default="https://graphql-prod.deso.com/graphql",
        help="Second GraphQL endpoint (default: DeSo Prod)"
    )
    parser.add_argument(
        "--query",
        choices=list(TEST_QUERIES.keys()) + ["all"],
        default="all",
        help="Which test query to run (default: all)"
    )
    parser.add_argument(
        "--custom-query",
        help="Custom GraphQL query string"
    )
    parser.add_argument(
        "--custom-variables",
        help="Custom variables JSON string"
    )
    parser.add_argument(
        "--verbose",
        "-v",
        action="store_true",
        help="Print full responses"
    )
    parser.add_argument(
        "--log-file",
        default="graphql_comparison.log",
        help="Log file path (default: graphql_comparison.log)"
    )
    
    args = parser.parse_args()
    
    # Open log file
    log_file = open(args.log_file, 'w', encoding='utf-8')
    
    try:
        comparator = GraphQLComparator(args.endpoint1, args.endpoint2)
        
        log_print(f"{Fore.CYAN}{Style.BRIGHT}GraphQL Endpoint Comparison Tool{Style.RESET_ALL}")
        log_print(f"Endpoint 1: {args.endpoint1}")
        log_print(f"Endpoint 2: {args.endpoint2}")
        log_print(f"Log file: {os.path.abspath(args.log_file)}")
        log_print("")
        
        if args.custom_query:
            # Run custom query
            variables = json.loads(args.custom_variables) if args.custom_variables else {}
            comparator.run_comparison("Custom Query", args.custom_query, variables)
        elif args.query == "all":
            # Run all test queries
            for query_name, query_data in TEST_QUERIES.items():
                comparator.run_comparison(
                    query_name,
                    query_data["query"],
                    query_data["variables"]
                )
        else:
            # Run specific test query
            query_data = TEST_QUERIES[args.query]
            comparator.run_comparison(
                args.query,
                query_data["query"],
                query_data["variables"]
            )
        
        log_print(f"\n{'='*80}")
        log_print(f"{Fore.CYAN}{Style.BRIGHT}Comparison Complete{Style.RESET_ALL}")
        log_print(f"{'='*80}\n")
        
    finally:
        if log_file:
            log_file.close()
            print(f"\nResults saved to: {os.path.abspath(args.log_file)}")

