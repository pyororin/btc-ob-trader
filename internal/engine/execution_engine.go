package engine

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/pnl"
	"github.com/your-org/obi-scalp-bot/internal/position"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
	"github.com/your-org/obi-scalp-bot/internal/config"
)

// ExecutionEngine defines the interface for order execution.
type ExecutionEngine interface {
	PlaceOrder(ctx context.Context, pair string, orderType string, rate float64, amount float64, postOnly bool) (*coincheck.OrderResponse, error)
	CancelOrder(ctx context.Context, orderID int64) (*coincheck.CancelResponse, error)
}

// LiveExecutionEngine handles real order placement with the exchange.
type LiveExecutionEngine struct {
	exchangeClient *coincheck.Client
	cfg            *config.Config
}

// NewLiveExecutionEngine creates a new LiveExecutionEngine.
func NewLiveExecutionEngine(client *coincheck.Client, cfg *config.Config) *LiveExecutionEngine {
	return &LiveExecutionEngine{
		exchangeClient: client,
		cfg:            cfg,
	}
}

// PlaceOrder places a new order on the exchange.
func (e *LiveExecutionEngine) PlaceOrder(ctx context.Context, pair string, orderType string, rate float64, amount float64, postOnly bool) (*coincheck.OrderResponse, error) {
	if e.exchangeClient == nil {
		return nil, fmt.Errorf("LiveExecutionEngine: exchange client is not initialized")
	}

	// Get current balance
	balance, err := e.exchangeClient.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("failed to get balance for order placement: %w", err)
	}
	currentJpy, _ := strconv.ParseFloat(balance.Jpy, 64)
	currentBtc, _ := strconv.ParseFloat(balance.Btc, 64)

	// Get open orders to calculate reserved funds
	openOrders, err := e.exchangeClient.GetOpenOrders()
	if err != nil {
		return nil, fmt.Errorf("failed to get open orders for balance adjustment: %w", err)
	}

	reservedJpy := 0.0
	reservedBtc := 0.0
	for _, order := range openOrders.Orders {
		if order.Pair != pair {
			continue
		}
		orderRate, _ := strconv.ParseFloat(order.Rate, 64)
		pendingAmount, _ := strconv.ParseFloat(order.PendingAmount, 64)
		if order.OrderType == "buy" {
			reservedJpy += orderRate * pendingAmount
		} else if order.OrderType == "sell" {
			reservedBtc += pendingAmount
		}
	}

	availableJpy := currentJpy - reservedJpy
	availableBtc := currentBtc - reservedBtc

	logger.Infof("[Live] Balance check: Current JPY=%.2f, Reserved JPY=%.2f, Available JPY=%.2f", currentJpy, reservedJpy, availableJpy)
	logger.Infof("[Live] Balance check: Current BTC=%.8f, Reserved BTC=%.8f, Available BTC=%.8f", currentBtc, reservedBtc, availableBtc)

	// Round rate to the nearest integer for JPY pairs, as required by Coincheck API.
	roundedRate := math.Round(rate)
	adjustedAmount := amount

	// Calculate the maximum amount based on the order ratio
	var cappedAmount float64
	if orderType == "buy" {
		cappedAmount = (availableJpy * e.cfg.OrderRatio) / roundedRate
	} else { // sell
		cappedAmount = availableBtc * e.cfg.OrderRatio
	}
	// Round down to 6 decimal places for safety
	cappedAmount = math.Floor(cappedAmount*1e6) / 1e6

	// Adjust order size only if the requested amount exceeds the capped amount
	if amount > cappedAmount {
		adjustedAmount = cappedAmount
		logger.Warnf("[Live] Requested %s amount %.8f exceeds the allowable ratio. Adjusting to %.8f.", orderType, amount, adjustedAmount)
	}

	if adjustedAmount <= 0 {
		return nil, fmt.Errorf("adjusted order amount is zero or negative, skipping order placement")
	}

	req := coincheck.OrderRequest{
		Pair:      pair,
		OrderType: orderType,
		Rate:      roundedRate,
		Amount:    adjustedAmount,
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
}

// NewReplayExecutionEngine creates a new ReplayExecutionEngine.
func NewReplayExecutionEngine(dbWriter *dbwriter.Writer) *ReplayExecutionEngine {
	return &ReplayExecutionEngine{
		dbWriter:      dbWriter,
		position:      position.NewPosition(),
		pnlCalculator: pnl.NewCalculator(),
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

	// Update position before calculating PnL
	e.position.Update(trade.Size, trade.Price)
	logger.Infof("[Replay] Position updated: %s", e.position.String())

	// Calculate PnL
	positionSize, avgEntryPrice := e.position.Get()
	unrealizedPnL := e.pnlCalculator.CalculateUnrealizedPnL(positionSize, avgEntryPrice, rate) // Use current rate for unrealized PnL
	realizedPnL := e.pnlCalculator.GetRealizedPnL()                                           // This needs more logic for realized PnL
	totalPnL := realizedPnL + unrealizedPnL

	// Save PnL summary
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
		logger.Errorf("[Replay] Error saving PnL summary: %v", err)
		// Decide if you should return the error or just log it
	}

	logger.Infof("[Replay] Saved simulated trade and PnL summary to DB.")

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
