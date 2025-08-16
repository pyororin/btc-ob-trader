package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/datastore"
	"github.com/your-org/obi-scalp-bot/internal/report"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

func main() {
	// --- Load Configuration ---
	cfg, err := config.LoadConfig("config/app_config.yaml", "")
	if err != nil {
		// If config fails to load, we can't even start the logger properly.
		// Log to a temporary basic logger and exit.
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// --- Logger Setup ---
	l := logger.NewLogger(cfg.App.LogLevel)

	// --- Database Connection ---
	dbURL := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&timezone=Asia/Tokyo",
		cfg.App.Database.User, cfg.App.Database.Password, cfg.App.Database.Host, cfg.App.Database.Port, cfg.App.Database.Name, cfg.App.Database.SSLMode)

	dbpool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		l.Fatalf("Unable to connect to database: %v", err)
	}
	defer dbpool.Close()

	repo := datastore.NewTimescaleRepository(dbpool)
	reportService := report.NewService(dbpool)

	// --- Ticker for Periodic Report Generation ---
	intervalMinutes := cfg.App.ReportGenerator.IntervalMinutes
	if intervalMinutes <= 0 {
		l.Warnf("Invalid interval_minutes (%d) in config, defaulting to 60 minutes.", intervalMinutes)
		intervalMinutes = 60
	}
	interval := time.Duration(intervalMinutes) * time.Minute

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
func runReportGeneration(repo datastore.Repository, reportService *report.Service, l logger.Logger) {
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

	// 4. Final check before saving
	if analysisReport.TotalTrades == 0 {
		l.Info("No completed trades to generate a report.")
		return
	}

	// 5. Save the report
	if err := reportService.SavePnlReport(ctx, analysisReport); err != nil {
		l.Errorf("Failed to save PnL report: %v", err)
		return
	}

	l.Infof("Successfully generated and saved a new PnL report from %d trades.", len(trades))
}
