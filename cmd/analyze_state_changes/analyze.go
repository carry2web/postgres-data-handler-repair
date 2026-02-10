package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

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

	// Create log file
	logFilePath := filepath.Join(stateChangeDir, "state-changes-analysis.log")
	logFile, err := os.Create(logFilePath)
	if err != nil {
		log.Fatalf("Failed to create log file %s: %v", logFilePath, err)
	}
	defer logFile.Close()

	// Setup multi-writer to write to both stdout and file
	multiWriter := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(multiWriter)

	startTime := time.Now()

	log.Printf("=== State-Changes Gap Analysis Started at %s ===", startTime.Format(time.RFC3339))
	log.Printf("Analyzing state-changes in: %s", stateChangeDir)
	log.Printf("Log file: %s", logFilePath)

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

	totalEntries := uint64(indexStat.Size() / 8)
	log.Printf("Total entries in index: %d", totalEntries)

	// Scan all entries to find block heights
	log.Printf("Scanning entries for block heights...")
	log.Printf("")

	blockHeights := make(map[uint64]uint64) // height -> entry index
	var maxHeight uint64
	var minHeight uint64 = ^uint64(0)
	blockCount := 0
	lastLoggedBlock := uint64(0)
	progressInterval := uint64(1000000) // Log every 1 million blocks

	for entryIdx := uint64(0); entryIdx < totalEntries; entryIdx++ {
		if entryIdx%100000 == 0 && entryIdx > 0 {
			pct := float64(entryIdx) / float64(totalEntries) * 100
			elapsed := time.Since(startTime)
			entriesPerSec := float64(entryIdx) / elapsed.Seconds()
			remaining := time.Duration(float64(totalEntries-entryIdx)/entriesPerSec) * time.Second

			log.Printf("Progress: %d/%d entries (%.2f%%) - %d blocks found - Elapsed: %v - ETA: %v",
				entryIdx, totalEntries, pct, blockCount, elapsed.Round(time.Second), remaining.Round(time.Second))

			// Check if we've found another million blocks
			blocksFoundSinceLastLog := blockCount - int(lastLoggedBlock)
			if blocksFoundSinceLastLog >= int(progressInterval) {
				log.Printf("  └─ Milestone: Found %d blocks (total: %d)", progressInterval, blockCount)
				lastLoggedBlock = uint64(blockCount)
			}
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

	expectedBlocks := maxHeight - minHeight + 1
	missingBlocks := expectedBlocks - uint64(blockCount)
	log.Printf("Expected blocks (continuous range): %d", expectedBlocks)
	log.Printf("Missing blocks: %d (%.2f%%)", missingBlocks, float64(missingBlocks)/float64(expectedBlocks)*100)

	log.Printf("\n=== Checking for gaps ===")
	log.Printf("Scanning height range %d to %d...", minHeight, maxHeight)

	// Sort heights
	heights := make([]uint64, 0, len(blockHeights))
	for h := range blockHeights {
		heights = append(heights, h)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })

	gaps := []struct{ Start, End, Missing uint64 }{}
	totalMissingInGaps := uint64(0)

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
			missing := gapEnd - gapStart + 1
			gaps = append(gaps, struct{ Start, End, Missing uint64 }{gapStart, gapEnd, missing})
			totalMissingInGaps += missing
		}
	}

	if len(gaps) == 0 {
		log.Printf("✓ No gaps found! State-changes file is complete.")
	} else {
		log.Printf("✗ Found %d gaps (total %d blocks missing)", len(gaps), totalMissingInGaps)
		log.Printf("")

		// Write gaps to separate file for easy analysis
		gapsFilePath := filepath.Join(stateChangeDir, "state-changes-gaps-detailed.txt")
		gapsFile, err := os.Create(gapsFilePath)
		if err != nil {
			log.Printf("Warning: Could not create gaps file: %v", err)
		} else {
			defer gapsFile.Close()
			fmt.Fprintf(gapsFile, "=== State-Changes Gaps Analysis ===\n")
			fmt.Fprintf(gapsFile, "Total gaps: %d\n", len(gaps))
			fmt.Fprintf(gapsFile, "Total missing blocks: %d\n\n", totalMissingInGaps)

			for i, gap := range gaps {
				line := fmt.Sprintf("Gap %d: heights %d -> %d (%d blocks missing)\n", i+1, gap.Start, gap.End, gap.Missing)
				fmt.Fprint(gapsFile, line)

				// Only print first 100 and last 100 gaps to console
				if i < 100 || i >= len(gaps)-100 {
					log.Printf("  %s", line[:len(line)-1]) // Remove trailing newline
				} else if i == 100 {
					log.Printf("  ... (%d more gaps omitted from console, see %s) ...", len(gaps)-200, gapsFilePath)
				}
			}
			log.Printf("")
			log.Printf("Complete gap list written to: %s", gapsFilePath)
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

	// Final summary
	elapsed := time.Since(startTime)
	log.Printf("\n=== Analysis Complete ===")
	log.Printf("Total time: %v", elapsed.Round(time.Second))
	log.Printf("Entries scanned: %d", totalEntries)
	log.Printf("Blocks found: %d", blockCount)
	log.Printf("Gaps found: %d", len(gaps))
	log.Printf("Log saved to: %s", logFilePath)
	log.Printf("Finished at: %s", time.Now().Format(time.RFC3339))
}
