package cvd

import (
	"encoding/csv"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	testTradesCSVPath = "test_trades.csv"
	floatTolerance    = 1e-9 // Tolerance for float comparisons
)

func loadTradesFromCSV(filePath string) ([]Trade, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	_, err = reader.Read() // Skip header row
	if err != nil {
		return nil, err
	}

	var trades []Trade
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Allow comments or empty lines
			if perr, ok := err.(*csv.ParseError); ok && (perr.Err == csv.ErrFieldCount || strings.HasPrefix(strings.TrimSpace(strings.Join(record, "")), "#")) {
				continue
			}
			return nil, err
		}

		// Skip lines that are comments (e.g. start with #) or obviously too short
		if len(record) < 4 || strings.HasPrefix(strings.TrimSpace(record[0]), "#") {
			continue
		}

		timestamp, err := time.Parse(time.RFC3339, record[0])
		if err != nil {
			// try to see if it's a comment after all
			if strings.HasPrefix(strings.TrimSpace(record[0]), "#") {
				continue
			}
			return nil, err
		}
		price, err := strconv.ParseFloat(record[1], 64)
		if err != nil {
			return nil, err
		}
		size, err := strconv.ParseFloat(record[2], 64)
		if err != nil {
			return nil, err
		}
		side := record[3]

		trades = append(trades, Trade{
			Timestamp: timestamp,
			Price:     price,
			Size:      size,
			Side:      side,
		})
	}
	return trades, nil
}

func TestCalculateCVD_FromCSV(t *testing.T) {
	trades, err := loadTradesFromCSV(testTradesCSVPath)
	if err != nil {
		t.Fatalf("Failed to load trades from CSV: %v", err)
	}

	// Expected CVD calculated manually from test_trades.csv
	// 0.1 (buy) - 0.05 (sell) + 0.2 (buy) - 0.15 (sell) + 0.02 (BUY) - 0.03 (SELL) + 0.07 (buy) = 0.16
	expectedCVD := 0.16
	actualCVD := CalculateCVD(trades)

	if absFloat(actualCVD-expectedCVD) > floatTolerance {
		t.Errorf("Expected CVD %f, got %f", expectedCVD, actualCVD)
	}
}

func TestCalculateCVD_EmptyTrades(t *testing.T) {
	trades := []Trade{}
	expectedCVD := 0.0
	actualCVD := CalculateCVD(trades)
	if actualCVD != expectedCVD {
		t.Errorf("Expected CVD %f for empty trades, got %f", expectedCVD, actualCVD)
	}
}

func TestCalculateCVD_OnlyBuys(t *testing.T) {
	trades := []Trade{
		{Timestamp: time.Now(), Price: 100, Size: 1.0, Side: "buy"},
		{Timestamp: time.Now(), Price: 101, Size: 0.5, Side: "BUY"},
	}
	expectedCVD := 1.5
	actualCVD := CalculateCVD(trades)
	if absFloat(actualCVD-expectedCVD) > floatTolerance {
		t.Errorf("Expected CVD %f, got %f", expectedCVD, actualCVD)
	}
}

func TestCalculateCVD_OnlySells(t *testing.T) {
	trades := []Trade{
		{Timestamp: time.Now(), Price: 100, Size: 1.0, Side: "sell"},
		{Timestamp: time.Now(), Price: 99, Size: 0.5, Side: "SELL"},
	}
	expectedCVD := -1.5
	actualCVD := CalculateCVD(trades)
	if absFloat(actualCVD-expectedCVD) > floatTolerance {
		t.Errorf("Expected CVD %f, got %f", expectedCVD, actualCVD)
	}
}

func TestCalculateCVD_MixedCaseAndInvalidSide(t *testing.T) {
	trades := []Trade{
		{Timestamp: time.Now(), Price: 100, Size: 1.0, Side: "Buy"},
		{Timestamp: time.Now(), Price: 101, Size: 0.5, Side: "sElL"},
		{Timestamp: time.Now(), Price: 102, Size: 0.2, Side: "unknown"}, // This should be ignored
		{Timestamp: time.Now(), Price: 103, Size: 0.3, Side: "BUY"},
	}
	// 1.0 - 0.5 + 0.3 = 0.8
	expectedCVD := 0.8
	actualCVD := CalculateCVD(trades)
	if absFloat(actualCVD-expectedCVD) > floatTolerance {
		t.Errorf("Expected CVD %f, got %f", expectedCVD, actualCVD)
	}
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
