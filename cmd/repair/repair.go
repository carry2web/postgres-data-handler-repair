package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
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
	type gapRow struct{ StartHeight, EndHeight, MissingCount uint64 }
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
),
internal_gaps AS (
  SELECT
    height + 1        AS start_height,
    next_height - 1   AS end_height,
    (next_height - height - 1) AS missing_count
  FROM sequenced
  WHERE next_height IS NOT NULL
    AND next_height > height + 1
),
first_gap AS (
  SELECT
    0::bigint AS start_height,
    (SELECT MIN(height) - 1 FROM ordered) AS end_height,
    (SELECT MIN(height) FROM ordered) AS missing_count
  WHERE (SELECT MIN(height) FROM ordered) > 0
)
SELECT start_height, end_height, missing_count FROM internal_gaps
UNION ALL
SELECT start_height, end_height, missing_count FROM first_gap
ORDER BY start_height;
	`
	err := db.NewRaw(query).Scan(context.Background(), &rows)
	if err != nil {
		return nil, fmt.Errorf("detectGaps query failed: %w", err)
	}
	var gaps []Gap
	for _, r := range rows {
		gaps = append(gaps, Gap{Start: r.StartHeight, End: r.EndHeight})
	}
	return gaps, nil
}

// fetchBlockByHeight fetches a block from the DeSo node by height using the /api/v1/block endpoint.
// Returns the block and its hash (from the API, not computed).
func fetchBlockByHeight(nodeURL string, height uint64) (*lib.MsgDeSoBlock, *lib.BlockHash, error) {
	url := fmt.Sprintf("%s/api/v1/block", nodeURL)
	body, err := json.Marshal(map[string]interface{}{
		"Height":    height,
		"FullBlock": true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal block request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse APIBlockResponse format
	// The API returns hashes as hex strings in specific field names
	var apiResult struct {
		Header struct {
			BlockHashHex                 string `json:"BlockHashHex"`
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
		return nil, nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if apiResult.Error != "" {
		return nil, nil, fmt.Errorf("API error: %s", apiResult.Error)
	}

	// Decode hex strings to BlockHash
	prevBlockHash, err := decodeBlockHash(apiResult.Header.PrevBlockHashHex)
	if err != nil {
		return nil, nil, fmt.Errorf("decode prev block hash: %w", err)
	}

	txnMerkleRoot, err := decodeBlockHash(apiResult.Header.TransactionMerkleRootHex)
	if err != nil {
		return nil, nil, fmt.Errorf("decode txn merkle root: %w", err)
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

	// For PoS blocks, the API returns BLS fields as base64-encoded strings
	// We need to decode them, but for now we'll use a simpler approach:
	// Just fetch the raw block bytes from a different endpoint or skip BLS validation
	// The state-consumer doesn't validate block hashes, it just stores them
	// So we can leave BLS fields nil and use the block hash from the API response

	// Decode transactions from hex
	txns := make([]*lib.MsgDeSoTxn, len(apiResult.Transactions))
	for i, txData := range apiResult.Transactions {
		txnBytes, err := hex.DecodeString(txData.RawTransactionHex)
		if err != nil {
			return nil, nil, fmt.Errorf("decode transaction %d hex: %w", i, err)
		}

		txn := &lib.MsgDeSoTxn{}
		if err := txn.FromBytes(txnBytes); err != nil {
			return nil, nil, fmt.Errorf("parse transaction %d bytes: %w", i, err)
		}
		txns[i] = txn
	}

	block := &lib.MsgDeSoBlock{
		Header: header,
		Txns:   txns,
	}

	// Return the block hash from the API (don't compute it, as that requires BLS fields for PoS blocks)
	blockHash, err := decodeBlockHash(apiResult.Header.BlockHashHex)
	if err != nil {
		return nil, nil, fmt.Errorf("decode block hash from API: %w", err)
	}

	return block, blockHash, nil
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
	block, blockHash, err := fetchBlockByHeight(nodeURL, height)
	if err != nil {
		return err
	}

	// Use the block hash from the API (don't compute it)
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

// openStateChangeFiles opens both the index and data files for reading
func openStateChangeFiles(stateChangeDir string) (*os.File, *os.File, error) {
	indexPath := filepath.Join(stateChangeDir, lib.StateChangeIndexFileName)
	dataPath := filepath.Join(stateChangeDir, lib.StateChangeFileName)

	indexFile, err := os.Open(indexPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open index file %s: %w", indexPath, err)
	}

	dataFile, err := os.Open(dataPath)
	if err != nil {
		indexFile.Close()
		return nil, nil, fmt.Errorf("failed to open data file %s: %w", dataPath, err)
	}

	return indexFile, dataFile, nil
}

// readBlockFromStateChange reads a StateChangeEntry for a specific block height from state-change files
func readBlockFromStateChange(indexFile, dataFile *os.File, height uint64) (*lib.StateChangeEntry, error) {
	// Read the byte position from the index file
	// Index file stores uint64 at position (height * 8)
	entryIndexBytes := make([]byte, 8)
	fileBytesPosition := int64(height * 8)

	bytesRead, err := indexFile.ReadAt(entryIndexBytes, fileBytesPosition)
	if err != nil {
		return nil, fmt.Errorf("failed to read index at height %d: %w", height, err)
	}
	if bytesRead != 8 {
		return nil, fmt.Errorf("expected to read 8 bytes from index, got %d", bytesRead)
	}

	// Decode the byte position in the data file
	dbIndex := binary.LittleEndian.Uint64(entryIndexBytes)

	// Seek to the position in the data file
	if _, err := dataFile.Seek(int64(dbIndex), io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to position %d in data file: %w", dbIndex, err)
	}

	// Read the entry length (uvarint)
	bufReader := bufio.NewReader(dataFile)
	entryLength, err := lib.ReadUvarint(bufReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read entry length at height %d: %w", height, err)
	}

	// Read the entry bytes
	entryBytes := make([]byte, entryLength)
	if _, err := io.ReadFull(bufReader, entryBytes); err != nil {
		return nil, fmt.Errorf("failed to read entry bytes at height %d: %w", height, err)
	}

	// Decode the entry
	entry := &lib.StateChangeEntry{}
	rr := bytes.NewReader(entryBytes)
	if _, err := lib.DecodeFromBytes(entry, rr); err != nil {
		return nil, fmt.Errorf("failed to decode entry at height %d: %w", height, err)
	}

	return entry, nil
}

// processGapFromStateChange processes a gap by reading directly from state-change files
func processGapFromStateChange(stateChangeDir string, startHeight, endHeight uint64, pdh *handler.PostgresDataHandler) error {
	log.Printf("Opening state-change files from %s", stateChangeDir)

	indexFile, dataFile, err := openStateChangeFiles(stateChangeDir)
	if err != nil {
		return fmt.Errorf("failed to open state-change files: %w", err)
	}
	defer indexFile.Close()
	defer dataFile.Close()

	log.Printf("Processing blocks %d -> %d from state-change files", startHeight, endHeight)

	totalBlocks := endHeight - startHeight + 1

	// We need to scan sequentially through entries to find blocks in our range
	// The index file is by entry number, not by block height
	// Start from the first entry at startHeight and scan forward
	currentEntryIndex := startHeight
	blocksFound := uint64(0)

	for blocksFound < totalBlocks {
		entry, err := readBlockFromStateChange(indexFile, dataFile, currentEntryIndex)
		if err != nil {
			return fmt.Errorf("failed to read entry at index %d: %w", currentEntryIndex, err)
		}

		// Only process block entries in our height range
		if entry.EncoderType == lib.EncoderTypeBlock &&
			entry.BlockHeight >= startHeight &&
			entry.BlockHeight <= endHeight {

			// Change operation type from Insert to Upsert for repair operations
			entry.OperationType = lib.DbOperationTypeUpsert

			// Process the entry
			if err := pdh.HandleEntryBatch([]*lib.StateChangeEntry{entry}, false); err != nil {
				return fmt.Errorf("failed to process block at height %d: %w", entry.BlockHeight, err)
			}

			blocksFound++
			if blocksFound%1000 == 0 {
				progress := float64(blocksFound) / float64(totalBlocks) * 100
				log.Printf("Progress: %d/%d blocks (%.2f%%)", blocksFound, totalBlocks, progress)
			}
		}

		currentEntryIndex++

		// Safety: don't scan forever if we can't find all blocks
		if currentEntryIndex > endHeight+1000000 {
			return fmt.Errorf("scanned 1M entries past end height without finding all blocks (found %d/%d)", blocksFound, totalBlocks)
		}
	}

	log.Printf("Successfully processed %d blocks from state-change files", blocksFound)
	return nil
}

// processGapParallel fetches and processes blocks in parallel using streaming batches
func processGapParallel(nodeURL string, startHeight, endHeight uint64, pdh *handler.PostgresDataHandler, workers int) error {
	type blockJob struct {
		height uint64
	}
	type blockResult struct {
		height uint64
		entry  *lib.StateChangeEntry
		err    error
	}

	totalBlocks := endHeight - startHeight + 1
	fetchBatchSize := uint64(50000)  // Fetch 50k blocks at a time
	commitBatchSize := uint64(10000) // Commit every 10k blocks

	blocksCommitted := uint64(0)

	// Process in fetch batches for better progress visibility
	for batchStart := startHeight; batchStart <= endHeight; batchStart += fetchBatchSize {
		batchEnd := batchStart + fetchBatchSize - 1
		if batchEnd > endHeight {
			batchEnd = endHeight
		}

		log.Printf("Fetching batch: heights %d -> %d", batchStart, batchEnd)

		jobs := make(chan blockJob, workers*2)
		results := make(chan blockResult, workers*2)

		// Start workers
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				for job := range jobs {
					block, blockHash, err := fetchBlockByHeight(nodeURL, job.height)
					if err != nil {
						results <- blockResult{height: job.height, err: err}
						continue
					}

					// Use the block hash from the API (don't compute it)
					blockEntry := &lib.StateChangeEntry{
						OperationType: lib.DbOperationTypeUpsert,
						EncoderType:   lib.EncoderTypeBlock,
						KeyBytes:      blockHash[:],
						Encoder:       block,
						BlockHeight:   job.height,
					}
					results <- blockResult{height: job.height, entry: blockEntry, err: nil}
				}
			}(i)
		}

		// Send jobs for this batch
		go func() {
			for h := batchStart; h <= batchEnd; h++ {
				jobs <- blockJob{height: h}
			}
			close(jobs)
		}()

		// Close results when all workers done
		go func() {
			wg.Wait()
			close(results)
		}()

		// Collect results for this batch
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
			return fmt.Errorf("failed to fetch %d blocks in batch %d->%d", len(errors), batchStart, batchEnd)
		}

		// Process blocks in height order with commits
		log.Printf("Processing %d fetched blocks...", len(blocks))
		for h := batchStart; h <= batchEnd; h++ {
			entry, ok := blocks[h]
			if !ok {
				return fmt.Errorf("missing block %d", h)
			}

			if err := pdh.HandleEntryBatch([]*lib.StateChangeEntry{entry}, false); err != nil {
				return fmt.Errorf("failed to process block %d: %w", h, err)
			}

			blocksCommitted++

			// Commit every commitBatchSize blocks and at the end
			if blocksCommitted%commitBatchSize == 0 || h == endHeight {
				if err := pdh.CommitTransaction(); err != nil {
					return fmt.Errorf("failed to commit at block %d: %w", h, err)
				}
				log.Printf("✓ Committed: %d/%d blocks (%.2f%%)",
					blocksCommitted, totalBlocks, float64(blocksCommitted)/float64(totalBlocks)*100)

				// Start new transaction if not at end
				if h < endHeight {
					if err := pdh.InitiateTransaction(); err != nil {
						return fmt.Errorf("failed to start new transaction at block %d: %w", h, err)
					}
				}
			} else if blocksCommitted%1000 == 0 {
				log.Printf("Progress: %d/%d blocks processed", blocksCommitted, totalBlocks)
			}
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

	// Get worker count from environment (default 100)
	workerCount := viper.GetInt("REPAIR_WORKERS")
	if workerCount == 0 {
		workerCount = 100
	}
	// Set max connections to support parallel workers
	db.SetMaxIdleConns(workerCount + 10)
	db.SetMaxOpenConns(workerCount + 20)
	log.Printf("Worker count: %d, Max DB connections: %d", workerCount, workerCount+20)

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

	// Get state-change directory (optional, defaults to /db)
	stateChangeDir := viper.GetString("STATE_CHANGE_DIR")
	if stateChangeDir == "" {
		stateChangeDir = "/db"
	}
	log.Printf("State-change directory: %s", stateChangeDir)

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

	// Check for manual range specification
	var gaps []Gap
	startHeight := viper.GetUint64("REPAIR_START_HEIGHT")
	endHeight := viper.GetUint64("REPAIR_END_HEIGHT")

	if startHeight > 0 || endHeight > 0 {
		// Manual range specified
		if startHeight == 0 {
			startHeight = 0 // Allow starting from block 0
		}
		if endHeight == 0 {
			log.Fatalf("REPAIR_END_HEIGHT must be specified when using manual range")
		}
		if startHeight > endHeight {
			log.Fatalf("REPAIR_START_HEIGHT (%d) cannot be greater than REPAIR_END_HEIGHT (%d)", startHeight, endHeight)
		}
		gaps = []Gap{{Start: startHeight, End: endHeight}}
		log.Printf("Manual repair mode: processing range %d -> %d (%d blocks)", startHeight, endHeight, endHeight-startHeight+1)
	} else {
		// Automatic gap detection
		var err error
		gaps, err = detectGaps(db)
		if err != nil {
			log.Fatalf("detectGaps: %v", err)
		}
		log.Printf("Found %d gap(s)", len(gaps))
		for _, g := range gaps {
			log.Printf("Gap: %d -> %d (%d blocks)", g.Start, g.End, g.End-g.Start+1)
		}
	}

	// Process each gap
	for _, gap := range gaps {
		blockCount := gap.End - gap.Start + 1
		log.Printf("Processing gap: %d -> %d (%d blocks)", gap.Start, gap.End, blockCount)

		// Skip verification check if in manual mode (allow forced re-processing)
		if startHeight == 0 && endHeight == 0 {
			// Auto-detect mode: verify the gap actually exists by checking a sample block
			count, err := db.NewSelect().
				Table("block").
				Where("height = ?", gap.Start).
				Count(context.Background())
			if err == nil && count > 0 {
				log.Printf("WARNING: Block %d already exists in database (%d records), skipping gap. This may indicate duplicate heights.", gap.Start, count)
				continue
			}
		}

		if err := pdh.InitiateTransaction(); err != nil {
			log.Fatalf("InitiateTransaction: %v", err)
		}

		// Hybrid strategy:
		// - Small gaps (≤100 blocks): Sequential API calls
		// - Medium/Large gaps (>100 blocks): Parallel API calls (50 workers)
		// Note: State-consumer file reading is not suitable for random-access repair
		// because entry indices don't correlate with block heights
		if blockCount <= 100 {
			// Sequential processing for small gaps
			log.Printf("Using sequential API processing for small gap...")
			for h := gap.Start; h <= gap.End; h++ {
				log.Printf("Processing height %d...", h)
				if err := processBlockFromAPI(nodeURL, h, pdh); err != nil {
					log.Printf("WARNING: Failed to process block %d: %v", h, err)
					continue
				}
			}
		} else {
			// Parallel API processing for medium and large gaps
			log.Printf("Using parallel API processing (%d workers) for gap...", workerCount)
			if err := processGapParallel(nodeURL, gap.Start, gap.End, pdh, workerCount); err != nil {
				log.Fatalf("processGapParallel: %v", err)
			}
			// Transaction is committed inside processGapParallel in batches
		}

		// For small gaps, commit here (large gaps commit inside processGapParallel)
		if blockCount <= 100 {
			if err := pdh.CommitTransaction(); err != nil {
				log.Fatalf("CommitTransaction: %v", err)
			}
		}

		log.Printf("Successfully repaired gap %d -> %d", gap.Start, gap.End)
	}
	log.Println("Repair completed successfully")
}
