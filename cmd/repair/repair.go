package main

import (
	"context"
	"database/sql"
	"encoding/gob"
	"fmt"
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

// processStateChangeFile reads and processes a state-change file for a specific block height.
// The state-change files are written by the DeSo node to STATE_CHANGE_DIR.
func processStateChangeFile(stateChangeDir string, height uint64, pdh *handler.PostgresDataHandler) error {
	// State change files are named: state-changes-<height>
	filePath := filepath.Join(stateChangeDir, fmt.Sprintf("state-changes-%d", height))

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("state-change file not found for height %d: %s", height, filePath)
	}

	// Open the state-change file
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open state-change file for height %d: %w", height, err)
	}
	defer file.Close()

	// Decode the state change entries using gob
	decoder := gob.NewDecoder(file)
	var stateChangeEntries []*lib.StateChangeEntry

	for {
		var entry lib.StateChangeEntry
		err := decoder.Decode(&entry)
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("failed to decode state-change entry at height %d: %w", height, err)
		}
		stateChangeEntries = append(stateChangeEntries, &entry)
	}

	if len(stateChangeEntries) == 0 {
		return fmt.Errorf("no state change entries found in file for height %d", height)
	}

	// Group entries by encoder type for batch processing
	entryBatches := make(map[lib.EncoderType][]*lib.StateChangeEntry)
	for _, entry := range stateChangeEntries {
		entryBatches[entry.EncoderType] = append(entryBatches[entry.EncoderType], entry)
	}

	// Process each batch using the PostgresDataHandler
	for encoderType, batch := range entryBatches {
		if err := pdh.HandleEntryBatch(batch, false); err != nil {
			return fmt.Errorf("failed to process batch (encoder type %v) for height %d: %w", encoderType, height, err)
		}
	}

	log.Printf("Successfully processed %d state-change entries for height %d", len(stateChangeEntries), height)
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

	// Get state change directory
	stateChangeDir := viper.GetString("STATE_CHANGE_DIR")
	if stateChangeDir == "" {
		log.Fatalf("STATE_CHANGE_DIR must be set in config or environment")
	}
	log.Printf("Using state change directory: %s", stateChangeDir)

	// Set the state change dir flag that core uses, so DeSoEncoders properly encode and decode state change metadata
	viper.Set("state-change-dir", stateChangeDir)

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
		log.Printf("Gap: %d -> %d", g.Start, g.End)
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

			// Read and process the state-change file for this height
			if err := processStateChangeFile(stateChangeDir, h, pdh); err != nil {
				log.Printf("WARNING: Failed to process state-change file for height %d: %v", h, err)
				log.Printf("Ensure the state-change file exists at: %s/state-changes-%d", stateChangeDir, h)
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
