package main

import (
	"encoding/csv"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"song-recognition/spotify"
	"strconv"
	"strings"
	"time"
)

func checkAvailability(csvPath, browser string, delaySeconds, jitterSeconds int) {
	f, err := os.Open(csvPath)
	if err != nil {
		fmt.Printf("Error opening CSV: %v\n", err)
		return
	}
	defer f.Close()

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		fmt.Printf("Error reading CSV: %v\n", err)
		return
	}

	if len(records) == 0 {
		fmt.Println("CSV file is empty")
		return
	}

	// Skip header row if first field is non-numeric
	startIdx := 0
	if _, err := strconv.ParseUint(strings.TrimSpace(records[0][0]), 10, 32); err != nil {
		startIdx = 1
	}
	records = records[startIdx:]

	if len(records) == 0 {
		fmt.Println("No records to process")
		return
	}
	total := len(records)

	unavailablePath := strings.TrimSuffix(csvPath, filepath.Ext(csvPath)) + "_check_unavailable.txt"
	unavailableFile, err := os.OpenFile(unavailablePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Warning: could not create unavailable log: %v\n", err)
		unavailableFile = nil
	}
	if unavailableFile != nil {
		defer unavailableFile.Close()
	}

	okCount := 0
	unavailableCount := 0
	errorCount := 0
	var lastCheckTime time.Time

	for i, record := range records {
		if len(record) < 2 {
			fmt.Printf("[%d/%d] Skipping invalid row\n", i+1, total)
			errorCount++
			continue
		}

		songIDStr := strings.TrimSpace(record[0])
		ytID := strings.TrimSpace(record[1])

		if !lastCheckTime.IsZero() {
			jitter := rand.Intn(jitterSeconds + 1)
			target := time.Duration(delaySeconds+jitter) * time.Second
			if elapsed := time.Since(lastCheckTime); elapsed < target {
				time.Sleep(target - elapsed)
			}
		}
		lastCheckTime = time.Now()

		fmt.Printf("[%d/%d] song_id=%-8s ytID=%-15s ", i+1, total, songIDStr, ytID)

		err := spotify.CheckVideoAvailability(ytID, browser)
		if err == nil {
			fmt.Println("OK")
			okCount++
			continue
		}

		var reason string
		switch err {
		case spotify.ErrVideoUnavailable:
			reason = "video unavailable"
		case spotify.ErrVideoNotAvailable:
			reason = "video not available"
		case spotify.ErrPrivateVideo:
			reason = "private video"
		case spotify.ErrVideoViolation:
			reason = "violating YouTube"
		case spotify.ErrConfirmAge:
			reason = "age confirmation required"
		case spotify.ErrFormatNotAvailable:
			reason = "format not available"
		default:
			reason = err.Error()
		}

		fmt.Printf("UNAVAILABLE (%s)\n", reason)
		unavailableCount++
		if unavailableFile != nil {
			fmt.Fprintf(unavailableFile, "%s\n", songIDStr)
		}
	}

	fmt.Printf("\nCheck complete: %d OK, %d unavailable, %d invalid rows\n",
		okCount, unavailableCount, errorCount)
	if unavailableCount > 0 {
		fmt.Printf("Unavailable IDs written to: %s\n", unavailablePath)
	}
}
