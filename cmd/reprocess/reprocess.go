package main

import (
	"context"
	"database/sql"
	"encoding/binary"
	"io"
	"log"
	"os"
	"path/filepath"

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

// ReprocessBlocks reprocesses specific block heights from state-change files
// This extracts and processes ALL state changes (transactions) within those blocks
func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// Load config
	viper.SetConfigFile("config.yaml")
	viper.SetConfigType("yaml")
	viper.AutomaticEnv()
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config: %v", err)
	}

	// Database connection
	pgURI := viper.GetString("POSTGRES_URI")
	if pgURI == "" {
		log.Fatal("POSTGRES_URI not set")
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(pgURI)))
	db := bun.NewDB(sqldb, pgdialect.New())
	if viper.GetBool("LOG_DB_QUERIES") {
		db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(true)))
	}
	defer db.Close()

	log.Println("Connected to database")

	// State-change directory
	stateChangeDir := viper.GetString("STATE_CHANGE_DIR")
	if stateChangeDir == "" {
		stateChangeDir = "/db"
	}
	log.Printf("State-change directory: %s", stateChangeDir)

	// Network params
	params := &lib.DeSoMainnetParams
	if viper.GetBool("IS_TESTNET") {
		params = &lib.DeSoTestnetParams
	}
	lib.GlobalDeSoParams = *params

	// Create PostgresDataHandler
	cachedEntries, err := lru.New[string, []byte](int(handler.EntryCacheSize))
	if err != nil {
		log.Fatalf("LRU cache: %v", err)
	}
	pdh := &handler.PostgresDataHandler{
		DB:            db,
		Params:        params,
		CachedEntries: cachedEntries,
	}

	// Get block heights to reprocess
	startHeight := viper.GetUint64("REPROCESS_START_HEIGHT")
	endHeight := viper.GetUint64("REPROCESS_END_HEIGHT")

	if startHeight == 0 && endHeight == 0 {
		log.Fatal("Please specify REPROCESS_START_HEIGHT and REPROCESS_END_HEIGHT")
	}

	if endHeight == 0 {
		endHeight = startHeight
	}

	log.Printf("Reprocessing blocks %d -> %d (%d blocks)", startHeight, endHeight, endHeight-startHeight+1)
	log.Printf("This will DELETE existing block data and reprocess ALL state changes from state-change files")

	// Confirm before proceeding
	deleteExisting := viper.GetBool("DELETE_EXISTING_BLOCKS")
	if deleteExisting {
		log.Printf("Deleting existing blocks %d -> %d from database...", startHeight, endHeight)
		result, err := db.NewDelete().
			Table("block").
			Where("height >= ? AND height <= ?", startHeight, endHeight).
			Exec(context.Background())
		if err != nil {
			log.Fatalf("Failed to delete blocks: %v", err)
		}
		affected, _ := result.RowsAffected()
		log.Printf("Deleted %d block records", affected)
	} else {
		log.Printf("DELETE_EXISTING_BLOCKS=false, will attempt to process over existing data (may cause duplicates)")
	}

	// Open state-change files
	indexPath := filepath.Join(stateChangeDir, lib.StateChangeIndexFileName)
	dataPath := filepath.Join(stateChangeDir, lib.StateChangeFileName)

	indexFile, err := os.Open(indexPath)
	if err != nil {
		log.Fatalf("Failed to open index file %s: %v", indexPath, err)
	}
	defer indexFile.Close()

	dataFile, err := os.Open(dataPath)
	if err != nil {
		log.Fatalf("Failed to open data file %s: %v", dataPath, err)
	}
	defer dataFile.Close()

	log.Printf("Opened state-change files")

	// Start transaction
	if err := pdh.InitiateTransaction(); err != nil {
		log.Fatalf("InitiateTransaction: %v", err)
	}

	// Process all entries from state-change files
	// We'll scan all entries and process only those with block heights in our range
	processed := 0
	skipped := 0
	entriesProcessed := 0
	currentEntryIndex := uint64(0)

	log.Printf("Scanning state-change files for blocks in range %d -> %d", startHeight, endHeight)

	bufReader := bufio.NewReader(dataFile)

	for {
		// Read index entry (8 bytes: offset into data file, little-endian)
		indexBytes := make([]byte, 8)
		if _, err := io.ReadFull(indexFile, indexBytes); err != nil {
			if err == io.EOF {
				break
			}
			log.Fatalf("Error reading index: %v", err)
		}

		offset := binary.LittleEndian.Uint64(indexBytes[0:8])
		currentEntryIndex++

		// Read the state change entry from data file
		if _, err := dataFile.Seek(int64(offset), 0); err != nil {
			log.Fatalf("Seek error at offset %d: %v", offset, err)
		}

		// Reset buffered reader after seek
		bufReader.Reset(dataFile)

		// Read entry length (uvarint)
		entryLength, err := binary.ReadUvarint(bufReader)
		if err != nil {
			log.Printf("WARNING: Failed to read entry length at offset %d: %v", offset, err)
			continue
		}

		// Sanity check: max 10MB per entry
		if entryLength > 10*1024*1024 {
			log.Printf("WARNING: Entry too large at offset %d: %d bytes", offset, entryLength)
			continue
		}

		entryBytes := make([]byte, entryLength)
		if _, err := io.ReadFull(bufReader, entryBytes); err != nil {
			log.Printf("WARNING: Failed to read entry data: %v", err)
			continue
		}

		// Decode the state change entry
		entry := &lib.StateChangeEntry{}
		rr := bytes.NewReader(entryBytes)
		if _, err := lib.DecodeFromBytes(entry, rr); err != nil {
			log.Printf("WARNING: Failed to decode entry at offset %d: %v", offset, err)
			continue
		}

		// Get block height from decoded entry
		blockHeight := entry.BlockHeight

		// Skip entries outside our range
		if blockHeight < startHeight || blockHeight > endHeight {
			skipped++
			continue
		}

		// Process the entry through the data handler
		if err := pdh.HandleEntryBatch([]*lib.StateChangeEntry{entry}, false); err != nil {
			log.Printf("WARNING: Failed to process entry for block %d, encoder type %v: %v", blockHeight, entry.EncoderType, err)
			continue
		}

		entriesProcessed++

		// Log progress for blocks
		if entry.EncoderType == lib.EncoderTypeBlock {
			processed++
			log.Printf("Processed block %d (%d state changes processed so far)", blockHeight, entriesProcessed)
		}

		// Safety check - if we've processed all blocks in range, we're done
		if processed >= int(endHeight-startHeight+1) {
			// Keep processing until we're past the end height
			if blockHeight > endHeight {
				break
			}
		}

		// Commit periodically to avoid huge transactions
		if entriesProcessed%10000 == 0 {
			log.Printf("Committing batch after %d entries...", entriesProcessed)
			if err := pdh.CommitTransaction(); err != nil {
				log.Fatalf("CommitTransaction: %v", err)
			}
			if err := pdh.InitiateTransaction(); err != nil {
				log.Fatalf("InitiateTransaction: %v", err)
			}
		}
	}

	// Final commit
	if err := pdh.CommitTransaction(); err != nil {
		log.Fatalf("CommitTransaction: %v", err)
	}

	log.Printf("Reprocessing complete!")
	log.Printf("  Blocks processed: %d", processed)
	log.Printf("  Total state changes processed: %d", entriesProcessed)
	log.Printf("  Entries skipped (outside range): %d", skipped)
	log.Printf("  Total entries scanned: %d", currentEntryIndex)

	// Verify blocks exist in database
	for h := startHeight; h <= endHeight; h++ {
		count, err := db.NewSelect().
			Table("block").
			Where("height = ?", h).
			Count(context.Background())
		if err != nil {
			log.Printf("WARNING: Error checking block %d: %v", h, err)
		} else if count == 0 {
			log.Printf("WARNING: Block %d still missing after reprocess!", h)
		} else {
			log.Printf("✓ Block %d present in database", h)
		}
	}

	log.Println("Done!")
}
