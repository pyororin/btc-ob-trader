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

// StreamMarketEventsFromCSV reads order book data from a CSV file and streams it as MarketEvent
// instances through a channel. It groups updates by timestamp to form snapshots.
// The function returns a channel for events and a channel for errors.
// The CSV file is expected to have a header and the following columns:
// time, pair, side, price, size, is_snapshot
func StreamMarketEventsFromCSV(ctx context.Context, filePath string) (<-chan MarketEvent, <-chan error) {
	eventCh := make(chan MarketEvent)
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

		flushSnapshot := func() {
			if len(currentUpdates) == 0 {
				return
			}
			var bids, asks [][]string
			for _, u := range currentUpdates {
				level := []string{fmt.Sprintf("%f", u.Price), fmt.Sprintf("%f", u.Size)}
				if u.Side == "bid" {
					bids = append(bids, level)
				} else {
					asks = append(asks, level)
				}
			}

			select {
			case eventCh <- OrderBookEvent{
				OrderBook: coincheck.OrderBookData{
					PairStr: currentPair,
					Bids:    bids,
					Asks:    asks,
				},
				Time: currentTime,
			}:
				totalSnapshots++
			case <-ctx.Done():
				return
			}
		}

		for {
			record, err := reader.Read()
			if err == io.EOF {
				break
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

		// Flush the last snapshot
		flushSnapshot()

		logger.Infof("Successfully streamed %d order book snapshots from %s", totalSnapshots, filePath)
	}()

	return eventCh, errCh
}

func parseTime(timeStr string) (time.Time, error) {
	// First, try RFC3339 format
	t, err := time.Parse(time.RFC3339, timeStr)
	if err == nil {
		return t, nil
	}
	// Fallback to psql's default format with space and timezone
	// e.g., "2025-07-14 04:11:13.484971+00"
	layout := "2006-01-02 15:04:05.999999-07"
	t, err = time.Parse(layout, timeStr)
	if err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("could not parse time '%s' with any known format", timeStr)
}
