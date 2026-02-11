# GraphQL Endpoint Comparison Tool

Compare query results between two GraphQL endpoints to identify data discrepancies and validate synchronization.

## Installation

```bash
pip install -r requirements-compare.txt
```

## Usage

### Run all test queries
```bash
python compare_graphql.py
```

### Run a specific test query
```bash
python compare_graphql.py --query account_stats
```

### Available test queries:
- `account_stats` - Compare follower/following counts
- `recent_posts` - Compare recent posts
- `top_profiles` - Compare top profiles by coin price
- `user_posts` - Compare user's posts
- `follower_stats` - Compare detailed follower data

### Run a custom query
```bash
python compare_graphql.py \
  --custom-query "query { accounts(first: 5) { nodes { username } } }" \
  --custom-variables '{"first": 5}'
```

### Compare different endpoints
```bash
python compare_graphql.py \
  --endpoint1 http://localhost:8084/graphql \
  --endpoint2 https://graphql-prod.deso.com/graphql
```

### Verbose output (show full responses)
```bash
python compare_graphql.py --query account_stats --verbose
```

## Output

The tool provides:
- ✓ Match confirmation when results are identical
- ✗ Detailed differences with highlighted changes
- Response time comparison
- Numeric difference calculations with percentages
- Color-coded output for easy reading

## Example Output

```
Query: account_stats
================================================================================

Response Times:
  SafetyNet: 0.234s
  DeSo Prod: 0.156s
  Difference: 0.078s

✗ DIFFERENCES FOUND

Value Changes:
  Path: root['data']['accounts']['nodes'][0]['followers']['totalCount']
    SafetyNet: 1268
    DeSo Prod: 1350
    Difference: 82 (+6.47%)

  Path: root['data']['accounts']['nodes'][0]['following']['totalCount']
    SafetyNet: 375
    DeSo Prod: 385
    Difference: 10 (+2.67%)
```

## Use Cases

1. **Data Validation**: Verify your local instance matches production
2. **Sync Verification**: Check if data synchronization is complete
3. **Performance Testing**: Compare response times
4. **Debugging**: Identify specific data discrepancies
5. **Testing**: Validate schema compatibility between versions
