package engine

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/alert"
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

// TradeParams defines parameters for placing an order.
type TradeParams struct {
	Pair      string
	OrderType string
	Rate      float64
	Amount    float64
	PostOnly  bool
	// TP and SL are absolute price values. They are primarily for the replay engine.
	TP float64
	SL float64
}

// ExecutionEngine defines the interface for order execution.
type ExecutionEngine interface {
	PlaceOrder(ctx context.Context, params TradeParams) (*coincheck.OrderResponse, error)
	CancelOrder(ctx context.Context, orderID int64) (*coincheck.CancelResponse, error)
	GetBalance() (*coincheck.BalanceResponse, error)
}

// LiveExecutionEngine handles real order placement with the exchange.
type LiveExecutionEngine struct {
	exchangeClient *coincheck.Client
	dbWriter       dbwriter.DBWriter
	position       *position.Position
	pnlCalculator  *pnl.Calculator
	partialExitMutex   sync.Mutex
	isExitingPartially bool
	notifier           alert.Notifier
}

// NewLiveExecutionEngine creates a new LiveExecutionEngine.
func NewLiveExecutionEngine(client *coincheck.Client, dbWriter dbwriter.DBWriter, notifier alert.Notifier) *LiveExecutionEngine {
	var activeNotifier alert.Notifier
	if notifier == nil || (reflect.ValueOf(notifier).Kind() == reflect.Ptr && reflect.ValueOf(notifier).IsNil()) {
		activeNotifier = &alert.NoOpNotifier{}
	} else {
		activeNotifier = notifier
	}

	engine := &LiveExecutionEngine{
		exchangeClient: client,
		dbWriter:       dbWriter,
		position:       position.NewPosition(),
		pnlCalculator:  pnl.NewCalculator(),
		notifier:       activeNotifier,
	}
	return engine
}

