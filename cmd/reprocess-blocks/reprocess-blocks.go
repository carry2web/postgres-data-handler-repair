package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/deso-protocol/postgres-data-handler/handler"
	"github.com/uptrace/bun"
)

func main() {
	gapFile := "../../blocks-reprocess.txt"
	log.Printf("Reprocessing blocks from %s...", gapFile)

	blockHeights, err := readBlockHeights(gapFile)
	if err != nil {
		log.Fatalf("Failed to read block heights: %v", err)
	}
	log.Printf("Loaded %d block heights", len(blockHeights))

	// Setup DB connection (reuse handler logic)
	pdh, err := handler.NewPostgresDataHandler()
	if err != nil {
		log.Fatalf("Failed to initialize data handler: %v", err)
	}

	ctx := context.Background()

	for i, height := range blockHeights {
		if i%1000 == 0 {
			log.Printf("Progress: %d/%d blocks", i, len(blockHeights))
		}

		// Load block and all related entries from DB
		block, err := pdh.LoadBlockFromDB(ctx, height)
		if err != nil {
			log.Printf("WARNING: Failed to load block %d: %v", height, err)
			continue
		}

		// Reprocess all entries for this block
		if err := pdh.ReprocessBlockEntries(ctx, block); err != nil {
			log.Printf("WARNING: Failed to reprocess block %d: %v", height, err)
			continue
		}
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
