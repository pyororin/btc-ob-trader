package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/your-org/obi-scalp-bot/internal/datastore"
	"github.com/your-org/obi-scalp-bot/internal/report"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

func main() {
	// --- Logger Setup ---
	l := logger.NewLogger("info") // Use "info" as a default log level

	// --- Database Connection ---
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		l.Fatal("DATABASE_URL environment variable is not set.")
	}

	dbpool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		l.Fatalf("Unable to connect to database: %v", err)
	}
	defer dbpool.Close()

	repo := datastore.NewRepository(dbpool)
	reportService := report.NewService(dbpool)

	// --- Ticker for Periodic Report Generation ---
	// Read interval from environment variable, default to 1 hour
	intervalStr := os.Getenv("REPORT_INTERVAL_MINUTES")
	interval, err := time.ParseDuration(intervalStr + "m")
	if err != nil || interval <= 0 {
		interval = 1 * time.Hour
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	l.Infof("Report generator started. Will run every %v.", interval)

	// --- Initial Run ---
	runReportGeneration(repo, reportService, l)

	// --- Main Loop ---
	for {
		select {
		case <-ticker.C:
			l.Info("--- Running Report Generation ---")
			runReportGeneration(repo, reportService, l)
		case <-context.Background().Done(): // Add a way to gracefully shutdown
			l.Info("Shutting down report generator.")
			return
		}
	}
}

// runReportGeneration fetches trades, analyzes them, and saves the report.
func runReportGeneration(repo *datastore.Repository, reportService *report.Service, l logger.Logger) {
	ctx := context.Background()

	// 1. Fetch the last processed trade ID
	lastTradeID, err := repo.FetchLatestPnlReportTradeID(ctx)
	if err != nil {
		// If no previous report exists, start from the beginning (trade_id = 0)
		l.Warnf("Could not fetch last trade ID, starting from beginning: %v", err)
		lastTradeID = 0
	}

	// 2. Fetch new trades since the last one
	trades, err := repo.FetchTradesForReportSince(ctx, lastTradeID)
	if err != nil {
		l.Errorf("Failed to fetch trades for report: %v", err)
		return
	}
	if len(trades) == 0 {
		l.Info("No new trades to generate a report.")
		return
	}

	// 3. Analyze trades
	reportTrades := make([]report.Trade, len(trades))
	for i, t := range trades {
		reportTrades[i] = report.Trade{
			Time:            t.Time,
			Pair:            t.Pair,
			Side:            t.Side,
			Price:           t.Price,
			Size:            t.Size,
			TransactionID:   t.TransactionID,
			IsCancelled:     t.IsCancelled,
			IsMyTrade:       t.IsMyTrade,
		}
	}

	analysisReport, err := reportService.AnalyzeTrades(reportTrades)
	if err != nil {
		if errors.Is(err, report.ErrNoExecutedTrades) {
			l.Warnf("Skipping report generation: %v", err)
		} else {
			l.Errorf("Failed to analyze trades: %v", err)
		}
		return
	}

	// 4. Save the report
	if err := reportService.SavePnlReport(ctx, analysisReport); err != nil {
		l.Errorf("Failed to save PnL report: %v", err)
		return
	}

	l.Infof("Successfully generated and saved a new PnL report from %d trades.", len(trades))
}
