package datastore

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/pkg/cvd"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

// MarketEvent is an interface for any event read from the simulation CSV.
type MarketEvent interface {
	GetTime() time.Time
}

// OrderBookEvent wraps OrderBookData to implement MarketEvent.
type OrderBookEvent struct {
	coincheck.OrderBookData
}

// GetTime returns the timestamp of the order book event.
func (e OrderBookEvent) GetTime() time.Time {
	return e.Time
}

// TradeEvent wraps a single trade to implement MarketEvent.
type TradeEvent struct {
	cvd.Trade
}

// GetTime returns the timestamp of the trade event.
func (e TradeEvent) GetTime() time.Time {
	return e.Timestamp
}

// StreamMarketEventsFromCSV reads market data from a CSV file and streams it as MarketEvent
// instances through a channel. It groups order book updates by timestamp to form snapshots.
// The function returns a channel for events and a channel for errors.
// The CSV file is expected to have a header and the following columns:
// time, event_type, pair, side, price, size, is_snapshot, trade_id
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
		header, err := reader.Read()
		if err != nil {
			if err != io.EOF {
				errCh <- fmt.Errorf("failed to read csv header: %w", err)
			}
			return
		}
		headerMap := make(map[string]int)
		for i, h := range header {
			headerMap[h] = i
		}

		var currentBookUpdates []coincheck.OrderBookLevel
		var currentBookTime time.Time
		var currentBookPair string
		var totalEvents int

		flushBookSnapshot := func() {
			if len(currentBookUpdates) == 0 {
				return
			}
			bids := make([][]string, 0)
			asks := make([][]string, 0)
			for _, u := range currentBookUpdates {
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
			case eventCh <- OrderBookEvent{
				OrderBookData: coincheck.OrderBookData{
					PairStr: currentBookPair,
					Bids:    bids,
					Asks:    asks,
					Time:    currentBookTime,
				},
			}:
				totalEvents++
			case <-ctx.Done():
				return
			}
			currentBookUpdates = nil
		}

		for {
			select {
			case <-ctx.Done():
				logger.Info("CSV streaming cancelled by context.")
				return
			default:
				record, err := reader.Read()
				if err == io.EOF {
					flushBookSnapshot()
					logger.Infof("Successfully streamed %d market events from %s", totalEvents, filePath)
					return
				}
				if err != nil {
					errCh <- fmt.Errorf("failed to read csv record: %w", err)
					return
				}

				eventType := record[headerMap["event_type"]]
				eventTime, err := parseTime(record[headerMap["time"]])
				if err != nil {
					logger.Warnf("Skipping record due to time parse error: %v", err)
					continue
				}

				if eventType == "book" {
					if !currentBookTime.IsZero() && eventTime != currentBookTime {
						flushBookSnapshot()
					}
					currentBookTime = eventTime
					currentBookPair = record[headerMap["pair"]]
					side := record[headerMap["side"]]
					price, _ := strconv.ParseFloat(record[headerMap["price"]], 64)
					size, _ := strconv.ParseFloat(record[headerMap["size"]], 64)
					currentBookUpdates = append(currentBookUpdates, coincheck.OrderBookLevel{
						Side:  side,
						Price: price,
						Size:  size,
					})
				} else if eventType == "trade" {
					flushBookSnapshot() // Flush any pending book updates before the trade
					price, _ := strconv.ParseFloat(record[headerMap["price"]], 64)
					size, _ := strconv.ParseFloat(record[headerMap["size"]], 64)
					trade := TradeEvent{
						Trade: cvd.Trade{
							ID:        record[headerMap["trade_id"]],
							Side:      record[headerMap["side"]],
							Price:     price,
							Size:      size,
							Timestamp: eventTime,
						},
					}
					select {
					case eventCh <- trade:
						totalEvents++
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return eventCh, errCh
}

func parseTime(timeStr string) (time.Time, error) {
	// Handle the specific format found in the CSV data, which is not quite RFC3339
	// e.g., "2025-08-03 14:01:05.12764+00"
	// We need to replace the space with 'T' and ensure the timezone is compliant.
	correctedStr := strings.Replace(timeStr, " ", "T", 1)
	if strings.HasSuffix(correctedStr, "+00") {
		correctedStr = strings.TrimSuffix(correctedStr, "+00") + "Z"
	}

	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999Z07:00",
		"2006-01-02 15:04:05.999999-07",
		time.RFC3339,
	}

	for _, layout := range layouts {
		t, err := time.Parse(layout, correctedStr)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("could not parse time '%s' (corrected to '%s') with any known format", timeStr, correctedStr)
}

// LoadMarketEventsFromCSV reads an entire CSV file into memory and returns it as a slice of MarketEvent.
func LoadMarketEventsFromCSV(filePath string) ([]MarketEvent, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open csv file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	header, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return []MarketEvent{}, nil
		}
		return nil, fmt.Errorf("failed to read csv header: %w", err)
	}
	headerMap := make(map[string]int)
	for i, h := range header {
		headerMap[h] = i
	}

	var events []MarketEvent
	var currentBookUpdates []coincheck.OrderBookLevel
	var currentBookTime time.Time
	var currentBookPair string

	flushBookSnapshot := func() {
		if len(currentBookUpdates) == 0 {
			return
		}
		bids := make([][]string, 0)
		asks := make([][]string, 0)
		for _, u := range currentBookUpdates {
			level := []string{strconv.FormatFloat(u.Price, 'f', -1, 64), strconv.FormatFloat(u.Size, 'f', -1, 64)}
			if u.Side == "bid" {
				bids = append(bids, level)
			} else {
				asks = append(asks, level)
			}
		}
		events = append(events, OrderBookEvent{
			OrderBookData: coincheck.OrderBookData{
				PairStr: currentBookPair,
				Bids:    bids,
				Asks:    asks,
				Time:    currentBookTime,
			},
		})
		currentBookUpdates = nil
	}

	for {
		record, err := reader.Read()
		if err == io.EOF {
			flushBookSnapshot()
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read csv record: %w", err)
		}

		eventType := record[headerMap["event_type"]]
		eventTime, err := parseTime(record[headerMap["time"]])
		if err != nil {
			logger.Warnf("Skipping record due to time parse error: %v", err)
			continue
		}

		if eventType == "book" {
			if !currentBookTime.IsZero() && eventTime != currentBookTime {
				flushBookSnapshot()
			}
			currentBookTime = eventTime
			currentBookPair = record[headerMap["pair"]]
			side := record[headerMap["side"]]
			price, _ := strconv.ParseFloat(record[headerMap["price"]], 64)
			size, _ := strconv.ParseFloat(record[headerMap["size"]], 64)
			currentBookUpdates = append(currentBookUpdates, coincheck.OrderBookLevel{
				Side: side, Price: price, Size: size,
			})
		} else if eventType == "trade" {
			flushBookSnapshot()
			price, _ := strconv.ParseFloat(record[headerMap["price"]], 64)
			size, _ := strconv.ParseFloat(record[headerMap["size"]], 64)
			events = append(events, TradeEvent{
				Trade: cvd.Trade{
					ID:        record[headerMap["trade_id"]],
					Side:      record[headerMap["side"]],
					Price:     price,
					Size:      size,
					Timestamp: eventTime,
				},
			})
		}
	}
	return events, nil
}