// PlaceOrder places a new order on the exchange and monitors for execution.
func (e *LiveExecutionEngine) PlaceOrder(ctx context.Context, params TradeParams) (*coincheck.OrderResponse, error) {
	logger.Infof("PlaceOrder called with: pair=%s, orderType=%s, rate=%.2f, amount=%.8f, postOnly=%t", params.Pair, params.OrderType, params.Rate, params.Amount, params.PostOnly)
	cfg := config.GetConfig() // Get latest config on every order
	if !cfg.EnableTrade {
		logger.Errorf("[Live] Trading is disabled. Skipping order placement.")
		return nil, fmt.Errorf("trading is disabled via ENABLE_TRADE flag")
	}

	if e.exchangeClient == nil {
		logger.Errorf("[Live] Exchange client is not initialized.")
		return nil, fmt.Errorf("LiveExecutionEngine: exchange client is not initialized")
	}

	// --- Risk Management ---
	logger.Info("[Live] Performing risk management checks...")
	balance, err := e.GetBalance()
	if err != nil {
		logger.Errorf("[Live] Failed to get balance for risk check: %v", err)
		return nil, fmt.Errorf("failed to get balance for risk check: %w", err)
	}
	jpyBalance, err := strconv.ParseFloat(balance.Jpy, 64)
	if err != nil {
		logger.Errorf("[Live] Failed to parse JPY balance '%s': %v", balance.Jpy, err)
		return nil, fmt.Errorf("failed to parse JPY balance for risk check: %w", err)
	}
	logger.Infof("[RiskCheck] Current Balance: JPY=%.2f, BTC=%s", jpyBalance, balance.Btc)

	// Max drawdown check
	currentDrawdown := -e.pnlCalculator.GetRealizedPnL()
	maxDrawdownAllowed := jpyBalance * (cfg.Trade.Risk.MaxDrawdownPercent / 100.0)
	logger.Infof("[RiskCheck] Drawdown Check: Current Drawdown = %.2f JPY, Max Allowed = %.2f JPY (%.2f%% of %.2f JPY balance)",
		currentDrawdown, maxDrawdownAllowed, cfg.Trade.Risk.MaxDrawdownPercent, jpyBalance)
	if currentDrawdown > maxDrawdownAllowed {
		errMsg := fmt.Sprintf("risk check failed: current drawdown %.2f exceeds max allowed drawdown %.2f", currentDrawdown, maxDrawdownAllowed)
		logger.Warnf("[RiskCheck] ORDER REJECTED: %s", errMsg)
		logger.Infof("[RiskCheckResult] Drawdown check FAILED.")
		return nil, &RiskCheckError{Message: errMsg}
	}
	logger.Infof("[RiskCheckResult] Drawdown check PASSED.")

	// Max position size check
	btcBalance, err := strconv.ParseFloat(balance.Btc, 64)
	if err != nil {
		logger.Errorf("[Live] Failed to parse BTC balance '%s': %v", balance.Btc, err)
		return nil, fmt.Errorf("failed to parse BTC balance for risk check: %w", err)
	}

	if params.OrderType == "buy" {
		positionSize, avgEntryPrice := e.position.Get()
		currentPositionValue := math.Abs(positionSize * avgEntryPrice)
		orderValue := params.Amount * params.Rate
		prospectivePositionValue := currentPositionValue + orderValue
		maxPositionValueAllowed := jpyBalance * cfg.Trade.Risk.MaxPositionRatio
		logger.Infof("[RiskCheck] Position Size Check (Buy): Current Position Value=%.2f, Order Value=%.2f, Prospective Value = %.2f JPY, Max Allowed = %.2f JPY (%.2f%% of %.2f JPY balance)",
			currentPositionValue, orderValue, prospectivePositionValue, maxPositionValueAllowed, cfg.Trade.Risk.MaxPositionRatio*100, jpyBalance)
		if prospectivePositionValue > maxPositionValueAllowed {
			errMsg := fmt.Sprintf("risk check failed: prospective JPY position value %.2f exceeds max allowed JPY position value %.2f", prospectivePositionValue, maxPositionValueAllowed)
			logger.Warnf("[RiskCheck] ORDER REJECTED: %s", errMsg)
			logger.Infof("[RiskCheckResult] Position size check (Buy) FAILED.")
			return nil, &RiskCheckError{Message: errMsg}
		}
		logger.Infof("[RiskCheckResult] Position size check (Buy) PASSED.")
	} else if params.OrderType == "sell" {
		// For sell orders, the maximum amount that can be sold is the available BTC balance.
		logger.Infof("[RiskCheck] Position Size Check (Sell): Order Amount = %.8f BTC, Available Balance = %.8f BTC", params.Amount, btcBalance)
		if params.Amount > btcBalance {
			errMsg := fmt.Sprintf("risk check failed: sell order amount %.8f exceeds available BTC balance %.8f", params.Amount, btcBalance)
			logger.Warnf("[RiskCheck] ORDER REJECTED: %s", errMsg)
			logger.Infof("[RiskCheckResult] Position size check (Sell) FAILED.")
			return nil, &RiskCheckError{Message: errMsg}
		}
		logger.Infof("[RiskCheckResult] Position size check (Sell) PASSED.")
	}
	logger.Info("[Live] Risk management checks passed.")
	// --- End Risk Management ---

	// Place the order
	req := coincheck.OrderRequest{
		Pair:      params.Pair,
		OrderType: params.OrderType,
		Rate:      params.Rate,
		Amount:    params.Amount,
	}
	if params.PostOnly {
		req.TimeInForce = "post_only"
	}

	logger.Infof("[Live] Placing order: %+v", req)
	orderResp, _, err := e.exchangeClient.NewOrder(req)
	if err != nil {
		// This error is from the HTTP client or request creation, not an API error
		logger.Errorf("[Live] Critical error placing order: %v", err)
		return nil, err // Return nil for orderResp since the request failed
	}

	// It's crucial to log the raw response regardless of success
	logger.Infof("[Live] Raw order response: %+v", orderResp)

	if !orderResp.Success {
		errMsg := fmt.Sprintf("failed to place order: %s", orderResp.Error)
		logger.Errorf("[Live] %s", errMsg)
		e.sendAlert(fmt.Sprintf("Failed to place order: %s", orderResp.Error))
		return orderResp, fmt.Errorf(errMsg)
	}

	logger.Infof("[Live] Order placed successfully: ID=%d", orderResp.ID)
	e.sendAlert(fmt.Sprintf("Order placed successfully: ID=%d, Type=%s, Rate=%.2f, Amount=%.8f", orderResp.ID, req.OrderType, req.Rate, req.Amount))

	// Monitor for execution
	pollInterval := time.Duration(cfg.App.Order.PollIntervalMs) * time.Millisecond
	timeout := time.Duration(cfg.App.Order.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	logger.Infof("[Live] Starting to monitor order ID %d for execution...", orderResp.ID)
	for {
		select {
		case <-ctx.Done():
			// Timeout reached, cancel the order
			logger.Errorf("[Live] Order ID %d did not fill within %v. Cancelling.", orderResp.ID, timeout)
			e.sendAlert(fmt.Sprintf("Order Timeout: ID=%d did not fill within %v. Cancelling.", orderResp.ID, timeout))
			_, cancelErr := e.CancelOrder(context.Background(), orderResp.ID) // Use a new context for cancellation
			if cancelErr != nil {
				logger.Errorf("[Live] CRITICAL: Failed to cancel timed-out order ID %d: %v", orderResp.ID, cancelErr)
				e.sendAlert(fmt.Sprintf("CRITICAL: Failed to cancel order ID %d: %v", orderResp.ID, cancelErr))
				// Even if cancellation fails, we log the attempt as a cancelled trade
			}

			// Save the cancelled trade to the database
			if e.dbWriter != nil {
				trade := dbwriter.Trade{
					Time:          time.Now().UTC(),
					Pair:          params.Pair,
					Side:          params.OrderType,
					Price:         params.Rate,
					Size:          params.Amount,
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
			transactions, err := e.exchangeClient.GetTransactions(100) // Get latest 100 transactions
			if err != nil {
				logger.Warnf("[Live] Failed to get transactions to check order status: %v", err)
				continue // Retry on the next tick
			}

			for _, tx := range transactions.Transactions {
				if tx.OrderID == orderResp.ID {
					logger.Infof("[Live] Order ID %d confirmed as filled (Transaction ID: %d).", orderResp.ID, tx.ID)
					e.sendAlert(fmt.Sprintf("Order filled: ID=%d, TransactionID=%d, Type=%s, Rate=%s, Amount=%s", orderResp.ID, tx.ID, tx.Side, tx.Rate, orderResp.Amount))

					// Parse transaction details
					price, _ := strconv.ParseFloat(tx.Rate, 64)
					size, _ := strconv.ParseFloat(orderResp.Amount, 64)
					txID, _ := strconv.ParseInt(strconv.FormatInt(tx.ID, 10), 10, 64)

					// Save the confirmed trade to the database
					if e.dbWriter != nil {
						// 約定時刻をパース
						filledTime, err := time.Parse(time.RFC3339, tx.CreatedAt)
						if err != nil {
							logger.Warnf("[Live] Failed to parse transaction timestamp: %v. Using current time as fallback.", err)
							filledTime = time.Now().UTC()
						}


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
						}
						logger.Infof("[Live] Position updated: %s", e.position.String())

						positionSize, avgEntryPrice := e.position.Get()
						unrealizedPnL := e.pnlCalculator.CalculateUnrealizedPnL(positionSize, avgEntryPrice, price)
						totalRealizedPnL := e.pnlCalculator.GetRealizedPnL()
						totalPnL := totalRealizedPnL + unrealizedPnL

						pnlSummary := dbwriter.PnLSummary{
							Time:          trade.Time,
							StrategyID:    "default", // Or from config
							Pair:          params.Pair,
							RealizedPnL:   realizedPnL,
							UnrealizedPnL: unrealizedPnL,
							TotalPnL:      totalPnL,
							PositionSize:  positionSize,
							AvgEntryPrice: avgEntryPrice,
						}
						if err := e.dbWriter.SavePnLSummary(ctx, pnlSummary); err != nil {
							logger.Warnf("[Live] Error saving PnL summary: %v", err)
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
								logger.Warnf("[Live] Error saving trade PnL: %v", err)
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
		logger.Warnf("[Live] Error cancelling order: %v, Response: %+v", err, resp)
		return resp, err
	}
	logger.Infof("[Live] Order cancelled successfully: %+v", resp)
	return resp, nil
}

// sendAlert sends a message using the notifier if it's configured.
func (e *LiveExecutionEngine) sendAlert(message string) {
	if e.notifier != nil {
		if err := e.notifier.Send(message); err != nil {
			logger.Errorf("Failed to send alert: %v", err)
		}
	}
}

// GetPosition returns the current position size and average entry price.
func (e *LiveExecutionEngine) GetPosition() (size float64, avgEntryPrice float64) {
	return e.position.Get()
}

// GetBalance fetches the current balance from the exchange.
func (e *LiveExecutionEngine) GetBalance() (*coincheck.BalanceResponse, error) {
	return e.exchangeClient.GetBalance()
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

	if e.isExitingPartially || !bool(cfg.Trade.Twap.PartialExitEnabled) {
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

// ReplayExecutionEngine simulates order execution for backtesting.
type ReplayExecutionEngine struct {
	position         *position.Position
	pnlCalculator    *pnl.Calculator
	orderBook        pnl.OrderBookProvider
	mutex            sync.RWMutex
	lastPrice        float64
	// ExecutedTrades stores the history of simulated trades.
	ExecutedTrades   []dbwriter.Trade
	tradeCounter     int64 // Counter for generating deterministic trade IDs
	currentTP        float64
	currentSL        float64
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

// PlaceOrder simulates placing an order. It first checks for TP/SL on any existing position,
// then processes the new order signal if no TP/SL was hit.
func (e *ReplayExecutionEngine) PlaceOrder(ctx context.Context, params TradeParams) (*coincheck.OrderResponse, error) {
	mode := "Simulation"
	bestBid := e.orderBook.BestBid()
	bestAsk := e.orderBook.BestAsk()

	// --- 1. Check for TP/SL on existing position FIRST ---
	positionSize, _ := e.position.Get()
	if positionSize > 0 { // Long position is open
		if e.currentSL > 0 && bestBid <= e.currentSL {
			return e.executeClosingOrder(ctx, params.Pair, "sell", positionSize, e.currentSL, "SL")
		}
		if e.currentTP > 0 && bestBid >= e.currentTP {
			return e.executeClosingOrder(ctx, params.Pair, "sell", positionSize, e.currentTP, "TP")
		}
	} else if positionSize < 0 { // Short position is open
		if e.currentSL > 0 && bestAsk >= e.currentSL {
			return e.executeClosingOrder(ctx, params.Pair, "buy", -positionSize, e.currentSL, "SL")
		}
		if e.currentTP > 0 && bestAsk <= e.currentTP {
			return e.executeClosingOrder(ctx, params.Pair, "buy", -positionSize, e.currentTP, "TP")
		}
	}

	// If we are here, no TP/SL was hit. Now process the new signal.

	// Do not open a new position if one is already open
	if positionSize != 0 {
		// logger.Debugf("[%s] Signal for %s received, but position is already open. Ignoring signal.", mode, params.OrderType)
		return &coincheck.OrderResponse{Success: false, Error: "position already open"}, nil
	}

	// --- 2. Execute New Order based on signal ---
	if params.Amount < 0.001 {
		return nil, &RiskCheckError{Message: fmt.Sprintf("order amount %.8f is below the minimum required amount of 0.001 BTC", params.Amount)}
	}

	var executedPrice float64
	var executed bool

	if params.OrderType == "buy" {
		if bestAsk > 0 {
			executed = true
			executedPrice = bestAsk
			logger.Debugf("[%s] Buy order matched against BestAsk: %.2f. Executing at %.2f", mode, bestAsk, executedPrice)
		} else {
			logger.Debugf("[%s] Buy order NOT matched: No ask price available.", mode)
		}
	} else if params.OrderType == "sell" {
		if bestBid > 0 {
			executed = true
			executedPrice = bestBid
			logger.Debugf("[%s] Sell order matched against BestBid: %.2f. Executing at %.2f", mode, bestBid, executedPrice)
		} else {
			logger.Debugf("[%s] Sell order NOT matched: No bid price available.", mode)
		}
	}

	if !executed {
		return &coincheck.OrderResponse{
			Success: false,
			Error:   fmt.Sprintf("order not executed: no liquidity available. best_bid=%.2f, best_ask=%.2f", bestBid, bestAsk),
		}, nil
	}

	// --- 3. Record the new trade and update state ---
	e.tradeCounter++
	fakeTxID := e.tradeCounter

	trade := dbwriter.Trade{
		Time:          time.Now().UTC(),
		Pair:          params.Pair,
		Side:          params.OrderType,
		Price:         executedPrice,
		Size:          params.Amount,
		TransactionID: fakeTxID,
		EntryTime:     time.Now().UTC(), // Mark entry time for new trade
	}

	tradeAmount := trade.Size
	if trade.Side == "sell" {
		tradeAmount = -tradeAmount
	}

	// Since this is a new order, realized PnL should be 0.
	realizedPnL := e.position.Update(tradeAmount, trade.Price)
	if realizedPnL != 0 {
		logger.Warnf("[%s] Unexpected realized PnL of %.2f when opening a new position.", mode, realizedPnL)
	}

	e.currentTP = params.TP
	e.currentSL = params.SL
	e.ExecutedTrades = append(e.ExecutedTrades, trade)
	logger.Debugf("[%s] Position updated: %s. TP set to %.2f, SL set to %.2f", mode, e.position.String(), e.currentTP, e.currentSL)

	return &coincheck.OrderResponse{
		Success: true,
		ID:      fakeTxID,
		Rate:    fmt.Sprintf("%f", executedPrice),
		Amount:  fmt.Sprintf("%f", params.Amount),
		Pair:    params.Pair,
	}, nil
}

// CancelOrder simulates cancelling an order.
func (e *ReplayExecutionEngine) CancelOrder(ctx context.Context, orderID int64) (*coincheck.CancelResponse, error) {
	logger.Debugf("[Simulation] Simulating cancellation of order ID: %d", orderID)
	return &coincheck.CancelResponse{
		Success: true,
		ID:      orderID,
	}, nil
}

// GetTotalRealizedPnL returns the total realized PnL from the internal calculator.
func (e *ReplayExecutionEngine) GetTotalRealizedPnL() float64 {
	return e.pnlCalculator.GetRealizedPnL()
}

// GetBalance returns a mock balance for the replay engine.
func (e *ReplayExecutionEngine) GetBalance() (*coincheck.BalanceResponse, error) {
	// In replay mode, we don't have a real balance, so we return a large mock balance
	// to ensure that the order sizing logic doesn't fail.
	return &coincheck.BalanceResponse{
		Jpy: "100000000",
		Btc: "100",
	}, nil
}

// UpdateLastPrice updates the last known price for unrealized PnL calculations.
func (e *ReplayExecutionEngine) UpdateLastPrice(price float64) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.lastPrice = price
}

// GetLastPrice returns the last known price.
func (e *ReplayExecutionEngine) GetLastPrice() float64 {
	e.mutex.RLock()
	defer e.mutex.RUnlock()
	return e.lastPrice
}

// GetPosition returns the position object.
func (e *ReplayExecutionEngine) GetPosition() *position.Position {
	return e.position
}

// GetPnLCalculator returns the pnl calculator object.
func (e *ReplayExecutionEngine) GetPnLCalculator() *pnl.Calculator {
	return e.pnlCalculator
}

func (e *ReplayExecutionEngine) executeClosingOrder(ctx context.Context, pair string, orderType string, amount float64, price float64, reason string) (*coincheck.OrderResponse, error) {
	logger.Infof("[Simulation] Closing position due to %s: %s %.8f @ %.2f", reason, orderType, amount, price)

	e.tradeCounter++
	fakeTxID := e.tradeCounter

	trade := dbwriter.Trade{
		Time:          time.Now().UTC(),
		Pair:          pair,
		Side:          orderType,
		Price:         price,
		Size:          amount,
		TransactionID: fakeTxID,
	}

	tradeAmount := trade.Size
	if trade.Side == "sell" {
		tradeAmount = -tradeAmount
	}

	previousPositionSize, _ := e.position.Get()
	realizedPnL := e.position.Update(tradeAmount, trade.Price)
	newPositionSize, _ := e.position.Get()

	if math.Abs(previousPositionSize) > 1e-8 && math.Abs(newPositionSize) < 1e-8 {
		if previousPositionSize > 0 {
			trade.PositionSide = "long"
		} else {
			trade.PositionSide = "short"
		}
		for i := len(e.ExecutedTrades) - 1; i >= 0; i-- {
			if e.ExecutedTrades[i].ExitTime.IsZero() {
				if e.ExecutedTrades[i].PositionSide == "" {
					e.ExecutedTrades[i].PositionSide = trade.PositionSide
				}
				e.ExecutedTrades[i].ExitTime = trade.Time
			} else {
				break
			}
		}
	}
	trade.EntryTime = trade.Time

	if realizedPnL != 0 {
		e.pnlCalculator.UpdateRealizedPnL(realizedPnL)
	}
	trade.RealizedPnL = realizedPnL
	e.ExecutedTrades = append(e.ExecutedTrades, trade)
	logger.Debugf("[Simulation] Position updated after closing: %s", e.position.String())

	// Reset TP/SL after closing position
	e.currentTP = 0
	e.currentSL = 0

	return &coincheck.OrderResponse{
		Success: true,
		ID:      fakeTxID,
	}, nil
}
