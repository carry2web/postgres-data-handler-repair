package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/deso-protocol/core/lib"
	"github.com/deso-protocol/postgres-data-handler/handler"
	lru "github.com/hashicorp/golang-lru/v2"
	_ "github.com/lib/pq"
	"github.com/spf13/viper"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/extra/bundebug"
)

// Gap represents a contiguous range of missing block heights.
type Gap struct{ Start, End uint64 }

// detectGaps runs the user-provided SQL to return missing block ranges.
func detectGaps(db *bun.DB) ([]Gap, error) {
	type gapRow struct{ Start, End, MissingCount uint64 }
	var rows []gapRow
	query := `
WITH ordered AS (
  SELECT DISTINCT height
  FROM block
),
sequenced AS (
  SELECT
    height,
    LEAD(height) OVER (ORDER BY height) AS next_height
  FROM ordered
)
SELECT
  height + 1        AS start,
  next_height - 1   AS end,
  (next_height - height - 1) AS missing_count
FROM sequenced
WHERE next_height IS NOT NULL
  AND next_height > height + 1
ORDER BY start;
	`
	err := db.NewRaw(query).Scan(context.Background(), &rows)
	if err != nil {
		return nil, fmt.Errorf("detectGaps query failed: %w", err)
	}
	var gaps []Gap
	for _, r := range rows {
		gaps = append(gaps, Gap{Start: r.Start, End: r.End})
	}
	return gaps, nil
}

