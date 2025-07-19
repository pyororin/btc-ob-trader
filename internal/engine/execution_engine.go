package engine

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/pnl"
	"github.com/your-org/obi-scalp-bot/internal/position"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

// RiskCheckError is a custom error type for risk check failures.
type RiskCheckError struct {
	Message string
}

// Error implements the error interface for RiskCheckError.
func (e *RiskCheckError) Error() string {
	return e.Message
}

// ExecutionEngine defines the interface for order execution.
type ExecutionEngine interface {
	PlaceOrder(ctx context.Context, pair string, orderType string, rate float64, amount float64, postOnly bool) (*coincheck.OrderResponse, error)
	CancelOrder(ctx context.Context, orderID int64) (*coincheck.CancelResponse, error)
}

// LiveExecutionEngine handles real order placement with the exchange.
type LiveExecutionEngine struct {
	exchangeClient *coincheck.Client
	dbWriter       dbwriter.DBWriter
	position       *position.Position
	pnlCalculator  *pnl.Calculator
	recentPnLs     []float64
	currentRatios  struct {
		OrderRatio  float64
		LotMaxRatio float64
	}
	partialExitMutex   sync.Mutex
	isExitingPartially bool
}

// NewLiveExecutionEngine creates a new LiveExecutionEngine.
func NewLiveExecutionEngine(client *coincheck.Client, dbWriter dbwriter.DBWriter) *LiveExecutionEngine {
	cfg := config.GetConfig()
	engine := &LiveExecutionEngine{
		exchangeClient: client,
		dbWriter:       dbWriter,
		position:       position.NewPosition(),
		pnlCalculator:  pnl.NewCalculator(),
		recentPnLs:     make([]float64, 0),
	}
	// Initialize ratios from config
	engine.currentRatios.OrderRatio = cfg.Trade.OrderRatio
	engine.currentRatios.LotMaxRatio = cfg.Trade.LotMaxRatio
	return engine
}

