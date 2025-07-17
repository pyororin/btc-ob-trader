// Package engine tests the execution engine.
package engine

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/position"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCoincheckServer is a helper to create a mock HTTP server for Coincheck API.
func mockCoincheckServer(
	newOrderHandler http.HandlerFunc,
	cancelOrderHandler http.HandlerFunc,
	balanceHandler http.HandlerFunc,
	openOrdersHandler http.HandlerFunc,
	transactionsHandler http.HandlerFunc,
) *httptest.Server {
	mux := http.NewServeMux()

	if balanceHandler != nil {
		mux.HandleFunc("/api/accounts/balance", balanceHandler)
	}
	if openOrdersHandler != nil {
		mux.HandleFunc("/api/exchange/orders/opens", openOrdersHandler)
	}
	if transactionsHandler != nil {
		mux.HandleFunc("/api/exchange/orders/transactions", transactionsHandler)
	}

	mux.HandleFunc("/api/exchange/orders", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && newOrderHandler != nil {
			newOrderHandler(w, r)
		} else {
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/api/exchange/orders/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && cancelOrderHandler != nil {
			cancelOrderHandler(w, r)
		} else {
			http.NotFound(w, r)
		}
	})

	return httptest.NewServer(mux)
}

func TestExecutionEngine_PlaceOrder_Success(t *testing.T) {
	var requestCount int32
	var orderID int64 = 12345
	mockServer := mockCoincheckServer(
		func(w http.ResponseWriter, r *http.Request) { // NewOrder Handler
			atomic.AddInt32(&requestCount, 1)
			var reqBody coincheck.OrderRequest
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			resp := coincheck.OrderResponse{
				Success:     true,
				ID:          orderID,
				Rate:        "5000000.0",
				Amount:      "0.01",
				OrderType:   "buy",
				TimeInForce: reqBody.TimeInForce,
				Pair:        "btc_jpy",
				CreatedAt:   time.Now().Format(time.RFC3339),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		nil, // No cancel handler needed for success case
		func(w http.ResponseWriter, r *http.Request) { // Balance Handler
			resp := coincheck.BalanceResponse{Success: true, Jpy: "1000000", Btc: "1.0"}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // OpenOrders Handler
			resp := coincheck.OpenOrdersResponse{Success: true, Orders: []coincheck.OpenOrder{}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // Transactions Handler
			resp := coincheck.TransactionsResponse{
				Success: true,
				Transactions: []coincheck.Transaction{
					{ID: 98765, OrderID: orderID, Pair: "btc_jpy", Rate: "5000000.0", Side: "buy"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
	)
	defer mockServer.Close()

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	testCfg := &config.Config{
		OrderRatio: 0.5,
		Order: config.OrderConfig{
			PollIntervalMs: 10,
			TimeoutSeconds: 2,
		},
	}
	execEngine := NewLiveExecutionEngine(ccClient, testCfg, nil)

	// Test normal order
	resp, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 0.01, false)
	if err != nil {
		t.Fatalf("PlaceOrder returned an error: %v", err)
	}
	if !resp.Success {
		t.Errorf("PlaceOrder success was false. API Error: %s %s", resp.Error, resp.ErrorDescription)
	}
	if resp.ID != orderID {
		t.Errorf("Expected order ID %d, got %d", orderID, resp.ID)
	}

	// Test post_only order
	respPostOnly, errPostOnly := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 0.01, true)
	if errPostOnly != nil {
		t.Fatalf("PlaceOrder (postOnly) returned an error: %v", errPostOnly)
	}
	if !respPostOnly.Success {
		t.Errorf("PlaceOrder (postOnly) success was false. API Error: %s %s", respPostOnly.Error, respPostOnly.ErrorDescription)
	}
	if respPostOnly.TimeInForce != "post_only" {
		t.Errorf("Expected TimeInForce to be 'post_only', got '%s'", respPostOnly.TimeInForce)
	}
}

func TestExecutionEngine_PlaceOrder_AmountAdjustment(t *testing.T) {
	var adjustedAmount float64
	var orderID int64
	mockServer := mockCoincheckServer(
		func(w http.ResponseWriter, r *http.Request) { // NewOrder Handler
			var reqBody coincheck.OrderRequest
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			adjustedAmount = reqBody.Amount
			orderID = 123 // Set orderID for the transaction handler to use
			resp := coincheck.OrderResponse{Success: true, ID: orderID}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		nil, // No cancel handler
		func(w http.ResponseWriter, r *http.Request) { // Balance Handler
			resp := coincheck.BalanceResponse{Success: true, Jpy: "1000000", Btc: "1.0"}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // OpenOrders Handler
			resp := coincheck.OpenOrdersResponse{Success: true, Orders: []coincheck.OpenOrder{}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // Transactions Handler
			resp := coincheck.TransactionsResponse{
				Success: true,
				Transactions: []coincheck.Transaction{
					{ID: 98766, OrderID: orderID, Pair: "btc_jpy", Rate: "5000000.0", Side: "buy"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
	)
	defer mockServer.Close()

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	testCfg := &config.Config{
		OrderRatio: 0.5,
		Order: config.OrderConfig{
			PollIntervalMs: 10,
			TimeoutSeconds: 2,
		},
	}
	execEngine := NewLiveExecutionEngine(ccClient, testCfg, nil)

	_, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 0.2, false)
	if err != nil {
		t.Fatalf("PlaceOrder returned an unexpected error: %v", err)
	}

	expectedAmount := 0.1
	if adjustedAmount != expectedAmount {
		t.Errorf("Expected adjusted amount to be %.8f, got %.8f", expectedAmount, adjustedAmount)
	}
}

func TestExecutionEngine_CancelOrder_Success(t *testing.T) {
	var cancelRequestCount int32
	mockServer := mockCoincheckServer(
		nil,
		func(w http.ResponseWriter, r *http.Request) { // CancelOrder Handler
			atomic.AddInt32(&cancelRequestCount, 1)
			resp := coincheck.CancelResponse{
				Success: true,
				ID:      56789,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		nil,
		nil,
		nil,
	)
	defer mockServer.Close()

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	execEngine := NewLiveExecutionEngine(ccClient, &config.Config{}, nil)

	resp, err := execEngine.CancelOrder(context.Background(), 56789)
	if err != nil {
		t.Fatalf("CancelOrder returned an error: %v", err)
	}
	if !resp.Success {
		t.Errorf("CancelOrder success was false. API Error: %s", resp.Error)
	}
	if resp.ID != 56789 {
		t.Errorf("Expected cancelled order ID 56789, got %d", resp.ID)
	}
	if atomic.LoadInt32(&cancelRequestCount) != 1 {
		t.Errorf("Expected 1 request to cancel order endpoint, got %d", atomic.LoadInt32(&cancelRequestCount))
	}
}

func TestExecutionEngine_CancelOrder_Failure(t *testing.T) {
	mockServer := mockCoincheckServer(
		nil,
		func(w http.ResponseWriter, r *http.Request) { // CancelOrder Handler for failure
			resp := coincheck.CancelResponse{
				Success: false,
				ID:      11111,
				Error:   "Order not found or already processed",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		nil,
		nil,
		nil,
	)
	defer mockServer.Close()

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	execEngine := NewLiveExecutionEngine(ccClient, &config.Config{}, nil)

	resp, err := execEngine.CancelOrder(context.Background(), 11111)
	if err == nil {
		t.Fatal("CancelOrder was expected to return an error, but it didn't")
	}
	if resp == nil {
		t.Fatal("CancelOrder response should not be nil on API error")
	}
	if resp.Success {
		t.Error("CancelOrder success was true when expecting API error")
	}
	if resp.Error != "Order not found or already processed" {
		t.Errorf("Expected API error 'Order not found or already processed', got '%s'", resp.Error)
	}
	if !strings.Contains(err.Error(), "Order not found or already processed") {
		t.Errorf("Error message does not contain expected API error. Got: %s", err.Error())
	}
}

func TestExecutionEngine_PlaceOrder_Timeout(t *testing.T) {
	var orderID int64 = 67890
	mockServer := mockCoincheckServer(
		func(w http.ResponseWriter, r *http.Request) { // NewOrder Handler
			resp := coincheck.OrderResponse{Success: true, ID: orderID}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // CancelOrder Handler
			// Extract order ID from URL path, e.g., /api/exchange/orders/67890
			pathParts := strings.Split(r.URL.Path, "/")
			cancelledIDStr := pathParts[len(pathParts)-1]
			var cancelledID int64
			if id, err := Atoi64(cancelledIDStr); err == nil {
				cancelledID = id
			}

			resp := coincheck.CancelResponse{Success: true, ID: cancelledID}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // Balance Handler
			resp := coincheck.BalanceResponse{Success: true, Jpy: "1000000", Btc: "1.0"}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // OpenOrders Handler
			resp := coincheck.OpenOrdersResponse{Success: true, Orders: []coincheck.OpenOrder{}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // Transactions Handler (returns no matching transaction)
			resp := coincheck.TransactionsResponse{Success: true, Transactions: []coincheck.Transaction{}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
	)
	defer mockServer.Close()

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	testCfg := &config.Config{
		OrderRatio: 0.5,
		Order: config.OrderConfig{
			PollIntervalMs: 1, // Poll quickly
			TimeoutSeconds: 1, // Timeout quickly
		},
	}
	execEngine := NewLiveExecutionEngine(ccClient, testCfg, nil) // Assuming dbWriter is not essential for this test

	_, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 0.01, false)
	if err == nil {
		t.Fatal("PlaceOrder was expected to return a timeout error, but it didn't")
	}

	expectedErrorMsg := "timed out and was cancelled"
	if !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Errorf("Expected error message to contain '%s', got '%s'", expectedErrorMsg, err.Error())
	}
}

// Atoi64 is a helper to convert string to int64.
func Atoi64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func TestMain(m *testing.M) {
	m.Run()
}

func TestExecutionEngine_AdaptivePositionSizing(t *testing.T) {
	var requestCount int32
	var orderIDCounter int64 = 1000
	var lastRequestedAmount float64
	var mu sync.Mutex // To protect lastRequestedAmount

	// This handler will simulate successful order placement and capture the amount.
	newOrderHandler := func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		currentOrderID := atomic.AddInt64(&orderIDCounter, 1)

		var reqBody coincheck.OrderRequest
		_ = json.NewDecoder(r.Body).Decode(&reqBody)

		mu.Lock()
		lastRequestedAmount = reqBody.Amount
		mu.Unlock()

		resp := coincheck.OrderResponse{
			Success:   true,
			ID:        currentOrderID,
			Rate:      "5000000.0",
			Amount:    strconv.FormatFloat(reqBody.Amount, 'f', -1, 64),
			OrderType: "buy",
			Pair:      "btc_jpy",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}

	// This handler simulates the transaction appearing after the order is placed.
	transactionsHandler := func(w http.ResponseWriter, r *http.Request) {
		currentOrderID := atomic.LoadInt64(&orderIDCounter)
		mu.Lock()
		mu.Unlock()

		resp := coincheck.TransactionsResponse{
			Success: true,
			Transactions: []coincheck.Transaction{
				{
					ID:      currentOrderID + 5000, // Just a unique tx id
					OrderID: currentOrderID,
					Pair:    "btc_jpy",
					Rate:    "5000000.0", // Assume execution at this price
					Side:    "buy",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}

	mockServer := mockCoincheckServer(
		newOrderHandler,
		nil, // No cancel handler
		func(w http.ResponseWriter, r *http.Request) { // Balance Handler
			resp := coincheck.BalanceResponse{Success: true, Jpy: "100000000", Btc: "10.0"} // Large balance
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // OpenOrders Handler
			resp := coincheck.OpenOrdersResponse{Success: true, Orders: []coincheck.OpenOrder{}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		},
		transactionsHandler,
	)
	defer mockServer.Close()

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	testCfg := &config.Config{
		OrderRatio:  0.2,
		LotMaxRatio: 0.2, // For consistency
		Order: config.OrderConfig{
			PollIntervalMs: 1, // Poll fast
			TimeoutSeconds: 1,
		},
		AdaptivePositionSizing: config.AdaptiveSizingConfig{
			Enabled:       true,
			NumTrades:     5,
			ReductionStep: 0.8,
			MinRatio:      0.5,
		},
	}
	execEngine := NewLiveExecutionEngine(ccClient, testCfg, nil)

	// --- Scenario 1: Losing trades lead to reduced size ---
	t.Run("size reduction after losses", func(t *testing.T) {
		// Simulate 5 losing trades to trigger the reduction logic.
		// We can do this by calling PlaceOrder and manually manipulating the PnL tracker inside the engine.
		// However, the engine's PnL tracking is internal. A better approach is to simulate the full loop.
		// For this test, we'll assume every trade results in a fixed loss.
		// The engine calculates PnL based on position changes.
		// To simulate a loss, we need a buy and a sell.
		// Let's simplify: we will manually update the internal PnL list for the test.
		// This is a white-box test.
		execEngine.UpdateRecentPnLsForTest(t, []float64{-100, -100, -100, -100, -100})

		// Now, place an order. The size should be reduced.
		_, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 10.0, false) // Large amount to trigger adjustment
		if err != nil {
			t.Fatalf("PlaceOrder returned an unexpected error: %v", err)
		}

		mu.Lock()
		finalAmount := lastRequestedAmount
		mu.Unlock()

		expectedReducedAmount := (100000000 * (testCfg.OrderRatio * testCfg.AdaptivePositionSizing.ReductionStep)) / 5000000
		const epsilon = 1e-9
		if math.Abs(finalAmount-expectedReducedAmount) > epsilon {
			t.Errorf("Expected amount to be reduced to %.8f, but got %.8f", expectedReducedAmount, finalAmount)
		}
	})

	// --- Scenario 2: Winning trades reset the size ---
	t.Run("size reset after profits", func(t *testing.T) {
		// First, ensure the size is reduced
		execEngine.UpdateRecentPnLsForTest(t, []float64{-100, -100, -100, -100, -100})
		_, _ = execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 10.0, false)

		// Now, simulate a winning streak that turns the PnL positive
		execEngine.UpdateRecentPnLsForTest(t, []float64{200, 200, 200, 200, 200})

		// Place another order. The size should be reset to the original ratio.
		_, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 10.0, false) // Large amount
		if err != nil {
			t.Fatalf("PlaceOrder returned an unexpected error: %v", err)
		}

		mu.Lock()
		finalAmount := lastRequestedAmount
		mu.Unlock()

		originalAmount := (100000000 * testCfg.OrderRatio) / 5000000
		const epsilon = 1e-9
		if math.Abs(finalAmount-originalAmount) > epsilon {
			t.Errorf("Expected amount to be reset to %.8f, but got %.8f", originalAmount, finalAmount)
		}
	})
}

// UpdateRecentPnLsForTest is a test helper to inject PnL data into the engine.
func (e *LiveExecutionEngine) UpdateRecentPnLsForTest(t *testing.T, pnls []float64) {
	t.Helper()
	e.recentPnLs = make([]float64, len(pnls))
	copy(e.recentPnLs, pnls)
}

func TestLiveExecutionEngine_CheckAndTriggerPartialExit(t *testing.T) {
	baseCfg := &config.Config{
		Twap: config.TwapConfig{
			PartialExitEnabled: true,
			ProfitThreshold:    1.0, // 1%
			ExitRatio:          0.5, // 50%
		},
	}

	t.Run("Long position with profit above threshold", func(t *testing.T) {
		engine := NewLiveExecutionEngine(nil, baseCfg, nil)
		engine.position = position.NewPosition()
		engine.position.Update(1.0, 100000) // Buy 1 BTC @ 100,000

		marketPrice := 102000.0 // 2% profit
		exitOrder := engine.CheckAndTriggerPartialExit(marketPrice)

		require.NotNil(t, exitOrder)
		assert.Equal(t, "sell", exitOrder.OrderType)
		assert.InDelta(t, 0.5, exitOrder.Size, 1e-9) // 50% of 1.0 BTC
		assert.True(t, engine.IsExitingPartially(), "Flag should be set after triggering")
	})

	t.Run("Long position with profit below threshold", func(t *testing.T) {
		engine := NewLiveExecutionEngine(nil, baseCfg, nil)
		engine.position = position.NewPosition()
		engine.position.Update(1.0, 100000)

		marketPrice := 100500.0 // 0.5% profit
		exitOrder := engine.CheckAndTriggerPartialExit(marketPrice)

		assert.Nil(t, exitOrder)
		assert.False(t, engine.IsExitingPartially())
	})

	t.Run("Short position with profit above threshold", func(t *testing.T) {
		engine := NewLiveExecutionEngine(nil, baseCfg, nil)
		engine.position = position.NewPosition()
		engine.position.Update(-1.0, 100000) // Sell 1 BTC @ 100,000

		marketPrice := 98000.0 // 2% profit
		exitOrder := engine.CheckAndTriggerPartialExit(marketPrice)

		require.NotNil(t, exitOrder)
		assert.Equal(t, "buy", exitOrder.OrderType)
		assert.InDelta(t, 0.5, exitOrder.Size, 1e-9)
		assert.True(t, engine.IsExitingPartially())
	})

	t.Run("No position", func(t *testing.T) {
		engine := NewLiveExecutionEngine(nil, baseCfg, nil)
		engine.position = position.NewPosition()

		marketPrice := 102000.0
		exitOrder := engine.CheckAndTriggerPartialExit(marketPrice)

		assert.Nil(t, exitOrder)
	})

	t.Run("Disabled in config", func(t *testing.T) {
		cfg := *baseCfg
		cfg.Twap.PartialExitEnabled = false
		engine := NewLiveExecutionEngine(nil, &cfg, nil)
		engine.position = position.NewPosition()
		engine.position.Update(1.0, 100000)

		marketPrice := 102000.0
		exitOrder := engine.CheckAndTriggerPartialExit(marketPrice)

		assert.Nil(t, exitOrder)
	})

	t.Run("Already exiting", func(t *testing.T) {
		engine := NewLiveExecutionEngine(nil, baseCfg, nil)
		engine.position = position.NewPosition()
		engine.position.Update(1.0, 100000)
		engine.SetPartialExitStatus(true) // Manually set flag

		marketPrice := 102000.0
		exitOrder := engine.CheckAndTriggerPartialExit(marketPrice)

		assert.Nil(t, exitOrder, "Should not trigger another exit if one is in progress")
	})
}