// fetchBlockByHeight fetches a block from the DeSo node by height using the node API endpoint.
func fetchBlockByHeight(nodeURL string, height uint64) (*lib.MsgDeSoBlock, error) {
	// Try local node first, then fallback to public node
	nodeURLs := []string{nodeURL, "https://node.deso.org"}

	var lastErr error
	for _, baseURL := range nodeURLs {
		url := fmt.Sprintf("%s/api/v0/block", baseURL)
		body, err := json.Marshal(map[string]interface{}{"Height": height})
		if err != nil {
			return nil, fmt.Errorf("marshal block request: %w", err)
		}

		req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
		if err != nil {
			lastErr = fmt.Errorf("create block request: %w", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("block request failed for %s: %w", baseURL, err)
			continue
		}
		defer resp.Body.Close()

		respBody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			lastErr = fmt.Errorf("read block response: %w", err)
			resp.Body.Close()
			continue
		}
		if resp.StatusCode == 404 {
			lastErr = fmt.Errorf("endpoint not found at %s (404)", baseURL)
			resp.Body.Close()
			continue // Try next URL
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("block fetch failed from %s (status %d): %s", baseURL, resp.StatusCode, string(respBody))
			resp.Body.Close()
			continue
		}

		var result struct {
			Header *lib.MsgDeSoHeader `json:"Header"`
			Txns   []*lib.MsgDeSoTxn  `json:"Txns"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			lastErr = fmt.Errorf("decode block response: %w", err)
			resp.Body.Close()
			continue
		}

		if result.Header == nil {
			lastErr = fmt.Errorf("no header in block response from %s", baseURL)
			resp.Body.Close()
			continue
		}

		block := &lib.MsgDeSoBlock{
			Header: result.Header,
			Txns:   result.Txns,
		}

		log.Printf("Fetched block %d from %s", height, baseURL)
		return block, nil
	}

	return nil, fmt.Errorf("fetchBlockByHeight failed for all nodes: %v", lastErr)
}

// processBlockFromAPI fetches and processes a block from the node API
func processBlockFromAPI(nodeURL string, height uint64, pdh *handler.PostgresDataHandler) error {
	block, err := fetchBlockByHeight(nodeURL, height)
	if err != nil {
		return err
	}

	// Compute block hash for KeyBytes
	blockHash, err := block.Hash()
	if err != nil {
		return fmt.Errorf("failed to compute block hash: %w", err)
	}

	// Create state change entry for the block
	blockEntry := &lib.StateChangeEntry{
		EncoderType:   lib.EncoderTypeBlock,
		OperationType: lib.DbOperationTypeUpsert,
		Encoder:       block,
		Block:         block,
		BlockHeight:   height,
		KeyBytes:      blockHash[:],
	}

	// Process the block entry
	if err := pdh.HandleEntryBatch([]*lib.StateChangeEntry{blockEntry}, false); err != nil {
		return fmt.Errorf("failed to process block for height %d: %w", height, err)
	}

	log.Printf("Successfully processed block %d via API", height)
	return nil
}

func main() {
	// Load configuration the same way as main.go
	viper.SetConfigFile(".env")
	if err := viper.ReadInConfig(); err != nil && !os.IsNotExist(err) {
		log.Fatalf("Error reading .env: %v", err)
	}
	viper.AutomaticEnv()

	dbHost := viper.GetString("DB_HOST")
	dbPort := viper.GetString("DB_PORT")
	dbUser := viper.GetString("DB_USERNAME")
	dbPass := viper.GetString("DB_PASSWORD")
	dbName := "postgres"
	if n := viper.GetString("DB_NAME"); n != "" {
		dbName = n
	}
	pgURI := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable&timeout=18000s", dbUser, dbPass, dbHost, dbPort, dbName)

	// Open DB using the same pattern as main.go
	pgdb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(pgURI)))
	if pgdb == nil {
		log.Fatalf("Failed to open postgres DB")
	}
	db := bun.NewDB(pgdb, pgdialect.New())
	db.SetConnMaxLifetime(0)
	db.SetMaxIdleConns(50) // you can tune this

	// Optional: enable query logging
	if viper.GetBool("LOG_QUERIES") {
		db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(true)))
	}

	// Get node URL for API calls
	nodeURL := viper.GetString("NODE_URL")
	if nodeURL == "" {
		nodeURL = "http://localhost:17001" // Default for mainnet node
	}
	log.Printf("Using DeSo node URL: %s", nodeURL)

	// Choose network params
	params := &lib.DeSoMainnetParams
	if viper.GetBool("IS_TESTNET") {
		params = &lib.DeSoTestnetParams
		if viper.GetBool("REGTEST") {
			params.EnableRegtest(viper.GetBool("ACCELERATED_REGTEST"))
		}
	}
	lib.GlobalDeSoParams = *params

	// Create PostgresDataHandler for transactionality
	cachedEntries, err := lru.New[string, []byte](int(handler.EntryCacheSize))
	if err != nil {
		log.Fatalf("LRU cache: %v", err)
	}
	pdh := &handler.PostgresDataHandler{
		DB:            db,
		Params:        params,
		CachedEntries: cachedEntries,
	}

	// Detect gaps
	gaps, err := detectGaps(db)
	if err != nil {
		log.Fatalf("detectGaps: %v", err)
	}
	log.Printf("Found %d gap(s)", len(gaps))
	for _, g := range gaps {
		log.Printf("Gap: %d -> %d (%d blocks)", g.Start, g.End, g.End-g.Start+1)
	}

	// Process each gap
	for _, gap := range gaps {
		log.Printf("Processing gap: %d -> %d (%d blocks)", gap.Start, gap.End, gap.End-gap.Start+1)

		if err := pdh.InitiateTransaction(); err != nil {
			log.Fatalf("InitiateTransaction: %v", err)
		}

		// Process each block height in the gap
		for h := gap.Start; h <= gap.End; h++ {
			log.Printf("Processing height %d...", h)

			// Fetch and process block from node API
			if err := processBlockFromAPI(nodeURL, h, pdh); err != nil {
				log.Printf("WARNING: Failed to process block %d: %v", h, err)
				continue
			}
		}

		if err := pdh.CommitTransaction(); err != nil {
			log.Fatalf("CommitTransaction: %v", err)
		}
		log.Printf("Successfully repaired gap %d -> %d", gap.Start, gap.End)
	}
	log.Println("Repair completed successfully")
}
