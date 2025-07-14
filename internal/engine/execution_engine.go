// Package engine contains the core trading logic components.
package engine

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/pnl"
	"github.com/your-org/obi-scalp-bot/internal/position"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

// ExecutionEngine defines the interface for order execution.
type ExecutionEngine interface {
	PlaceOrder(ctx context.Context, pair string, orderType string, rate float64, amount float64, postOnly bool) (*coincheck.OrderResponse, error)
	CancelOrder(ctx context.Context, orderID int64) (*coincheck.CancelResponse, error)
}

// LiveExecutionEngine handles real order placement with the exchange.
type LiveExecutionEngine struct {
	exchangeClient *coincheck.Client
}

// NewLiveExecutionEngine creates a new LiveExecutionEngine.
func NewLiveExecutionEngine(client *coincheck.Client) *LiveExecutionEngine {
	return &LiveExecutionEngine{
		exchangeClient: client,
	}
}

// PlaceOrder places a new order on the exchange.
func (e *LiveExecutionEngine) PlaceOrder(ctx context.Context, pair string, orderType string, rate float64, amount float64, postOnly bool) (*coincheck.OrderResponse, error) {
	if e.exchangeClient == nil {
		return nil, fmt.Errorf("LiveExecutionEngine: exchange client is not initialized")
	}

	// Round rate to the nearest integer for JPY pairs, as required by Coincheck API.
	roundedRate := math.Round(rate)

	req := coincheck.OrderRequest{
		Pair:      pair,
		OrderType: orderType,
		Rate:      roundedRate,
		Amount:    amount,
	}
	if postOnly {
		req.TimeInForce = "post_only"
	}

	logger.Infof("[Live] Placing order: %+v", req)
	resp, err := e.exchangeClient.NewOrder(req)
	if err != nil {
		logger.Errorf("[Live] Error placing order: %v, Response: %+v", err, resp)
		return resp, err
	}
	logger.Infof("[Live] Order placed successfully: %+v", resp)
	return resp, nil
}

// CancelOrder cancels an existing order on the exchange.
func (e *LiveExecutionEngine) CancelOrder(ctx context.Context, orderID int64) (*coincheck.CancelResponse, error) {
	if e.exchangeClient == nil {
		return nil, fmt.Errorf("LiveExecutionEngine: exchange client is not initialized")
	}

	logger.Infof("[Live] Cancelling order ID: %d", orderID)
	resp, err := e.exchangeClient.CancelOrder(orderID)
	if err != nil {
		logger.Errorf("[Live] Error cancelling order: %v, Response: %+v", err, resp)
		return resp, err
	}
	logger.Infof("[Live] Order cancelled successfully: %+v", resp)
	return resp, nil
}

// ReplayExecutionEngine simulates order execution for backtesting.
type ReplayExecutionEngine struct {
	dbWriter      *dbwriter.Writer
	position      *position.Position
	pnlCalculator *pnl.Calculator
	// ExecutedTrades stores the history of simulated trades.
	ExecutedTrades []dbwriter.Trade
}

// NewReplayExecutionEngine creates a new ReplayExecutionEngine.
func NewReplayExecutionEngine(dbWriter *dbwriter.Writer) *ReplayExecutionEngine {
	return &ReplayExecutionEngine{
		dbWriter:       dbWriter,
		position:       position.NewPosition(),
		pnlCalculator:  pnl.NewCalculator(),
		ExecutedTrades: make([]dbwriter.Trade, 0),
	}
}

// PlaceOrder simulates placing an order and records it.
// If dbWriter is configured, it also saves the trade to the database.
func (e *ReplayExecutionEngine) PlaceOrder(ctx context.Context, pair string, orderType string, rate float64, amount float64, postOnly bool) (*coincheck.OrderResponse, error) {
	mode := "Replay"
	if e.dbWriter == nil {
		mode = "Simulation"
	}
	logger.Infof("[%s] Simulating order placement: Pair=%s, Type=%s, Rate=%.2f, Amount=%.4f", mode, pair, orderType, rate, amount)

	// Generate a fake transaction ID for the trade record.
	fakeTxID, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate fake transaction ID: %w", err)
	}

	// Create a trade record for the simulated execution.
	trade := dbwriter.Trade{
		// TODO: This should ideally use the event time from the simulation, not time.Now().
		// This requires passing the event time through the execution flow.
		Time:          time.Now().UTC(),
		Pair:          pair,
		Side:          orderType, // "buy" or "sell"
		Price:         rate,
		Size:          amount,
		TransactionID: int64(fakeTxID.ID()), // This is not ideal, but works for now.
	}

	// Store the executed trade in memory for later analysis.
	e.ExecutedTrades = append(e.ExecutedTrades, trade)

	// Update position and get realized PnL
	tradeAmount := trade.Size
	if trade.Side == "sell" {
		tradeAmount = -tradeAmount
	}
	realizedPnL := e.position.Update(tradeAmount, trade.Price)
	if realizedPnL != 0 {
		e.pnlCalculator.UpdateRealizedPnL(realizedPnL)
	}
	trade.RealizedPnL = realizedPnL
	logger.Infof("[%s] Position updated: %s", mode, e.position.String())

	// Calculate PnL
	positionSize, avgEntryPrice := e.position.Get()
	unrealizedPnL := e.pnlCalculator.CalculateUnrealizedPnL(positionSize, avgEntryPrice, rate) // Use current rate for unrealized PnL
	totalRealizedPnL := e.pnlCalculator.GetRealizedPnL()
	totalPnL := totalRealizedPnL + unrealizedPnL

	// If dbWriter is available, save the trade and PnL summary.
	if e.dbWriter != nil {
		e.dbWriter.SaveTrade(trade)

		pnlSummary := dbwriter.PnLSummary{
			Time:          trade.Time,
			StrategyID:    "default", // Or get from context/config
			Pair:          pair,
			RealizedPnL:   realizedPnL,
			UnrealizedPnL: unrealizedPnL,
			TotalPnL:      totalPnL,
			PositionSize:  positionSize,
			AvgEntryPrice: avgEntryPrice,
		}
		if err := e.dbWriter.SavePnLSummary(ctx, pnlSummary); err != nil {
			logger.Errorf("[%s] Error saving PnL summary: %v", mode, err)
			// Decide if you should return the error or just log it
		}

		logger.Infof("[%s] Saved simulated trade and PnL summary to DB.", mode)
	} else {
		// Log PnL info for simulation mode
		logger.Infof("[%s] PnL Update: Realized=%.2f, Unrealized=%.2f, Total=%.2f", mode, realizedPnL, unrealizedPnL, totalPnL)
	}

	// Return a mock OrderResponse
	return &coincheck.OrderResponse{
		Success: true,
		ID:      int64(fakeTxID.ID()),
		Rate:    fmt.Sprintf("%f", rate),
		Amount:  fmt.Sprintf("%f", amount),
		Pair:    pair,
	}, nil
}

// CancelOrder simulates cancelling an order. In this simple simulation, we assume it's always successful.
func (e *ReplayExecutionEngine) CancelOrder(ctx context.Context, orderID int64) (*coincheck.CancelResponse, error) {
	logger.Infof("[Replay] Simulating cancellation of order ID: %d", orderID)
	// In a more complex simulation, we might check if the order exists in a local state.
	return &coincheck.CancelResponse{
		Success: true,
		ID:      orderID,
	}, nil
}

// GetTotalRealizedPnL returns the total realized PnL from the internal calculator.
func (e *ReplayExecutionEngine) GetTotalRealizedPnL() float64 {
	return e.pnlCalculator.GetRealizedPnL()
}
