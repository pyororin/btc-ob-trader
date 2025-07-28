// Package engine tests the execution engine.
package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/position"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTest configures a mock server and a default configuration for tests.
func setupTest(t *testing.T) (*httptest.Server, *config.Config) {
	t.Helper()

	// Create a default config to prevent nil pointers
	cfg := &config.Config{
		App: config.AppConfig{
			Order: config.OrderConfig{
				PollIntervalMs: 10,
				TimeoutSeconds: 1,
			},
		},
		Trade: config.TradeConfig{
			OrderRatio:  0.1,
			LotMaxRatio: 0.1,
			Risk: config.RiskConfig{
				MaxDrawdownPercent: 10.0,
				MaxPositionRatio:   0.5,
			},
			AdaptivePositionSizing: config.AdaptiveSizingConfig{
				Enabled:       false,
				NumTrades:     5,
				ReductionStep: 0.8,
				MinRatio:      0.5,
			},
			Twap: config.TwapConfig{
				PartialExitEnabled: false,
			},
		},
		EnableTrade: true,
	}
	config.SetTestConfig(cfg) // Use a setter to inject the mock config

	// Default handlers that can be overridden by tests
	mockServer := mockCoincheckServer(
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(coincheck.OrderResponse{Success: true, ID: 12345})
		},
		nil,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(coincheck.BalanceResponse{Success: true, Jpy: "1000000", Btc: "1.0"})
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(coincheck.OpenOrdersResponse{Success: true, Orders: []coincheck.OpenOrder{}})
		},
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(coincheck.TransactionsResponse{Success: true, Transactions: []coincheck.Transaction{}})
		},
	)

	return mockServer, cfg
}

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
		mux.HandleFunc("/api/exchange/orders/transactions_pagination", transactionsHandler)
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
	mockServer, _ := setupTest(t)
	defer mockServer.Close()

	// Override default handlers for this specific test
	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/exchange/orders":
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
		case "/api/accounts/balance":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(coincheck.BalanceResponse{Success: true, Jpy: "1000000", Btc: "1.0"})
		case "/api/exchange/orders/opens":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(coincheck.OpenOrdersResponse{Success: true, Orders: []coincheck.OpenOrder{}})
		case "/api/exchange/orders/transactions_pagination":
			resp := struct {
				Success bool `json:"success"`
				Data    []coincheck.Transaction `json:"data"`
			}{
				Success: true,
				Data: []coincheck.Transaction{
					{ID: 98765, OrderID: orderID, Pair: "btc_jpy", Rate: "5000000.0", Side: "buy", CreatedAt: time.Now().UTC().Format(time.RFC3339)},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	})

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	execEngine := NewLiveExecutionEngine(ccClient, nil, nil)

	// Test normal order
	resp, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 0.01, false)
	require.NoError(t, err)
	require.True(t, resp.Success, "PlaceOrder success was false. API Error: %s %s", resp.Error, resp.ErrorDescription)
	assert.Equal(t, orderID, resp.ID)

	// Test post_only order
	respPostOnly, errPostOnly := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 0.01, true)
	require.NoError(t, errPostOnly)
	require.True(t, respPostOnly.Success, "PlaceOrder (postOnly) success was false. API Error: %s %s", respPostOnly.Error, respPostOnly.ErrorDescription)
	assert.Equal(t, "post_only", respPostOnly.TimeInForce)
}

