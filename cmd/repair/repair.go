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
	"sync"
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

// fetchBlockByHeight fetches a block from the DeSo node by height using the /api/v1/block endpoint.
func fetchBlockByHeight(nodeURL string, height uint64) (*lib.MsgDeSoBlock, error) {
	url := fmt.Sprintf("%s/api/v1/block", nodeURL)
	body, err := json.Marshal(map[string]interface{}{
		"Height":    height,
		"FullBlock": true,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal block request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse APIBlockResponse format
	// The API returns hashes as hex strings in specific field names
	var apiResult struct {
		Header struct {
			Version                      uint32 `json:"Version"`
			PrevBlockHashHex             string `json:"PrevBlockHashHex"`
			TransactionMerkleRootHex     string `json:"TransactionMerkleRootHex"`
			TstampNanoSecs               int64  `json:"TstampNanoSecs"`
			Height                       uint64 `json:"Height"`
			Nonce                        uint64 `json:"Nonce"`
			ExtraNonce                   uint64 `json:"ExtraNonce"`
			ProposerVotingPublicKey      string `json:"ProposerVotingPublicKey"`
			ProposerRandomSeedSignature  string `json:"ProposerRandomSeedSignature"`
			ProposedInView               uint64 `json:"ProposedInView"`
			ProposerVotePartialSignature string `json:"ProposerVotePartialSignature"`
		} `json:"Header"`
		Transactions []struct {
			RawTransactionHex string `json:"RawTransactionHex"`
		} `json:"Transactions"`
		Error string `json:"Error"`
	}
	if err := json.Unmarshal(respBody, &apiResult); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if apiResult.Error != "" {
		return nil, fmt.Errorf("API error: %s", apiResult.Error)
	}

	// Decode hex strings to BlockHash
	prevBlockHash, err := decodeBlockHash(apiResult.Header.PrevBlockHashHex)
	if err != nil {
		return nil, fmt.Errorf("decode prev block hash: %w", err)
	}

	txnMerkleRoot, err := decodeBlockHash(apiResult.Header.TransactionMerkleRootHex)
	if err != nil {
		return nil, fmt.Errorf("decode txn merkle root: %w", err)
	}

	// Build the proper Header struct
	header := &lib.MsgDeSoHeader{
		Version:               apiResult.Header.Version,
		PrevBlockHash:         prevBlockHash,
		TransactionMerkleRoot: txnMerkleRoot,
		TstampNanoSecs:        apiResult.Header.TstampNanoSecs,
		Height:                apiResult.Header.Height,
		Nonce:                 apiResult.Header.Nonce,
		ExtraNonce:            apiResult.Header.ExtraNonce,
		ProposedInView:        apiResult.Header.ProposedInView,
	}

	// TODO: Decode BLS fields if present (for PoS blocks)
	// For now, leave them nil for old PoW blocks

	// Decode transactions from hex
	txns := make([]*lib.MsgDeSoTxn, len(apiResult.Transactions))
	for i, txData := range apiResult.Transactions {
		txnBytes, err := hex.DecodeString(txData.RawTransactionHex)
		if err != nil {
			return nil, fmt.Errorf("decode transaction %d hex: %w", i, err)
		}

		txn := &lib.MsgDeSoTxn{}
		if err := txn.FromBytes(txnBytes); err != nil {
			return nil, fmt.Errorf("parse transaction %d bytes: %w", i, err)
		}
		txns[i] = txn
	}

	block := &lib.MsgDeSoBlock{
		Header: header,
		Txns:   txns,
	}

	return block, nil
}

// decodeBlockHash converts a hex string to *lib.BlockHash
func decodeBlockHash(hexStr string) (*lib.BlockHash, error) {
	if hexStr == "" {
		return nil, nil
	}

	hashBytes, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, err
	}

	if len(hashBytes) != lib.HashSizeBytes {
		return nil, fmt.Errorf("invalid hash length: got %d, want %d", len(hashBytes), lib.HashSizeBytes)
	}

	var blockHash lib.BlockHash
	copy(blockHash[:], hashBytes)
	return &blockHash, nil
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

// processGapParallel fetches and processes blocks in parallel using worker pool
func processGapParallel(nodeURL string, startHeight, endHeight uint64, pdh *handler.PostgresDataHandler, workers int) error {
	type blockJob struct {
		height uint64
	}
	type blockResult struct {
		height uint64
		entry  *lib.StateChangeEntry
		err    error
	}

	jobs := make(chan blockJob, workers*2)
	results := make(chan blockResult, workers*2)

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobs {
				block, err := fetchBlockByHeight(nodeURL, job.height)
				if err != nil {
					results <- blockResult{height: job.height, err: err}
					continue
				}

				blockHash, _ := block.Hash()
				blockEntry := &lib.StateChangeEntry{
					OperationType: lib.DbOperationTypeUpsert,
					KeyBytes:      blockHash[:],
					Encoder:       block,
					BlockHeight:   job.height,
				}
				results <- blockResult{height: job.height, entry: blockEntry, err: nil}
			}
		}(i)
	}

	// Send jobs
	go func() {
		for h := startHeight; h <= endHeight; h++ {
			jobs <- blockJob{height: h}
		}
		close(jobs)
	}()

	// Close results when all workers done
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results and process in order
	blockCount := endHeight - startHeight + 1
	blocks := make(map[uint64]*lib.StateChangeEntry)
	var errors []error

	for result := range results {
		if result.err != nil {
			log.Printf("WARNING: Failed to fetch block %d: %v", result.height, result.err)
			errors = append(errors, result.err)
			continue
		}
		blocks[result.height] = result.entry
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to fetch %d blocks", len(errors))
	}

	// Process blocks in height order
	log.Printf("Fetched %d blocks, now inserting into database...", len(blocks))
	for h := startHeight; h <= endHeight; h++ {
		entry, ok := blocks[h]
		if !ok {
			return fmt.Errorf("missing block %d", h)
		}

		if err := pdh.HandleEntryBatch([]*lib.StateChangeEntry{entry}, false); err != nil {
			return fmt.Errorf("failed to process block %d: %w", h, err)
		}

		if h%1000 == 0 {
			log.Printf("Progress: %d/%d blocks processed", h-startHeight+1, blockCount)
		}
	}

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
		blockCount := gap.End - gap.Start + 1
		log.Printf("Processing gap: %d -> %d (%d blocks)", gap.Start, gap.End, blockCount)

		if err := pdh.InitiateTransaction(); err != nil {
			log.Fatalf("InitiateTransaction: %v", err)
		}

		// For small gaps, process sequentially
		// For large gaps, use parallel fetching for speed
		if blockCount <= 100 {
			// Sequential processing for small gaps
			for h := gap.Start; h <= gap.End; h++ {
				log.Printf("Processing height %d...", h)
				if err := processBlockFromAPI(nodeURL, h, pdh); err != nil {
					log.Printf("WARNING: Failed to process block %d: %v", h, err)
					continue
				}
			}
		} else {
			// Parallel processing for large gaps
			log.Printf("Using parallel processing (50 workers) for large gap...")
			if err := processGapParallel(nodeURL, gap.Start, gap.End, pdh, 50); err != nil {
				log.Fatalf("processGapParallel: %v", err)
			}
		}

		if err := pdh.CommitTransaction(); err != nil {
			log.Fatalf("CommitTransaction: %v", err)
		}
		log.Printf("Successfully repaired gap %d -> %d", gap.Start, gap.End)
	}
	log.Println("Repair completed successfully")
}
