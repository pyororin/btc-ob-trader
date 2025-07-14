// Package engine_test tests the execution engine.
package engine_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/engine"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
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
	mockServer := mockCoincheckServer(
		func(w http.ResponseWriter, r *http.Request) { // NewOrder Handler
			atomic.AddInt32(&requestCount, 1)
			var reqBody coincheck.OrderRequest
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				http.Error(w, "bad request body", http.StatusBadRequest)
				return
			}

			if reqBody.Pair != "btc_jpy" || reqBody.OrderType != "buy" || reqBody.Rate != 5000000 || reqBody.Amount != 0.01 {
				http.Error(w, "unexpected request body", http.StatusBadRequest)
				return
			}

			resp := coincheck.OrderResponse{
				Success:     true,
				ID:          12345,
				Rate:        "5000000.0",
				Amount:      "0.01",
				OrderType:   "buy",
				TimeInForce: reqBody.TimeInForce,
				Pair:        "btc_jpy",
				CreatedAt:   time.Now().Format(time.RFC3339),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		},
		nil, // No cancel handler
		func(w http.ResponseWriter, r *http.Request) { // Balance Handler
			resp := coincheck.BalanceResponse{
				Success: true,
				Jpy:     "1000000",
				Btc:     "1.0",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // OpenOrders Handler
			resp := coincheck.OpenOrdersResponse{
				Success: true,
				Orders:  []coincheck.OpenOrder{},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // Transactions Handler
			resp := coincheck.TransactionsResponse{
				Success: true,
				Transactions: []coincheck.Transaction{
					{
						ID:      98765,
						OrderID: 12345,
						Pair:    "btc_jpy",
						Rate:    "5000000.0",
						Side:    "buy",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		},
	)
	defer mockServer.Close()

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	testCfg := &config.Config{OrderRatio: 0.5}
	execEngine := engine.NewLiveExecutionEngine(ccClient, testCfg, nil)

	for i := 0; i < 50; i++ {
		resp, err := execEngine.PlaceOrder(context.Background(), "btc_jpy", "buy", 5000000, 0.01, false)
		if err != nil {
			t.Fatalf("PlaceOrder (iteration %d) returned an error: %v", i+1, err)
		}
		if resp == nil {
			t.Fatalf("PlaceOrder (iteration %d) response is nil", i+1)
		}
		if !resp.Success {
			t.Errorf("PlaceOrder (iteration %d) success was false. API Error: %s %s", i+1, resp.Error, resp.ErrorDescription)
		}
		if resp.ID == 0 {
			t.Errorf("PlaceOrder (iteration %d) ID was 0", i+1)
		}
	}
	if atomic.LoadInt32(&requestCount) != 50 {
		t.Errorf("Expected 50 requests to new order endpoint, got %d", atomic.LoadInt32(&requestCount))
	}

	atomic.StoreInt32(&requestCount, 0) // Reset counter
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
	if atomic.LoadInt32(&requestCount) != 1 {
		t.Errorf("Expected 1 request to new order endpoint for postOnly, got %d", atomic.LoadInt32(&requestCount))
	}
}

func TestExecutionEngine_PlaceOrder_AmountAdjustment(t *testing.T) {
	var adjustedAmount float64
	mockServer := mockCoincheckServer(
		func(w http.ResponseWriter, r *http.Request) { // NewOrder Handler
			var reqBody coincheck.OrderRequest
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				http.Error(w, "bad request body", http.StatusBadRequest)
				return
			}
			adjustedAmount = reqBody.Amount
			resp := coincheck.OrderResponse{Success: true, ID: 123}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		},
		nil,
		func(w http.ResponseWriter, r *http.Request) { // Balance Handler
			resp := coincheck.BalanceResponse{Success: true, Jpy: "1000000", Btc: "1.0"}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // OpenOrders Handler
			resp := coincheck.OpenOrdersResponse{Success: true, Orders: []coincheck.OpenOrder{}}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		},
		func(w http.ResponseWriter, r *http.Request) { // Transactions Handler
			resp := coincheck.TransactionsResponse{
				Success: true,
				Transactions: []coincheck.Transaction{
					{
						ID:      98766,
						OrderID: 123,
						Pair:    "btc_jpy",
						Rate:    "5000000.0",
						Side:    "buy",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		},
	)
	defer mockServer.Close()

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	testCfg := &config.Config{OrderRatio: 0.5}
	execEngine := engine.NewLiveExecutionEngine(ccClient, testCfg, nil)

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
			json.NewEncoder(w).Encode(resp)
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

	execEngine := engine.NewLiveExecutionEngine(ccClient, nil, nil)

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
			json.NewEncoder(w).Encode(resp)
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

	execEngine := engine.NewLiveExecutionEngine(ccClient, nil, nil)

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

func TestMain(m *testing.M) {
	m.Run()
}
