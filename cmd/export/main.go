package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

func main() {
	// --- Argument Parsing ---
	startTimeStr := flag.String("start", "", "Start time for the export window (YYYY-MM-DD HH:MM:SS)")
	endTimeStr := flag.String("end", "", "End time for the export window (YYYY-MM-DD HH:MM:SS)")
	flag.Parse()

	if *startTimeStr == "" || *endTimeStr == "" {
		logger.Fatal("Both --start and --end flags are required.")
	}

	// --- Config and Logger Setup ---
	// We need a dummy config path to load the DB settings from .env
	cfg, err := config.LoadConfig("config/config.yaml")
	if err != nil {
		logger.Fatalf("Failed to load configuration to get DB settings: %v", err)
	}
	logger.SetGlobalLogLevel("info")

	// --- Database Connection ---
	ctx := context.Background()
	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.Database.User, cfg.Database.Password, cfg.Database.Host, cfg.Database.Port, cfg.Database.Name, cfg.Database.SSLMode)
	dbpool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		logger.Fatalf("Unable to connect to database: %v", err)
	}
	defer dbpool.Close()

	logger.Infof("Successfully connected to the database. Exporting data from %s to %s...", *startTimeStr, *endTimeStr)

	// --- CSV Writer Setup ---
	writer := csv.NewWriter(os.Stdout)
	defer writer.Flush()

	// Write header
	header := []string{"time", "pair", "side", "price", "size", "is_snapshot"}
	if err := writer.Write(header); err != nil {
		logger.Fatalf("Failed to write CSV header: %v", err)
	}

	// --- Query and Write Data ---
	query := `
        SELECT time, pair, side, price, size, is_snapshot
        FROM order_book_updates
        WHERE time >= $1 AND time < $2
        ORDER BY time ASC;
    `
	rows, err := dbpool.Query(ctx, query, *startTimeStr, *endTimeStr)
	if err != nil {
		logger.Fatalf("Failed to query order book updates: %v", err)
	}
	defer rows.Close()

	var rowCount int
	for rows.Next() {
		var t time.Time
		var pair, side string
		var price, size float64
		var isSnapshot bool

		if err := rows.Scan(&t, &pair, &side, &price, &size, &isSnapshot); err != nil {
			logger.Fatalf("Failed to scan row: %v", err)
		}

		record := []string{
			t.Format("2006-01-02 15:04:05.999999-07"),
			pair,
			side,
			fmt.Sprintf("%f", price),
			fmt.Sprintf("%f", size),
			fmt.Sprintf("%t", isSnapshot),
		}

		if err := writer.Write(record); err != nil {
			logger.Fatalf("Failed to write CSV record: %v", err)
		}
		rowCount++
	}

	if err := rows.Err(); err != nil {
		logger.Fatalf("Error iterating over rows: %v", err)
	}

	logger.Infof("Successfully exported %d rows.", rowCount)
}