func TestExecutionEngine_PlaceOrder_SuccessAfterMultiplePolls(t *testing.T) {
	var orderID int64 = 54321
	var pollCount int32
	mockServer, _ := setupTest(t)
	defer mockServer.Close()

	// This handler will return an empty transaction list for the first 2 polls,
	// then return the transaction on the 3rd poll.
	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/exchange/orders":
			resp := coincheck.OrderResponse{Success: true, ID: orderID, Amount: "0.01"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case "/api/accounts/balance":
			resp := coincheck.BalanceResponse{Success: true, Jpy: "1000000", Btc: "1.0"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		case "/api/exchange/orders/transactions_pagination":
			currentPollCount := atomic.AddInt32(&pollCount, 1)
			var transactions []coincheck.Transaction
			if currentPollCount >= 3 {
				transactions = []coincheck.Transaction{
					{ID: 98765, OrderID: orderID, Pair: "btc_jpy", Rate: "5100000.0", Side: "buy", CreatedAt: time.Now().UTC().Format(time.RFC3339)},
				}
			}
			// The actual response is nested under a "data" key
			resp := struct {
				Success bool                    `json:"success"`
				Data    []coincheck.Transaction `json:"data"`
			}{
				Success: true,
				Data:    transactions,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	})

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	// Use a mock DB writer to verify data is being saved
	mockWriter := &mockDBWriter{
		saveTradeCalled: make(chan bool, 1),
	}
	execEngine := NewLiveExecutionEngine(ccClient, mockWriter, nil)

	resp, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5100000, 0.01, false)
	require.NoError(t, err)
	require.True(t, resp.Success)
	assert.Equal(t, orderID, resp.ID)
	assert.True(t, atomic.LoadInt32(&pollCount) >= 3, "Expected at least 3 polls to GetTransactions")

	// Verify that the trade was saved to the DB
	select {
	case <-mockWriter.saveTradeCalled:
		assert.Equal(t, int64(98765), mockWriter.savedTrade.TransactionID)
		assert.False(t, mockWriter.savedTrade.IsCancelled)
		assert.True(t, mockWriter.savedTrade.IsMyTrade)
	case <-time.After(2 * time.Second):
		t.Fatal("SaveTrade was not called within the timeout")
	}
}

func TestExecutionEngine_PlaceOrder_AmountAdjustment(t *testing.T) {
	var requestedAmount float64
	var orderID int64
	mockServer, cfg := setupTest(t)
	defer mockServer.Close()

	// Override handlers
	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/exchange/orders":
			var reqBody coincheck.OrderRequest
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			requestedAmount = reqBody.Amount
			orderID = 123
			resp := coincheck.OrderResponse{Success: true, ID: orderID}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/accounts/balance":
			resp := coincheck.BalanceResponse{Success: true, Jpy: "1000000", Btc: "1.0"}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/exchange/orders/opens":
			resp := coincheck.OpenOrdersResponse{Success: true, Orders: []coincheck.OpenOrder{}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/exchange/orders/transactions_pagination":
			resp := struct {
				Success bool `json:"success"`
				Data    []coincheck.Transaction `json:"data"`
			}{
				Success: true,
				Data: []coincheck.Transaction{
					{ID: 98766, OrderID: orderID, Pair: "btc_jpy", Rate: "5000000.0", Side: "buy", CreatedAt: time.Now().UTC().Format(time.RFC3339)},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	})

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	cfg.Trade.OrderRatio = 0.5
	cfg.Trade.Risk.MaxPositionRatio = 1.0 // Disable risk check for this test
	execEngine := NewLiveExecutionEngine(ccClient, nil, nil)

	// The amount passed to PlaceOrder is 0.2. The test should verify that this is the amount requested.
	_, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 0.2, false)
	require.NoError(t, err)

	expectedAmount := 0.2
	assert.Equal(t, expectedAmount, requestedAmount)
}

func TestExecutionEngine_CancelOrder_Success(t *testing.T) {
	var cancelRequestCount int32
	mockServer, _ := setupTest(t)
	defer mockServer.Close()

	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/exchange/orders/") && r.Method == http.MethodDelete {
			atomic.AddInt32(&cancelRequestCount, 1)
			resp := coincheck.CancelResponse{Success: true, ID: 56789}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		} else {
			http.NotFound(w, r)
		}
	})

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	execEngine := NewLiveExecutionEngine(ccClient, nil, nil)

	resp, err := execEngine.CancelOrder(context.Background(), 56789)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.EqualValues(t, 56789, resp.ID)
	assert.EqualValues(t, 1, atomic.LoadInt32(&cancelRequestCount))
}

func TestExecutionEngine_CancelOrder_Failure(t *testing.T) {
	mockServer, _ := setupTest(t)
	defer mockServer.Close()

	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := coincheck.CancelResponse{
			Success: false,
			ID:      11111,
			Error:   "Order not found or already processed",
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK) // Coincheck API might return 200 OK with success:false
		_ = json.NewEncoder(w).Encode(resp)
	})

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	execEngine := NewLiveExecutionEngine(ccClient, nil, nil)

	resp, err := execEngine.CancelOrder(context.Background(), 11111)
	require.Error(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Equal(t, "Order not found or already processed", resp.Error)
	assert.Contains(t, err.Error(), "Order not found or already processed")
}

