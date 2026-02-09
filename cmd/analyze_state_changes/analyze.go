package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/deso-protocol/core/lib"
	"github.com/spf13/viper"
)

type BlockHeightInfo struct {
	EntryIndex uint64
	Height     uint64
}

func main() {
	// Load config
	viper.SetConfigFile(".env")
	viper.ReadInConfig()
	viper.AutomaticEnv()

	stateChangeDir := viper.GetString("STATE_CHANGE_DIR")
	if stateChangeDir == "" {
		stateChangeDir = "/tmp/state-changes"
	}

	log.Printf("Analyzing state-changes in: %s", stateChangeDir)

	// Open files
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

	// Get file sizes
	indexStat, _ := indexFile.Stat()
	dataStat, _ := dataFile.Stat()
	log.Printf("Index file size: %d bytes", indexStat.Size())
	log.Printf("Data file size: %d bytes", dataStat.Size())

	totalEntries := indexStat.Size() / 8
	log.Printf("Total entries in index: %d", totalEntries)

	// Scan all entries to find block heights
	log.Printf("Scanning entries for block heights...")

	blockHeights := make(map[uint64]uint64) // height -> entry index
	var maxHeight uint64
	var minHeight uint64 = ^uint64(0)
	blockCount := 0

	for entryIdx := uint64(0); entryIdx < uint64(totalEntries); entryIdx++ {
		if entryIdx%100000 == 0 && entryIdx > 0 {
			log.Printf("Progress: %d/%d entries scanned (%d blocks found)", entryIdx, totalEntries, blockCount)
		}

		// Read index entry
		entryIndexBytes := make([]byte, 8)
		fileBytesPosition := int64(entryIdx * 8)

		bytesRead, err := indexFile.ReadAt(entryIndexBytes, fileBytesPosition)
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("Warning: Failed to read index at %d: %v", entryIdx, err)
			continue
		}
		if bytesRead != 8 {
			continue
		}

		dbIndex := binary.LittleEndian.Uint64(entryIndexBytes)

		// Seek to data position
		if _, err := dataFile.Seek(int64(dbIndex), io.SeekStart); err != nil {
			log.Printf("Warning: Failed to seek to %d: %v", dbIndex, err)
			continue
		}

		// Read entry
		bufReader := bufio.NewReader(dataFile)
		entryLength, err := lib.ReadUvarint(bufReader)
		if err != nil {
			continue
		}

		entryBytes := make([]byte, entryLength)
		if _, err := io.ReadFull(bufReader, entryBytes); err != nil {
			continue
		}

		// Decode entry
		entry := &lib.StateChangeEntry{}
		rr := bytes.NewReader(entryBytes)
		if _, err := lib.DecodeFromBytes(entry, rr); err != nil {
			continue
		}

		// Only track block entries
		if entry.EncoderType == lib.EncoderTypeBlock {
			blockHeights[entry.BlockHeight] = entryIdx
			blockCount++

			if entry.BlockHeight > maxHeight {
				maxHeight = entry.BlockHeight
			}
			if entry.BlockHeight < minHeight {
				minHeight = entry.BlockHeight
			}
		}
	}

	log.Printf("\n=== Analysis Results ===")
	log.Printf("Total blocks found: %d", blockCount)
	log.Printf("Min block height: %d", minHeight)
	log.Printf("Max block height: %d", maxHeight)
	log.Printf("Expected blocks: %d", maxHeight-minHeight+1)

	// Find gaps
	log.Printf("\n=== Checking for gaps ===")

	// Sort heights
	heights := make([]uint64, 0, len(blockHeights))
	for h := range blockHeights {
		heights = append(heights, h)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })

	gaps := []struct{ Start, End uint64 }{}

	// Check from min to max
	for h := minHeight; h <= maxHeight; h++ {
		if _, exists := blockHeights[h]; !exists {
			// Found missing height, find the end of this gap
			gapStart := h
			for h <= maxHeight {
				if _, exists := blockHeights[h]; exists {
					break
				}
				h++
			}
			gapEnd := h - 1
			gaps = append(gaps, struct{ Start, End uint64 }{gapStart, gapEnd})
		}
	}

	if len(gaps) == 0 {
		log.Printf("✓ No gaps found! State-changes file is complete.")
	} else {
		log.Printf("✗ Found %d gaps:", len(gaps))
		for i, gap := range gaps {
			missing := gap.End - gap.Start + 1
			log.Printf("  Gap %d: heights %d -> %d (%d blocks missing)", i+1, gap.Start, gap.End, missing)
		}
	}

	// Show last 10 blocks
	log.Printf("\n=== Last 10 blocks in state-changes ===")
	startIdx := len(heights) - 10
	if startIdx < 0 {
		startIdx = 0
	}
	for i := startIdx; i < len(heights); i++ {
		h := heights[i]
		log.Printf("  Height %d (entry index: %d)", h, blockHeights[h])
	}
}
