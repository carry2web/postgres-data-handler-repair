# GraphQL Endpoint Comparison Summary

**Date:** February 11, 2026  
**Endpoints:**
- SafetyNet: https://graphql.safetynet.social/graphql
- DeSo Prod: https://graphql-prod.deso.com/graphql

## Key Findings

### 🚀 Performance Comparison

SafetyNet consistently outperforms DeSo Prod:
- **Account queries:** 2-2.5x faster
- **Diamond count:** 33x faster (0.84s vs 27.67s)
- **NFT count:** 11.6x faster (0.41s vs 4.82s)
- **Total accounts:** 2.6x faster (1.56s vs 3.98s)
- **Recent posts:** 15x faster (1.06s vs 16.25s)

### 📊 Data Gaps Identified

| Metric | SafetyNet | DeSo Prod | Difference | % Gap |
|--------|-----------|-----------|------------|-------|
| **Total Posts** | 17,758,421 | 17,963,369 | -204,948 | -1.15% |
| **Total Accounts** | 10,486,152 | 9,638,739 | **+847,413** | **+8.78%** |
| **Diamonds** | 13,583,129 | 13,755,196 | -172,067 | -1.27% |
| **NFTs** | 4,341,470 | 4,377,296 | -35,826 | -0.83% |
| **User Posts (carry2web)** | 6,308 | 6,564 | -256 | -4.06% |
| **Followers (carry2web)** | 1,268 | 1,350 | -82 | -6.47% |
| **Following (carry2web)** | 375 | 385 | -10 | -2.67% |

### ⚠️ Notable Observations

1. **Account Count Anomaly:** SafetyNet has 847K MORE accounts than DeSo Prod (+8.78%)
   - This is the opposite direction of other metrics
   - Suggests possible data inconsistency or different filtering logic
   - Needs investigation

2. **Consistent Data Gaps:** Most metrics show SafetyNet is behind by 1-6%
   - Posts: -1.15%
   - Diamonds: -1.27%
   - NFTs: -0.83%
   - User-specific data: -2.67% to -6.47%

3. **DeSo Prod Timeout Issues:** 
   - Block count query timed out
   - Transaction count query timed out
   - Like count query timed out
   - Follow count query timed out
   - These queries work on SafetyNet, suggesting infrastructure differences

4. **Performance Advantage:**
   - SafetyNet is significantly faster across all working queries
   - Especially dramatic on count queries (10-30x faster)
   - Regional deployment or infrastructure optimization showing clear benefits

### ✅ Queries That Match Exactly

- **Profile count** (with username filtering) - EXACT MATCH

### 🔧 Recommended Actions

1. **Investigate Account Count:** Why does SafetyNet have 847K more accounts?
2. **Sync Recent Data:** Focus on catching up the ~1-6% gap in posts, diamonds, NFTs
3. **Verify Block/Transaction Data:** Need working queries to compare these critical metrics
4. **Monitor Gap Repair:** The repair process is working, continue monitoring progress
5. **Document Performance:** SafetyNet's speed advantage is a major selling point

### 📈 Decentralization Value Proposition

**Why This Matters:**
- ✅ **Speed:** 2-30x faster queries for better UX
- ✅ **Reliability:** No timeouts on SafetyNet vs multiple on DeSo Prod
- ✅ **Regional:** Can deploy globally to reduce latency
- ✅ **Customizable:** Each instance can optimize for specific use cases
- ✅ **Resilient:** Reduces single point of failure

The data shows SafetyNet is catching up (currently 94-99% synced on most metrics) while already providing superior performance and reliability.

## Next Steps

1. Continue running gap repair to close the 1-6% data gap
2. Add queries for blocks and transactions (once schema is confirmed)
3. Schedule regular comparisons to monitor sync progress
4. Consider this infrastructure ready for production use with known data freshness caveats
