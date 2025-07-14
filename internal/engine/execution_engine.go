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
	dbWriter       *dbwriter.Writer
}

// NewLiveExecutionEngine creates a new LiveExecutionEngine.
func NewLiveExecutionEngine(client *coincheck.Client, cfg *config.Config, dbWriter *dbwriter.Writer) *LiveExecutionEngine {
	return &LiveExecutionEngine{
		exchangeClient: client,
		cfg:            cfg,
		dbWriter:       dbWriter,
	}
}

// PlaceOrder places a new order on the exchange and monitors for execution.
func (e *LiveExecutionEngine) PlaceOrder(ctx context.Context, pair string, orderType string, rate float64, amount float64, postOnly bool) (*coincheck.OrderResponse, error) {
	if e.exchangeClient == nil {
		return nil, fmt.Errorf("LiveExecutionEngine: exchange client is not initialized")
	}

	// Balance check and amount adjustment logic
	balance, err := e.exchangeClient.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("failed to get balance for order placement: %w", err)
	}
	currentJpy, _ := strconv.ParseFloat(balance.Jpy, 64)
	currentBtc, _ := strconv.ParseFloat(balance.Btc, 64)

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

	roundedRate := math.Round(rate)
	adjustedAmount := amount

	var cappedAmount float64
	if orderType == "buy" {
		cappedAmount = (availableJpy * e.cfg.OrderRatio) / roundedRate
	} else { // sell
		cappedAmount = availableBtc * e.cfg.OrderRatio
	}
	cappedAmount = math.Floor(cappedAmount*1e6) / 1e6

	if amount > cappedAmount {
		adjustedAmount = cappedAmount
		logger.Warnf("[Live] Requested %s amount %.8f exceeds the allowable ratio. Adjusting to %.8f.", orderType, amount, adjustedAmount)
	}

	if adjustedAmount <= 0 {
		return nil, fmt.Errorf("adjusted order amount is zero or negative, skipping order placement")
	}

	// Place the order
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
	orderResp, err := e.exchangeClient.NewOrder(req)
	if err != nil {
		logger.Errorf("[Live] Error placing order: %v, Response: %+v", err, orderResp)
		return orderResp, err
	}
	if !orderResp.Success {
		return orderResp, fmt.Errorf("failed to place order: %s", orderResp.Error)
	}
	logger.Infof("[Live] Order placed successfully: ID=%d", orderResp.ID)

	// Monitor for execution
	pollInterval := time.Duration(e.cfg.Order.PollIntervalMs) * time.Millisecond
	timeout := time.Duration(e.cfg.Order.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			// Timeout reached, cancel the order
			logger.Warnf("[Live] Order ID %d did not fill within %v. Cancelling.", orderResp.ID, timeout)
			_, cancelErr := e.CancelOrder(context.Background(), orderResp.ID) // Use a new context for cancellation
			if cancelErr != nil {
				logger.Errorf("[Live] Failed to cancel order ID %d: %v", orderResp.ID, cancelErr)
				// Even if cancellation fails, we log the attempt as a cancelled trade
			}

			// Save the cancelled trade to the database
			if e.dbWriter != nil {
				trade := dbwriter.Trade{
					Time:          time.Now().UTC(),
					Pair:          pair,
					Side:          orderType,
					Price:         rate,
					Size:          adjustedAmount,
					TransactionID: orderResp.ID, // Use order ID as a reference
					IsCancelled:   true,
				}
				e.dbWriter.SaveTrade(trade)
				logger.Infof("[Live] Cancelled trade for Order ID %d saved to DB.", orderResp.ID)
			}

			if cancelErr != nil {
				return nil, fmt.Errorf("order timed out and cancellation failed: %w", cancelErr)
			}
			logger.Infof("[Live] Order ID %d cancelled successfully.", orderResp.ID)
			return nil, fmt.Errorf("order %d timed out and was cancelled", orderResp.ID)

		case <-time.After(pollInterval):
			transactions, err := e.exchangeClient.GetTransactions()
			if err != nil {
				logger.Errorf("[Live] Failed to get transactions to check order status: %v", err)
				continue // Retry on the next tick
			}

			for _, tx := range transactions.Transactions {
				if tx.OrderID == orderResp.ID {
					logger.Infof("[Live] Order ID %d confirmed as filled (Transaction ID: %d).", orderResp.ID, tx.ID)

					// Parse transaction details
					price, _ := strconv.ParseFloat(tx.Rate, 64)
					size, _ := strconv.ParseFloat(orderResp.Amount, 64)
					txID, _ := strconv.ParseInt(strconv.FormatInt(tx.ID, 10), 10, 64)

					// Save the confirmed trade to the database
					if e.dbWriter != nil {
						trade := dbwriter.Trade{
							Time:          time.Now().UTC(),
							Pair:          tx.Pair,
							Side:          tx.Side,
							Price:         price,
							Size:          size,
							TransactionID: txID,
							IsCancelled:   false,
						}
						e.dbWriter.SaveTrade(trade)
						logger.Infof("[Live] Confirmed trade for Order ID %d saved to DB.", orderResp.ID)
					}
					return orderResp, nil // Order filled
				}
			}
			logger.Infof("[Live] Order ID %d not yet filled. Retrying in %v...", orderResp.ID, pollInterval)
		}
	}
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
		Time:          time.Now().UTC(),
		Pair:          pair,
		Side:          orderType, // "buy" or "sell"
		Price:         rate,
		Size:          amount,
		TransactionID: int64(fakeTxID.ID()),
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
	unrealizedPnL := e.pnlCalculator.CalculateUnrealizedPnL(positionSize, avgEntryPrice, rate)
	totalRealizedPnL := e.pnlCalculator.GetRealizedPnL()
	totalPnL := totalRealizedPnL + unrealizedPnL

	// If dbWriter is available, save the trade and PnL summary.
	if e.dbWriter != nil {
		e.dbWriter.SaveTrade(trade)

		pnlSummary := dbwriter.PnLSummary{
			Time:          trade.Time,
			StrategyID:    "default",
			Pair:          pair,
			RealizedPnL:   realizedPnL,
			UnrealizedPnL: unrealizedPnL,
			TotalPnL:      totalPnL,
			PositionSize:  positionSize,
			AvgEntryPrice: avgEntryPrice,
		}
		if err := e.dbWriter.SavePnLSummary(ctx, pnlSummary); err != nil {
			logger.Errorf("[%s] Error saving PnL summary: %v", mode, err)
		}

		logger.Infof("[%s] Saved simulated trade and PnL summary to DB.", mode)
	} else {
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

// CancelOrder simulates cancelling an order.
func (e *ReplayExecutionEngine) CancelOrder(ctx context.Context, orderID int64) (*coincheck.CancelResponse, error) {
	logger.Infof("[Replay] Simulating cancellation of order ID: %d", orderID)
	return &coincheck.CancelResponse{
		Success: true,
		ID:      orderID,
	}, nil
}

// GetTotalRealizedPnL returns the total realized PnL from the internal calculator.
func (e *ReplayExecutionEngine) GetTotalRealizedPnL() float64 {
	return e.pnlCalculator.GetRealizedPnL()
}