// PlaceOrder places a new order on the exchange and monitors for execution.
func (e *LiveExecutionEngine) PlaceOrder(ctx context.Context, pair string, orderType string, rate float64, amount float64, postOnly bool) (*coincheck.OrderResponse, error) {
	if e.exchangeClient == nil {
		return nil, fmt.Errorf("LiveExecutionEngine: exchange client is not initialized")
	}

	cfg := config.GetConfig() // Get latest config on every order

	// Balance check and amount adjustment logic
	balance, err := e.exchangeClient.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("failed to get balance for risk check: %w", err)
	}
	currentJpy, _ := strconv.ParseFloat(balance.Jpy, 64)

	// --- Risk Management Check ---
	// 1. Max Position Check
	positionSize, avgEntryPrice := e.position.Get()
	orderSideMultiplier := 1.0
	if orderType == "sell" {
		orderSideMultiplier = -1.0
	}
	prospectivePositionSize := positionSize + (amount * orderSideMultiplier)
	prospectivePositionValueJPY := math.Abs(prospectivePositionSize) * rate

	// JPY残高に対するポジションサイズの比率をチェック
	if cfg.Trade.Risk.MaxPositionRatio > 0 {
		maxAllowedPositionValue := currentJpy * cfg.Trade.Risk.MaxPositionRatio
		if prospectivePositionValueJPY > maxAllowedPositionValue {
			msg := fmt.Sprintf("risk check failed: prospective position value %.2f JPY exceeds max_position_ratio (%.2f) of balance (%.2f JPY)", prospectivePositionValueJPY, cfg.Trade.Risk.MaxPositionRatio, currentJpy)
			return nil, &RiskCheckError{Message: msg}
		}
	}

	// 2. Max Drawdown Check
	unrealizedPnL := e.pnlCalculator.CalculateUnrealizedPnL(positionSize, avgEntryPrice, rate)
	totalRealizedPnL := e.pnlCalculator.GetRealizedPnL()
	totalPnL := unrealizedPnL + totalRealizedPnL

	if totalPnL < 0 {
		drawdownPercent := 0.0
		// Use current JPY balance as the capital base for drawdown percentage.
		// This is a simplification. A more robust implementation might track initial capital.
		if currentJpy > 0 {
			drawdownPercent = (-totalPnL / currentJpy) * 100
		}
		if cfg.Trade.Risk.MaxDrawdownPercent > 0 && drawdownPercent >= cfg.Trade.Risk.MaxDrawdownPercent {
			msg := fmt.Sprintf("risk check failed: current drawdown %.2f%% exceeds max_drawdown_percent %.2f%% (PnL: %.2f, Capital: %.2f)", drawdownPercent, cfg.Trade.Risk.MaxDrawdownPercent, totalPnL, currentJpy)
			return nil, &RiskCheckError{Message: msg}
		}
	}
	// --- End Risk Management Check ---

	// Adjust order ratios based on recent performance
	if cfg.Trade.AdaptivePositionSizing.Enabled {
		e.adjustRatios()
	}

	// Balance check and amount adjustment logic
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
		cappedAmount = (availableJpy * e.currentRatios.OrderRatio) / roundedRate
	} else { // sell
		cappedAmount = availableBtc * e.currentRatios.OrderRatio
	}
	cappedAmount = math.Floor(cappedAmount*1e6) / 1e6

	if amount > cappedAmount {
		adjustedAmount = cappedAmount
		logger.Warnf("[Live] Requested %s amount %.8f exceeds the allowable ratio. Adjusting to %.8f.", orderType, amount, adjustedAmount)
	}

	if adjustedAmount <= 0 {
		return nil, fmt.Errorf("adjusted order amount is zero or negative, skipping order placement")
	}

	// coincheckの最小注文単位（0.001BTC）を下回っていないか確認
	if adjustedAmount < 0.001 {
		msg := fmt.Sprintf("order amount %.8f is below the minimum required amount of 0.001 BTC", adjustedAmount)
		return nil, &RiskCheckError{Message: msg}
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
	orderResp, orderSentTime, err := e.exchangeClient.NewOrder(req)
	if err != nil {
		logger.Errorf("[Live] Error placing order: %v, Response: %+v", err, orderResp)
		return orderResp, err
	}
	if !orderResp.Success {
		return orderResp, fmt.Errorf("failed to place order: %s", orderResp.Error)
	}
	logger.Infof("[Live] Order placed successfully: ID=%d", orderResp.ID)

	// Monitor for execution
	pollInterval := time.Duration(cfg.App.Order.PollIntervalMs) * time.Millisecond
	timeout := time.Duration(cfg.App.Order.TimeoutSeconds) * time.Second
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
					IsMyTrade:     true,
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
						// 約定時刻をパース
						filledTime, err := time.Parse(time.RFC3339, tx.CreatedAt)
						if err != nil {
							logger.Errorf("[Live] Failed to parse transaction timestamp: %v. Using current time as fallback.", err)
							filledTime = time.Now().UTC()
						}

						// レイテンシを計算して保存
						latency := filledTime.Sub(orderSentTime)
						latencyMs := latency.Milliseconds()
						e.dbWriter.SaveLatency(dbwriter.Latency{
							Time:      filledTime,
							OrderID:   orderResp.ID,
							LatencyMs: latencyMs,
						})
						logger.Infof("[Live] Order ID %d latency: %d ms", orderResp.ID, latencyMs)

						trade := dbwriter.Trade{
							Time:          filledTime,
							Pair:          tx.Pair,
							Side:          tx.Side,
							Price:         price,
							Size:          size,
							TransactionID: txID,
							IsCancelled:   false,
							IsMyTrade:     true,
						}
						e.dbWriter.SaveTrade(trade)
						logger.Infof("[Live] Confirmed trade for Order ID %d saved to DB.", orderResp.ID)

						// PnL Calculation and Saving
						tradeAmount := trade.Size
						if trade.Side == "sell" {
							tradeAmount = -tradeAmount
						}
						realizedPnL := e.position.Update(tradeAmount, trade.Price)
						if realizedPnL != 0 {
							e.pnlCalculator.UpdateRealizedPnL(realizedPnL)
							// Update recent PnLs for adaptive sizing
							if cfg.Trade.AdaptivePositionSizing.Enabled {
								e.updateRecentPnLs(realizedPnL)
							}
						}
						logger.Infof("[Live] Position updated: %s", e.position.String())

						positionSize, avgEntryPrice := e.position.Get()
						unrealizedPnL := e.pnlCalculator.CalculateUnrealizedPnL(positionSize, avgEntryPrice, price)
						totalRealizedPnL := e.pnlCalculator.GetRealizedPnL()
						totalPnL := totalRealizedPnL + unrealizedPnL

						pnlSummary := dbwriter.PnLSummary{
							Time:          trade.Time,
							StrategyID:    "default", // Or from config
							Pair:          pair,
							RealizedPnL:   realizedPnL,
							UnrealizedPnL: unrealizedPnL,
							TotalPnL:      totalPnL,
							PositionSize:  positionSize,
							AvgEntryPrice: avgEntryPrice,
						}
						if err := e.dbWriter.SavePnLSummary(ctx, pnlSummary); err != nil {
							logger.Errorf("[Live] Error saving PnL summary: %v", err)
						} else {
							logger.Infof("[Live] Saved PnL summary to DB.")
						}
						// Save individual trade PnL
						if realizedPnL != 0 {
							tradePnl := dbwriter.TradePnL{
								TradeID:   txID,
								Pnl:       realizedPnL,
								CreatedAt: trade.Time,
							}
							if err := e.dbWriter.SaveTradePnL(ctx, tradePnl); err != nil {
								logger.Errorf("[Live] Error saving trade PnL: %v", err)
							} else {
								logger.Infof("[Live] Saved trade PnL to DB.")
							}
						}
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

// updateRecentPnLs adds a new PnL to the recent PnL list and keeps it at the configured size.
func (e *LiveExecutionEngine) updateRecentPnLs(pnl float64) {
	cfg := config.GetConfig()
	e.recentPnLs = append(e.recentPnLs, pnl)
	numTrades := cfg.Trade.AdaptivePositionSizing.NumTrades
	if len(e.recentPnLs) > numTrades {
		e.recentPnLs = e.recentPnLs[len(e.recentPnLs)-numTrades:]
	}
	logger.Infof("[Live] Updated recent PnLs: %v", e.recentPnLs)
}

// GetPosition returns the current position size and average entry price.
func (e *LiveExecutionEngine) GetPosition() (size float64, avgEntryPrice float64) {
	return e.position.Get()
}

// SetPartialExitStatus sets the status of the partial exit flag.
func (e *LiveExecutionEngine) SetPartialExitStatus(isExiting bool) {
	e.partialExitMutex.Lock()
	defer e.partialExitMutex.Unlock()
	e.isExitingPartially = isExiting
}

// IsExitingPartially returns true if a partial exit is currently in progress.
func (e *LiveExecutionEngine) IsExitingPartially() bool {
	e.partialExitMutex.Lock()
	defer e.partialExitMutex.Unlock()
	return e.isExitingPartially
}

// PartialExitOrder represents the details of a partial exit order to be executed.
type PartialExitOrder struct {
	OrderType string
	Size      float64
	Price     float64
}

// CheckAndTriggerPartialExit checks for partial profit taking conditions and returns an order if triggered.
func (e *LiveExecutionEngine) CheckAndTriggerPartialExit(currentMidPrice float64) *PartialExitOrder {
	e.partialExitMutex.Lock()
	defer e.partialExitMutex.Unlock()

	cfg := config.GetConfig()

	if e.isExitingPartially || !cfg.Trade.Twap.PartialExitEnabled {
		return nil
	}

	positionSize, avgEntryPrice := e.position.Get()
	if math.Abs(positionSize) < 1e-8 { // No position
		return nil
	}

	unrealizedPnL := (currentMidPrice - avgEntryPrice) * positionSize
	entryValue := avgEntryPrice * math.Abs(positionSize)
	if entryValue == 0 {
		return nil
	}
	profitRatio := unrealizedPnL / entryValue * 100 // In percent

	if profitRatio > cfg.Trade.Twap.ProfitThreshold {
		logger.Infof("Profit threshold of %.2f%% reached (current: %.2f%%). Initiating partial exit.",
			cfg.Trade.Twap.ProfitThreshold, profitRatio)

		e.isExitingPartially = true // Set flag inside the lock

		exitSize := positionSize * cfg.Trade.Twap.ExitRatio
		orderType := "sell"
		if positionSize < 0 { // Short position
			orderType = "buy"
		}

		return &PartialExitOrder{
			OrderType: orderType,
			Size:      math.Abs(exitSize),
			Price:     currentMidPrice,
		}
	}

	return nil
}

// adjustRatios dynamically adjusts the order and lot max ratios based on recent PnL.
func (e *LiveExecutionEngine) adjustRatios() {
	cfg := config.GetConfig()
	if len(e.recentPnLs) < cfg.Trade.AdaptivePositionSizing.NumTrades {
		// Not enough trade data yet, use default ratios
		e.currentRatios.OrderRatio = cfg.Trade.OrderRatio
		e.currentRatios.LotMaxRatio = cfg.Trade.LotMaxRatio
		return
	}

	pnlSum := 0.0
	for _, pnl := range e.recentPnLs {
		pnlSum += pnl
	}

	if pnlSum < 0 {
		// Reduce ratios
		e.currentRatios.OrderRatio *= cfg.Trade.AdaptivePositionSizing.ReductionStep
		e.currentRatios.LotMaxRatio *= cfg.Trade.AdaptivePositionSizing.ReductionStep

		// Enforce minimum ratios
		minOrderRatio := cfg.Trade.OrderRatio * cfg.Trade.AdaptivePositionSizing.MinRatio
		minLotMaxRatio := cfg.Trade.LotMaxRatio * cfg.Trade.AdaptivePositionSizing.MinRatio
		if e.currentRatios.OrderRatio < minOrderRatio {
			e.currentRatios.OrderRatio = minOrderRatio
		}
		if e.currentRatios.LotMaxRatio < minLotMaxRatio {
			e.currentRatios.LotMaxRatio = minLotMaxRatio
		}
		logger.Warnf("[Live] Negative PnL trend detected. Reducing ratios to OrderRatio: %.4f, LotMaxRatio: %.4f", e.currentRatios.OrderRatio, e.currentRatios.LotMaxRatio)
	} else {
		// Reset to default ratios
		if e.currentRatios.OrderRatio != cfg.Trade.OrderRatio || e.currentRatios.LotMaxRatio != cfg.Trade.LotMaxRatio {
			e.currentRatios.OrderRatio = cfg.Trade.OrderRatio
			e.currentRatios.LotMaxRatio = cfg.Trade.LotMaxRatio
			logger.Infof("[Live] Positive PnL trend. Ratios reset to default. OrderRatio: %.4f, LotMaxRatio: %.4f", e.currentRatios.OrderRatio, e.currentRatios.LotMaxRatio)
		}
	}
}

// ReplayExecutionEngine simulates order execution for backtesting.
type ReplayExecutionEngine struct {
	position      *position.Position
	pnlCalculator *pnl.Calculator
	orderBook     pnl.OrderBookProvider
	// ExecutedTrades stores the history of simulated trades.
	ExecutedTrades []dbwriter.Trade
	tradeCounter  int64 // Counter for generating deterministic trade IDs
}

// NewReplayExecutionEngine creates a new ReplayExecutionEngine.
func NewReplayExecutionEngine(orderBook pnl.OrderBookProvider) *ReplayExecutionEngine {
	return &ReplayExecutionEngine{
		position:       position.NewPosition(),
		pnlCalculator:  pnl.NewCalculator(),
		orderBook:      orderBook,
		ExecutedTrades: make([]dbwriter.Trade, 0),
		tradeCounter:   0, // Initialize counter
	}
}

// PlaceOrder simulates placing an order and records it based on the current order book state.
func (e *ReplayExecutionEngine) PlaceOrder(ctx context.Context, pair string, orderType string, rate float64, amount float64, postOnly bool) (*coincheck.OrderResponse, error) {
	mode := "Simulation"

	bestBid := e.orderBook.BestBid()
	bestAsk := e.orderBook.BestAsk()

	var executedPrice float64
	var executed bool

	if orderType == "buy" {
		if rate >= bestAsk && bestAsk > 0 {
			executed = true
			executedPrice = bestAsk
			logger.Infof("[%s] Buy order matched: Rate %.2f >= BestAsk %.2f. Executing at %.2f", mode, rate, bestAsk, executedPrice)
		} else {
			logger.Infof("[%s] Buy order NOT matched: Rate %.2f < BestAsk %.2f. Order would be on book.", mode, rate, bestAsk)
		}
	} else if orderType == "sell" {
		if rate <= bestBid && bestBid > 0 {
			executed = true
			executedPrice = bestBid
			logger.Infof("[%s] Sell order matched: Rate %.2f <= BestBid %.2f. Executing at %.2f", mode, rate, bestBid, executedPrice)
		} else {
			logger.Infof("[%s] Sell order NOT matched: Rate %.2f > BestBid %.2f. Order would be on book.", mode, rate, bestBid)
		}
	}

	if !executed {
		return &coincheck.OrderResponse{Success: false, Error: "order not executed"}, nil
	}

	// Generate a deterministic transaction ID using the counter.
	e.tradeCounter++
	fakeTxID := e.tradeCounter

	// Create a trade record for the simulated execution.
	trade := dbwriter.Trade{
		Time:          time.Now().UTC(),
		Pair:          pair,
		Side:          orderType,
		Price:         executedPrice,
		Size:          amount,
		TransactionID: fakeTxID,
	}

	// Update position and get realized PnL
	tradeAmount := trade.Size
	if trade.Side == "sell" {
		tradeAmount = -tradeAmount
	}

	previousPositionSize, _ := e.position.Get()
	realizedPnL := e.position.Update(tradeAmount, trade.Price)
	newPositionSize, _ := e.position.Get()

	// Set entry and exit times
	if math.Abs(previousPositionSize) > 1e-8 && math.Abs(newPositionSize) < 1e-8 { // Position closed
		// Find the entry trade and set the exit time for all trades in that position
		for i := len(e.ExecutedTrades) - 1; i >= 0; i-- {
			if e.ExecutedTrades[i].ExitTime.IsZero() {
				e.ExecutedTrades[i].ExitTime = trade.Time
			} else {
				break // Stop when we hit a trade that was already part of a closed position
			}
		}
	}
	trade.EntryTime = trade.Time

	if realizedPnL != 0 {
		e.pnlCalculator.UpdateRealizedPnL(realizedPnL)
	}
	trade.RealizedPnL = realizedPnL
	e.ExecutedTrades = append(e.ExecutedTrades, trade) // Append after PnL calculation
	logger.Infof("[%s] Position updated: %s", mode, e.position.String())

	// Calculate PnL
	positionSize, avgEntryPrice := e.position.Get()
	unrealizedPnL := e.pnlCalculator.CalculateUnrealizedPnL(positionSize, avgEntryPrice, executedPrice)
	totalRealizedPnL := e.pnlCalculator.GetRealizedPnL()
	totalPnL := totalRealizedPnL + unrealizedPnL

	logger.Infof("[%s] PnL Update: Realized=%.2f, Unrealized=%.2f, Total=%.2f", mode, realizedPnL, unrealizedPnL, totalPnL)

	return &coincheck.OrderResponse{
		Success: true,
		ID:      fakeTxID,
		Rate:    fmt.Sprintf("%f", executedPrice),
		Amount:  fmt.Sprintf("%f", amount),
		Pair:    pair,
	}, nil
}

// CancelOrder simulates cancelling an order.
func (e *ReplayExecutionEngine) CancelOrder(ctx context.Context, orderID int64) (*coincheck.CancelResponse, error) {
	logger.Infof("[Simulation] Simulating cancellation of order ID: %d", orderID)
	return &coincheck.CancelResponse{
		Success: true,
		ID:      orderID,
	}, nil
}

// GetTotalRealizedPnL returns the total realized PnL from the internal calculator.
func (e *ReplayExecutionEngine) GetTotalRealizedPnL() float64 {
	return e.pnlCalculator.GetRealizedPnL()
}
