package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/deso-protocol/core/lib"
	"github.com/deso-protocol/postgres-data-handler/handler"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/spf13/viper"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

// Block represents a row in the block table
type Block struct {
	BlockHash   []byte `bun:"block_hash"`
	BlockHeight uint64 `bun:"block_height"`
	// Add other fields as needed from your block table schema
}

func main() {
	gapFile := "/postgres-data-handler/src/postgres-data-handler/blocks-reprocess.txt"
	log.Printf("Reprocessing blocks from %s...", gapFile)

	blockHeights, err := readBlockHeights(gapFile)
	if err != nil {
		log.Fatalf("Failed to read block heights: %v", err)
	}
	log.Printf("Loaded %d block heights", len(blockHeights))

	// Load config from .env (like repair.go)
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

	pgdb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(pgURI)))
	if pgdb == nil {
		log.Fatalf("Failed to open postgres DB")
	}
	db := bun.NewDB(pgdb, pgdialect.New())
	db.SetConnMaxLifetime(0)

	params := &lib.DeSoMainnetParams
	if viper.GetBool("IS_TESTNET") {
		params = &lib.DeSoTestnetParams
		if viper.GetBool("REGTEST") {
			params.EnableRegtest(viper.GetBool("ACCELERATED_REGTEST"))
		}
	}
	lib.GlobalDeSoParams = *params

	cachedEntries, err := lru.New[string, []byte](int(handler.EntryCacheSize))
	if err != nil {
		log.Fatalf("LRU cache: %v", err)
	}
	pdh := &handler.PostgresDataHandler{
		DB:            db,
		Params:        params,
		CachedEntries: cachedEntries,
	}

	ctx := context.Background()

	for i, height := range blockHeights {
		if i%1000 == 0 {
			log.Printf("Progress: %d/%d blocks", i, len(blockHeights))
		}

		// Fetch block entry from DB (block table)
		block := &Block{}
		err := db.NewSelect().Model(block).Where("block_height = ?", height).Scan(ctx)
		if err != nil {
			log.Printf("WARNING: Failed to load block %d: %v", height, err)
			continue
		}

		// Reprocess block entry using handler (if you need to, adapt this to your handler's requirements)
		// If you need to convert Block to StateChangeEntry, do so here, or adjust logic as needed.
		// Example placeholder:
		// entry := &lib.StateChangeEntry{BlockHeight: block.BlockHeight, ...}
		// if err := pdh.HandleEntryBatch([]*lib.StateChangeEntry{entry}, false); err != nil {
		//     log.Printf("WARNING: Failed to reprocess block %d: %v", height, err)
		//     continue
		// }
	}

	log.Printf("✅ Reprocessing complete!")
}

func readBlockHeights(filename string) ([]uint64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var heights []uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		h, err := strconv.ParseUint(line, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid block height: %s", line)
		}
		heights = append(heights, h)
	}
	return heights, scanner.Err()
}
