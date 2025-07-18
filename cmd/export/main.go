package main

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

func main() {
	// --- Argument Parsing ---
	hoursBefore := flag.Int("hours-before", 0, "Number of hours before the current time to start the export")
	startTimeStr := flag.String("start", "", "Start time for the export window (YYYY-MM-DD HH:MM:SS)")
	endTimeStr := flag.String("end", "", "End time for the export window (YYYY-MM-DD HH:MM:SS)")
	noZip := flag.Bool("no-zip", false, "Disable ZIP compression")
	flag.Parse()

	var startTime, endTime time.Time
	var err error

	// --- Time Parsing Logic ---
	if *hoursBefore > 0 {
		endTime = time.Now()
		startTime = endTime.Add(-time.Duration(*hoursBefore) * time.Hour)
	} else {
		if *startTimeStr == "" || *endTimeStr == "" {
			logger.Fatal("Both --start and --end flags are required when --hours-before is not used.")
		}
		startTime, err = time.Parse("2006-01-02 15:04:05", *startTimeStr)
		if err != nil {
			logger.Fatalf("Invalid start time format: %v", err)
		}
		endTime, err = time.Parse("2006-01-02 15:04:05", *endTimeStr)
		if err != nil {
			logger.Fatalf("Invalid end time format: %v", err)
		}
	}

	// --- Config and Logger Setup ---
	// We need dummy config paths to load the DB settings from .env
	cfg, err := config.LoadConfig("config/app_config.yaml", "config/trade_config.yaml")
	if err != nil {
		// If files don't exist, we can still proceed if env vars are set.
		// Create a dummy config to hold env vars.
		logger.Warnf("Could not load config files, relying on environment variables: %v", err)
		cfg = config.GetConfig()
		if cfg == nil {
			// If GetConfig is also nil, we must exit.
			logger.Fatal("Failed to initialize any configuration.")
		}
	}
	logger.SetGlobalLogLevel("info")

	// --- Database Connection ---
	ctx := context.Background()
	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.App.Database.User, cfg.App.Database.Password, cfg.App.Database.Host, cfg.App.Database.Port, cfg.App.Database.Name, cfg.App.Database.SSLMode)
	dbpool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		logger.Fatalf("Unable to connect to database: %v", err)
	}
	defer dbpool.Close()

	logger.Infof("Successfully connected to the database. Exporting data from %s to %s...", startTime.Format("2006-01-02 15:04:05"), endTime.Format("2006-01-02 15:04:05"))

	// --- File and Writer Setup ---
	timestamp := time.Now().Format("20060102-150405")
	outputDir := "simulation"
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		os.Mkdir(outputDir, 0755)
	}
	csvFileName := filepath.Join(outputDir, fmt.Sprintf("order_book_updates_%s.csv", timestamp))
	zipFileName := filepath.Join(outputDir, fmt.Sprintf("order_book_updates_%s.zip", timestamp))

	csvFile, err := os.Create(csvFileName)
	if err != nil {
		logger.Fatalf("Failed to create CSV file: %v", err)
	}
	defer csvFile.Close()

	writer := csv.NewWriter(csvFile)
	defer writer.Flush()

	// --- Write Data to CSV ---
	header := []string{"time", "pair", "side", "price", "size", "is_snapshot"}
	if err := writer.Write(header); err != nil {
		logger.Fatalf("Failed to write CSV header: %v", err)
	}

	query := `
        SELECT time, pair, side, price, size, is_snapshot
        FROM order_book_updates
        WHERE time >= $1 AND time < $2
        ORDER BY time ASC;
    `
	rows, err := dbpool.Query(ctx, query, startTime, endTime)
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
	writer.Flush()
	csvFile.Close()

	// --- ZIP Compression ---
	if !*noZip {
		if err := createZipFile(zipFileName, csvFileName); err != nil {
			logger.Fatalf("Failed to create ZIP file: %v", err)
		}
		if err := os.Remove(csvFileName); err != nil {
			logger.Warnf("Failed to remove temporary CSV file: %v", err)
		}
		logger.Infof("Successfully exported %d rows to %s", rowCount, zipFileName)
	} else {
		logger.Infof("Successfully exported %d rows to %s", rowCount, csvFileName)
	}
}

func createZipFile(zipFileName, csvFileName string) error {
	zipFile, err := os.Create(zipFileName)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	fileToZip, err := os.Open(csvFileName)
	if err != nil {
		return err
	}
	defer fileToZip.Close()

	info, err := fileToZip.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = filepath.Base(csvFileName)
	header.Method = zip.Deflate

	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(writer, fileToZip)
	return err
}
