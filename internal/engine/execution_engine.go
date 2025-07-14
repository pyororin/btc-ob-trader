// Package engine contains the core trading logic components.
package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
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

	req := coincheck.OrderRequest{
		Pair:      pair,
		OrderType: orderType,
		Rate:      rate,
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
	dbWriter *dbwriter.Writer
	// TODO: Add position manager and PnL calculator
}

// NewReplayExecutionEngine creates a new ReplayExecutionEngine.
func NewReplayExecutionEngine(dbWriter *dbwriter.Writer) *ReplayExecutionEngine {
	return &ReplayExecutionEngine{
		dbWriter: dbWriter,
	}
}

// PlaceOrder simulates placing an order and records it to the database.
func (e *ReplayExecutionEngine) PlaceOrder(ctx context.Context, pair string, orderType string, rate float64, amount float64, postOnly bool) (*coincheck.OrderResponse, error) {
	if e.dbWriter == nil {
		return nil, fmt.Errorf("ReplayExecutionEngine: dbWriter is not initialized")
	}

	// Simulate immediate execution at the requested rate
	logger.Infof("[Replay] Simulating order placement: Pair=%s, Type=%s, Rate=%.2f, Amount=%.4f", pair, orderType, rate, amount)

	// Generate a fake transaction ID for the trade record.
	// In a real scenario, this might need to be more sophisticated.
	fakeTxID, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate fake transaction ID: %w", err)
	}

	// Create a trade record for the simulated execution.
	trade := dbwriter.Trade{
		Time:        time.Now().UTC(), // In replay, this should be the event time
		Pair:        pair,
		Side:        orderType, // "buy" or "sell"
		Price:       rate,
		Size:        amount,
		TransactionID: int64(fakeTxID.ID()), // This is not ideal, but works for now.
	}
	e.dbWriter.SaveTrade(trade)

	// TODO: Update position and PnL here.
	// For now, we just log it.
	logger.Infof("[Replay] Saved simulated trade to DB: %+v", trade)

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
