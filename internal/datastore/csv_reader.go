package datastore

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

// StreamMarketEventsFromCSV reads order book data from a CSV file and streams it as coincheck.OrderBookData
// instances through a channel. It groups updates by timestamp to form snapshots.
// The function returns a channel for events and a channel for errors.
// The CSV file is expected to have a header and the following columns:
// time, pair, side, price, size, is_snapshot
func StreamMarketEventsFromCSV(ctx context.Context, filePath string) (<-chan coincheck.OrderBookData, <-chan error) {
	eventCh := make(chan coincheck.OrderBookData)
	errCh := make(chan error, 1)

	go func() {
		defer close(eventCh)
		defer close(errCh)

		file, err := os.Open(filePath)
		if err != nil {
			errCh <- fmt.Errorf("failed to open csv file: %w", err)
			return
		}
		defer file.Close()

		reader := csv.NewReader(file)
		// Read the header row
		if _, err := reader.Read(); err != nil {
			if err != io.EOF {
				errCh <- fmt.Errorf("failed to read csv header: %w", err)
			}
			return // Empty file is not an error
		}

		var currentUpdates []coincheck.OrderBookLevel
		var currentTime time.Time
		var currentPair string
		var totalSnapshots int

		const timeLayout = "2006-01-02 15:04:05.999999-07"
		flushSnapshot := func() {
			if len(currentUpdates) == 0 {
				return
			}

			bidCount := 0
			askCount := 0
			for _, u := range currentUpdates {
				if u.Side == "bid" {
					bidCount++
				} else {
					askCount++
				}
			}

			bids := make([][]string, 0, bidCount)
			asks := make([][]string, 0, askCount)

			for _, u := range currentUpdates {
				level := []string{
					strconv.FormatFloat(u.Price, 'f', -1, 64),
					strconv.FormatFloat(u.Size, 'f', -1, 64),
				}
				if u.Side == "bid" {
					bids = append(bids, level)
				} else {
					asks = append(asks, level)
				}
			}

			select {
			case eventCh <- coincheck.OrderBookData{
				PairStr: currentPair,
				Bids:    bids,
				Asks:    asks,
				Time:    currentTime,
			}:
				totalSnapshots++
			case <-ctx.Done():
				return
			}
		}

		for {
			select {
			case <-ctx.Done():
				logger.Info("CSV streaming cancelled by context.")
				return
			default:
				record, err := reader.Read()
				if err == io.EOF {
					flushSnapshot()
					logger.Infof("Successfully streamed %d order book snapshots from %s", totalSnapshots, filePath)
					return // End of file, successful completion
				}
				if err != nil {
					errCh <- fmt.Errorf("failed to read csv record: %w", err)
					return
				}

				if len(record) != 6 {
					logger.Warnf("Skipping record due to invalid number of columns: expected 6, got %d", len(record))
					continue
				}

				eventTime, err := parseTime(record[0])
				if err != nil {
					logger.Warnf("Skipping record due to time parse error: %v", err)
					continue
				}

				if !currentTime.IsZero() && eventTime != currentTime {
					flushSnapshot()
					currentUpdates = nil
				}

				currentTime = eventTime
				currentPair = record[1]
				side := record[2]
				price, err := strconv.ParseFloat(record[3], 64)
				if err != nil {
					logger.Warnf("Skipping record due to price parse error: %v", err)
					continue
				}
				size, err := strconv.ParseFloat(record[4], 64)
				if err != nil {
					logger.Warnf("Skipping record due to size parse error: %v", err)
					continue
				}

				currentUpdates = append(currentUpdates, coincheck.OrderBookLevel{
					Side:  side,
					Price: price,
					Size:  size,
				})
			}
		}
	}()

	return eventCh, errCh
}

func parseTime(timeStr string) (time.Time, error) {
	// The format is assumed to be consistent based on export logic.
	// e.g., "2025-07-14 04:11:13.484971+00"
	const layout = "2006-01-02 15:04:05.999999-07"
	t, err := time.Parse(layout, timeStr)
	if err != nil {
		// Fallback for safety, though it shouldn't be hit if data is consistent.
		t, err = time.Parse(time.RFC3339, timeStr)
		if err != nil {
			return time.Time{}, fmt.Errorf("could not parse time '%s' with any known format", timeStr)
		}
	}
	return t, nil
}

// LoadMarketEventsFromCSV reads an entire CSV file into memory and returns it as a slice of OrderBookData.
func LoadMarketEventsFromCSV(filePath string) ([]coincheck.OrderBookData, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open csv file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	// Read the header row
	if _, err := reader.Read(); err != nil {
		if err == io.EOF {
			return []coincheck.OrderBookData{}, nil // Empty file is okay
		}
		return nil, fmt.Errorf("failed to read csv header: %w", err)
	}

	var events []coincheck.OrderBookData
	var currentUpdates []coincheck.OrderBookLevel
	var currentTime time.Time
	var currentPair string

	flushSnapshot := func() {
		if len(currentUpdates) == 0 {
			return
		}

		bidCount := 0
		askCount := 0
		for _, u := range currentUpdates {
			if u.Side == "bid" {
				bidCount++
			} else {
				askCount++
			}
		}

		bids := make([][]string, 0, bidCount)
		asks := make([][]string, 0, askCount)

		for _, u := range currentUpdates {
			level := []string{
				strconv.FormatFloat(u.Price, 'f', -1, 64),
				strconv.FormatFloat(u.Size, 'f', -1, 64),
			}
			if u.Side == "bid" {
				bids = append(bids, level)
			} else {
				asks = append(asks, level)
			}
		}

		events = append(events, coincheck.OrderBookData{
			PairStr: currentPair,
			Bids:    bids,
			Asks:    asks,
			Time:    currentTime,
		})
	}

	for {
		record, err := reader.Read()
		if err == io.EOF {
			flushSnapshot()
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read csv record: %w", err)
		}

		if len(record) != 6 {
			logger.Warnf("Skipping record due to invalid number of columns: expected 6, got %d", len(record))
			continue
		}

		eventTime, err := parseTime(record[0])
		if err != nil {
			logger.Warnf("Skipping record due to time parse error: %v", err)
			continue
		}

		if !currentTime.IsZero() && eventTime != currentTime {
			flushSnapshot()
			currentUpdates = nil
		}

		currentTime = eventTime
		currentPair = record[1]
		side := record[2]
		price, err := strconv.ParseFloat(record[3], 64)
		if err != nil {
			logger.Warnf("Skipping record due to price parse error: %v", err)
			continue
		}
		size, err := strconv.ParseFloat(record[4], 64)
		if err != nil {
			logger.Warnf("Skipping record due to size parse error: %v", err)
			continue
		}

		currentUpdates = append(currentUpdates, coincheck.OrderBookLevel{
			Side:  side,
			Price: price,
			Size:  size,
		})
	}

	return events, nil
}