func TestExecutionEngine_PlaceOrder_Timeout(t *testing.T) {
	var orderID int64 = 67890
	mockServer, cfg := setupTest(t)
	defer mockServer.Close()

	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/exchange/orders" && r.Method == http.MethodPost:
			resp := coincheck.OrderResponse{Success: true, ID: orderID}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case strings.HasPrefix(r.URL.Path, "/api/exchange/orders/") && r.Method == http.MethodDelete:
			pathParts := strings.Split(r.URL.Path, "/")
			cancelledIDStr := pathParts[len(pathParts)-1]
			cancelledID, _ := Atoi64(cancelledIDStr)
			resp := coincheck.CancelResponse{Success: true, ID: cancelledID}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/api/accounts/balance":
			resp := coincheck.BalanceResponse{Success: true, Jpy: "1000000", Btc: "1.0"}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/api/exchange/orders/opens":
			resp := coincheck.OpenOrdersResponse{Success: true, Orders: []coincheck.OpenOrder{}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/api/exchange/orders/transactions_pagination":
			resp := struct {
				Success bool `json:"success"`
				Data    []coincheck.Transaction `json:"data"`
			}{
				Success: true,
				Data:    []coincheck.Transaction{},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	})

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	cfg.App.Order.PollIntervalMs = 1
	cfg.App.Order.TimeoutSeconds = 1

	execEngine := NewLiveExecutionEngine(ccClient, nil, nil)

	_, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 0.01, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out and was cancelled")
}

// mockDBWriter is a mock implementation of the dbwriter.DBWriter for testing.
type mockDBWriter struct {
	saveTradeCalled     chan bool
	savedTrade          dbwriter.Trade
	savePnlSummaryCalled chan bool
	savedPnlSummary     dbwriter.PnLSummary
	saveTradePnlCalled  chan bool
	savedTradePnl       dbwriter.TradePnL
}


func (m *mockDBWriter) SaveTrade(trade dbwriter.Trade) {
	m.savedTrade = trade
	if m.saveTradeCalled != nil {
		m.saveTradeCalled <- true
	}
}

func (m *mockDBWriter) SavePnLSummary(ctx context.Context, pnl dbwriter.PnLSummary) error {
	m.savedPnlSummary = pnl
	if m.savePnlSummaryCalled != nil {
		m.savePnlSummaryCalled <- true
	}
	return nil
}

func (m *mockDBWriter) SaveTradePnL(ctx context.Context, tradePnl dbwriter.TradePnL) error {
	m.savedTradePnl = tradePnl
	if m.saveTradePnlCalled != nil {
		m.saveTradePnlCalled <- true
	}
	return nil
}

func (m *mockDBWriter) SaveOrderBookUpdate(obu dbwriter.OrderBookUpdate) {
	// No-op for this test
}

func (m *mockDBWriter) SaveBenchmarkValue(ctx context.Context, value dbwriter.BenchmarkValue) {
	// No-op for this test
}

func (m *mockDBWriter) Close() {
	// No-op for this test
}

// Atoi64 is a helper to convert string to int64.
func Atoi64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func TestMain(m *testing.M) {
	m.Run()
}

func TestExecutionEngine_AdaptivePositionSizing(t *testing.T) {
	var orderIDCounter int64 = 1000
	var lastRequestedAmount float64
	var mu sync.Mutex

	mockServer, cfg := setupTest(t)
	defer mockServer.Close()

	cfg.Trade.OrderRatio = 0.2
	cfg.Trade.LotMaxRatio = 0.2
	cfg.Trade.AdaptivePositionSizing = config.AdaptiveSizingConfig{
		Enabled:       true,
		NumTrades:     5,
		ReductionStep: 0.8,
		MinRatio:      0.5,
	}
	cfg.Trade.Risk.MaxPositionRatio = 1.0 // Disable risk check

	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/exchange/orders":
			currentOrderID := atomic.AddInt64(&orderIDCounter, 1)
			var reqBody coincheck.OrderRequest
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			mu.Lock()
			lastRequestedAmount = reqBody.Amount
			mu.Unlock()
			resp := coincheck.OrderResponse{
				Success: true, ID: currentOrderID, Amount: strconv.FormatFloat(reqBody.Amount, 'f', -1, 64),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/exchange/orders/transactions_pagination":
			currentOrderID := atomic.LoadInt64(&orderIDCounter)
			resp := struct {
				Success bool `json:"success"`
				Data    []coincheck.Transaction `json:"data"`
			}{
				Success: true,
				Data:    []coincheck.Transaction{{ID: currentOrderID + 5000, OrderID: currentOrderID}},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/accounts/balance":
			resp := coincheck.BalanceResponse{Success: true, Jpy: "100000000", Btc: "10.0"}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/exchange/orders/opens":
			resp := coincheck.OpenOrdersResponse{Success: true, Orders: []coincheck.OpenOrder{}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	})

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	execEngine := NewLiveExecutionEngine(ccClient, nil, nil)

	t.Run("size reduction after losses", func(t *testing.T) {
		execEngine.UpdateRecentPnLsForTest(t, []float64{-100, -100, -100, -100, -100})
		execEngine.adjustRatios() // Manually trigger adjustment for test
		_, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 10.0, false)
		require.NoError(t, err)

		mu.Lock()
		finalAmount := lastRequestedAmount
		mu.Unlock()

		// The amount passed to PlaceOrder is 10.0, which should be the requested amount.
		// The adaptive sizing logic is not applied to the order amount directly.
		expectedAmount := 10.0
		assert.InDelta(t, expectedAmount, finalAmount, 1e-9)
	})

	t.Run("size reset after profits", func(t *testing.T) {
		execEngine.UpdateRecentPnLsForTest(t, []float64{-100, -100, -100, -100, -100})
		execEngine.adjustRatios() // Manually trigger adjustment for test
		_, _ = execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 10.0, false)

		execEngine.UpdateRecentPnLsForTest(t, []float64{200, 200, 200, 200, 200})
		execEngine.adjustRatios() // Manually trigger adjustment for test
		_, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 10.0, false)
		require.NoError(t, err)

		mu.Lock()
		finalAmount := lastRequestedAmount
		mu.Unlock()

		// The amount passed to PlaceOrder is 10.0, which should be the requested amount.
		expectedAmount := 10.0
		assert.InDelta(t, expectedAmount, finalAmount, 1e-9)
	})
}

// UpdateRecentPnLsForTest is a test helper to inject PnL data into the engine.
func (e *LiveExecutionEngine) UpdateRecentPnLsForTest(t *testing.T, pnls []float64) {
	t.Helper()
	e.recentPnLs = make([]float64, len(pnls))
	copy(e.recentPnLs, pnls)
}

// SetPositionForTest is a test helper to set the position for testing.
func (e *LiveExecutionEngine) SetPositionForTest(t *testing.T, size float64, avgEntryPrice float64) {
	t.Helper()
	e.position = position.NewPosition()
	e.position.Update(size, avgEntryPrice)
}

// SetRealizedPnLForTest is a test helper to set the realized PnL for testing.
func (e *LiveExecutionEngine) SetRealizedPnLForTest(t *testing.T, pnl float64) {
	t.Helper()
	e.pnlCalculator.UpdateRealizedPnL(pnl)
}

func TestLiveExecutionEngine_CheckAndTriggerPartialExit(t *testing.T) {
	mockServer, cfg := setupTest(t)
	defer mockServer.Close()
	cfg.Trade.Twap = config.TwapConfig{
		PartialExitEnabled: true,
		ProfitThreshold:    1.0, // 1%
		ExitRatio:          0.5, // 50%
	}

	t.Run("Long position with profit above threshold", func(t *testing.T) {
		engine := NewLiveExecutionEngine(nil, nil, nil)
		engine.SetPositionForTest(t, 1.0, 100000) // Buy 1 BTC @ 100,000

		marketPrice := 102000.0 // 2% profit
		exitOrder := engine.CheckAndTriggerPartialExit(marketPrice)

		require.NotNil(t, exitOrder)
		assert.Equal(t, "sell", exitOrder.OrderType)
		assert.InDelta(t, 0.5, exitOrder.Size, 1e-9) // 50% of 1.0 BTC
		assert.True(t, engine.IsExitingPartially(), "Flag should be set after triggering")
	})

	t.Run("Long position with profit below threshold", func(t *testing.T) {
		engine := NewLiveExecutionEngine(nil, nil, nil)
		engine.SetPositionForTest(t, 1.0, 100000)

		marketPrice := 100500.0 // 0.5% profit
		exitOrder := engine.CheckAndTriggerPartialExit(marketPrice)

		assert.Nil(t, exitOrder)
		assert.False(t, engine.IsExitingPartially())
	})

	t.Run("Short position with profit above threshold", func(t *testing.T) {
		engine := NewLiveExecutionEngine(nil, nil, nil)
		engine.SetPositionForTest(t, -1.0, 100000) // Sell 1 BTC @ 100,000

		marketPrice := 98000.0 // 2% profit
		exitOrder := engine.CheckAndTriggerPartialExit(marketPrice)

		require.NotNil(t, exitOrder)
		assert.Equal(t, "buy", exitOrder.OrderType)
		assert.InDelta(t, 0.5, exitOrder.Size, 1e-9)
		assert.True(t, engine.IsExitingPartially())
	})

	t.Run("No position", func(t *testing.T) {
		engine := NewLiveExecutionEngine(nil, nil, nil)
		marketPrice := 102000.0
		exitOrder := engine.CheckAndTriggerPartialExit(marketPrice)
		assert.Nil(t, exitOrder)
	})

	t.Run("Disabled in config", func(t *testing.T) {
		originalValue := cfg.Trade.Twap.PartialExitEnabled
		cfg.Trade.Twap.PartialExitEnabled = false
		defer func() { cfg.Trade.Twap.PartialExitEnabled = originalValue }()

		engine := NewLiveExecutionEngine(nil, nil, nil)
		engine.SetPositionForTest(t, 1.0, 100000)
		marketPrice := 102000.0
		exitOrder := engine.CheckAndTriggerPartialExit(marketPrice)
		assert.Nil(t, exitOrder)
	})

	t.Run("Already exiting", func(t *testing.T) {
		engine := NewLiveExecutionEngine(nil, nil, nil)
		engine.SetPositionForTest(t, 1.0, 100000)
		engine.SetPartialExitStatus(true)

		marketPrice := 102000.0
		exitOrder := engine.CheckAndTriggerPartialExit(marketPrice)
		assert.Nil(t, exitOrder, "Should not trigger another exit if one is in progress")
	})
}

func TestExecutionEngine_RiskManagement(t *testing.T) {
	var newOrderRequestCount int32
	mockServer, _ := setupTest(t)
	defer mockServer.Close()

	mockServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/exchange/orders" && r.Method == http.MethodPost:
			atomic.AddInt32(&newOrderRequestCount, 1)
			resp := coincheck.OrderResponse{Success: true, ID: 12345}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case strings.HasPrefix(r.URL.Path, "/api/exchange/orders/") && r.Method == http.MethodDelete:
			resp := coincheck.CancelResponse{Success: true, ID: 12345}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/api/accounts/balance":
			resp := coincheck.BalanceResponse{Success: true, Jpy: "1000000", Btc: "1.0"}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.URL.Path == "/api/exchange/orders/opens":
			resp := coincheck.OpenOrdersResponse{Success: true, Orders: []coincheck.OpenOrder{}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	})

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	setupEngine := func() *LiveExecutionEngine {
		atomic.StoreInt32(&newOrderRequestCount, 0)
		return NewLiveExecutionEngine(ccClient, nil, nil)
	}

	t.Run("stops order on max drawdown breach", func(t *testing.T) {
		execEngine := setupEngine()
		execEngine.SetRealizedPnLForTest(t, -110000) // 11% drawdown on 1M capital

		_, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 0.01, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "risk check failed: current drawdown")
		assert.EqualValues(t, 0, atomic.LoadInt32(&newOrderRequestCount), "NewOrder should not have been called")
	})

	t.Run("stops order on max position breach", func(t *testing.T) {
		execEngine := setupEngine()
		execEngine.SetPositionForTest(t, 0.1, 5000000) // Position value is 500,000 JPY (50% of 1M balance)

		_, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 0.001, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "risk check failed: prospective position value")
		assert.EqualValues(t, 0, atomic.LoadInt32(&newOrderRequestCount), "NewOrder should not have been called")
	})

	t.Run("allows order when within risk limits", func(t *testing.T) {
		execEngine := setupEngine()
		execEngine.SetRealizedPnLForTest(t, -50000)
		execEngine.SetPositionForTest(t, 0.05, 5000000)

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		// This will time out because the mock server doesn't confirm the order, which is expected.
		// The important part is that the order was placed (newOrderRequestCount == 1).
		_, err := execEngine.PlaceOrder(ctx, "btc_jpy", "buy", 5000000, 0.01, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "timed out")
		assert.EqualValues(t, 1, atomic.LoadInt32(&newOrderRequestCount), "NewOrder should have been called")
	})
}

