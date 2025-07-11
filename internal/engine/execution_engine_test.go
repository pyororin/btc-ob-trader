// Package engine_test tests the execution engine.
package engine_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/engine"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
)

// mockCoincheckServer is a helper to create a mock HTTP server for Coincheck API.
// It allows customizing handlers for different endpoints.
func mockCoincheckServer(
	newOrderHandler http.HandlerFunc,
	cancelOrderHandler http.HandlerFunc,
) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/exchange/orders", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && newOrderHandler != nil {
			newOrderHandler(w, r)
		} else if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/exchange/orders/") && cancelOrderHandler != nil {
			// This specific path with DELETE is usually /api/exchange/orders/{id}
			// The mux might need more specific registration or the handler needs to parse ID.
			// For simplicity, if it's a DELETE to the base, we'll assume it's a cancel for this test structure,
			// but ideally, the path for cancel is distinct or parsed.
			// Let's adjust the registration in the test functions for cancel.
			http.NotFound(w, r) // Should be handled by specific path
		} else {
			http.NotFound(w, r)
		}
	})
	// Specific path for cancel needed due to {id}
	mux.HandleFunc("/api/exchange/orders/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && cancelOrderHandler != nil {
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) == 4 && parts[0] == "api" && parts[1] == "exchange" && parts[2] == "orders" {
				// _, err := strconv.ParseInt(parts[3], 10, 64) // ID part
				// if err == nil {
				cancelOrderHandler(w,r)
				return
				// }
			}
		}
		http.NotFound(w, r)
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

			// timeInForceExpected := "" // This variable was unused.
			// This block was identified as an empty branch by staticcheck (SA9003)
			// The logic for timeInForceExpected was not actually used to assert or modify behavior based on URL query.
			// The actual test for postOnly is done by checking reqBody.TimeInForce which is set by the client.
			// Removing the problematic empty if block and unused timeInForceExpected variable.
			// if strings.Contains(r.URL.RawQuery, "postOnly=true") {
			// timeInForceExpected = "post_only"
			// }
			// if reqBody.TimeInForce != timeInForceExpected && !(reqBody.TimeInForce == "" && timeInForceExpected == "") {
			// }

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
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Logf("Error encoding response in mock server: %v", err) // Use t.Logf for test helper logging
				http.Error(w, "failed to encode response", http.StatusInternalServerError)
			}
		},
		nil, // No cancel handler needed for this test
	)
	defer mockServer.Close()

	// Override the coincheckBaseURL in the client (or pass mockServer.URL to client constructor if designed for it)
	// For this test, we assume NewClient can be configured or we modify its base URL.
	// Simplest for now: the client uses a passed-in URL or we modify the constant (not ideal for parallel tests).
	// Let's assume client is created with mockServer.URL
	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	// Modify client to use mock server's URL - this is a hack for the current client design
	// A better design would be ccClient := coincheck.NewClient("key", "secret", mockServer.URL)
	// Forcing the URL for testing:
	originalBaseURL := coincheck.GetBaseURL() // Need a getter for this, or make it configurable.
	coincheck.SetBaseURL(mockServer.URL)      // Need a setter for this.
	defer coincheck.SetBaseURL(originalBaseURL) // Reset after test

	execEngine := engine.NewExecutionEngine(ccClient)

	// Test DoD: Mock 50 注文全成功
	for i := 0; i < 50; i++ {
		resp, err := execEngine.PlaceOrder("btc_jpy", "buy", 5000000, 0.01, false)
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

	// Test with PostOnly
	atomic.StoreInt32(&requestCount, 0) // Reset counter
	respPostOnly, errPostOnly := execEngine.PlaceOrder("btc_jpy", "buy", 5000000, 0.01, true)
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

func TestExecutionEngine_PlaceOrder_Failure(t *testing.T) {
	mockServer := mockCoincheckServer(
		func(w http.ResponseWriter, r *http.Request) { // NewOrder Handler for failure
			resp := coincheck.OrderResponse{
				Success: false,
				Error:   "insufficient_balance",
                ErrorDescription: "Your JPY balance is not enough.",
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK) // Coincheck might return 200 OK with success:false
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Logf("Error encoding response in mock server (PlaceOrder_Failure): %v", err)
				http.Error(w, "failed to encode response", http.StatusInternalServerError)
			}
		},
		nil,
	)
	defer mockServer.Close()

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	execEngine := engine.NewExecutionEngine(ccClient)

	resp, err := execEngine.PlaceOrder("btc_jpy", "sell", 4000000, 0.1, false)
	if err == nil {
		t.Fatal("PlaceOrder was expected to return an error, but it didn't")
	}
	if resp == nil {
		t.Fatal("PlaceOrder response should not be nil on API error")
	}
	if resp.Success {
		t.Error("PlaceOrder success was true when expecting API error")
	}
	if resp.Error != "insufficient_balance" {
		t.Errorf("Expected API error 'insufficient_balance', got '%s'", resp.Error)
	}
	if !strings.Contains(err.Error(), "insufficient_balance") {
		// Comparing the full error string can be brittle. Checking for key parts.
		t.Errorf("Error message does not contain expected API error. Got: %s, Expected to contain: %s", err.Error(), "insufficient_balance")
	}
}


func TestExecutionEngine_CancelOrder_Success(t *testing.T) {
	var cancelRequestCount int32
	mockServer := mockCoincheckServer(
		nil, // No new order handler
		func(w http.ResponseWriter, r *http.Request) { // CancelOrder Handler
			atomic.AddInt32(&cancelRequestCount, 1)
			// Path: /api/exchange/orders/56789
			if !strings.HasSuffix(r.URL.Path, "/56789") {
				http.Error(w, "unexpected cancel URL", http.StatusBadRequest)
				return
			}
			resp := coincheck.CancelResponse{
				Success: true,
				ID:      56789,
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Logf("Error encoding response in mock server (CancelOrder_Success): %v", err)
				http.Error(w, "failed to encode response", http.StatusInternalServerError)
			}
		},
	)
	defer mockServer.Close()

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)

	execEngine := engine.NewExecutionEngine(ccClient)

	resp, err := execEngine.CancelOrder(56789)
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
            // Path: /api/exchange/orders/11111
            if !strings.HasSuffix(r.URL.Path, "/11111") {
                http.Error(w, "unexpected cancel URL for failure test", http.StatusBadRequest)
                return
            }
            resp := coincheck.CancelResponse{
                Success: false,
                ID:      11111, // Coincheck often returns the ID even on failure
                Error:   "Order not found or already processed",
            }
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusOK) // Or appropriate error code like 404, API dependent
            if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Logf("Error encoding response in mock server (CancelOrder_Failure): %v", err)
				http.Error(w, "failed to encode response", http.StatusInternalServerError)
			}
        },
    )
    defer mockServer.Close()

    ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
    originalBaseURL := coincheck.GetBaseURL()
    coincheck.SetBaseURL(mockServer.URL)
    defer coincheck.SetBaseURL(originalBaseURL)

    execEngine := engine.NewExecutionEngine(ccClient)

    resp, err := execEngine.CancelOrder(11111)
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


func TestExecutionEngine_ReplaceOrder_Success(t *testing.T) {
	var cancelCalled, newOrderCalled int32

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/123") { // Cancel part
			atomic.AddInt32(&cancelCalled, 1)
			resp := coincheck.CancelResponse{Success: true, ID: 123}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Logf("Error encoding cancel response in mock server (ReplaceOrder_Success): %v", err)
				http.Error(w, "failed to encode cancel response", http.StatusInternalServerError)
			}
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/exchange/orders" { // New order part
			atomic.AddInt32(&newOrderCalled, 1)
			var reqBody coincheck.OrderRequest
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				t.Logf("Error decoding request body in mock server (ReplaceOrder_Success): %v", err)
				http.Error(w, "bad request body", http.StatusBadRequest)
				return
			}
			resp := coincheck.OrderResponse{
				Success:     true,
				ID:          789,
				Rate:        fmt.Sprintf("%.1f", reqBody.Rate),
				Amount:      fmt.Sprintf("%.2f", reqBody.Amount),
				OrderType:   reqBody.OrderType,
				TimeInForce: reqBody.TimeInForce,
				Pair:        reqBody.Pair,
				CreatedAt:   time.Now().Format(time.RFC3339),
			}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Logf("Error encoding new order response in mock server (ReplaceOrder_Success): %v", err)
				http.Error(w, "failed to encode new order response", http.StatusInternalServerError)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer mockServer.Close()

	ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
	originalBaseURL := coincheck.GetBaseURL()
	coincheck.SetBaseURL(mockServer.URL)
	defer coincheck.SetBaseURL(originalBaseURL)
	execEngine := engine.NewExecutionEngine(ccClient)

	replaceResp, err := execEngine.ReplaceOrder(123, "btc_jpy", "sell", 6000000, 0.05, true)
	if err != nil {
		t.Fatalf("ReplaceOrder returned an error: %v", err)
	}
	if !replaceResp.Success {
		t.Errorf("ReplaceOrder new order part success was false. API Error: %s %s", replaceResp.Error, replaceResp.ErrorDescription)
	}
	if replaceResp.ID != 789 {
		t.Errorf("Expected new order ID 789 from replace, got %d", replaceResp.ID)
	}
	if replaceResp.TimeInForce != "post_only" {
		t.Errorf("Expected new order TimeInForce to be 'post_only', got '%s'", replaceResp.TimeInForce)
	}
	if atomic.LoadInt32(&cancelCalled) != 1 {
		t.Errorf("Expected cancel endpoint to be called once, got %d", atomic.LoadInt32(&cancelCalled))
	}
	if atomic.LoadInt32(&newOrderCalled) != 1 {
		t.Errorf("Expected new order endpoint to be called once, got %d", atomic.LoadInt32(&newOrderCalled))
	}
}

func TestExecutionEngine_ReplaceOrder_CancelFails(t *testing.T) {
    mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/321") {
            resp := coincheck.CancelResponse{Success: false, ID: 321, Error: "cannot_cancel_already_filled"}
            if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Logf("Error encoding response in mock server (ReplaceOrder_CancelFails): %v", err)
				http.Error(w, "failed to encode response", http.StatusInternalServerError)
			}
            return
        }
        // New order should not be called if cancel fails critically
        http.NotFound(w, r)
    }))
    defer mockServer.Close()

    ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
    originalBaseURL := coincheck.GetBaseURL()
    coincheck.SetBaseURL(mockServer.URL)
    defer coincheck.SetBaseURL(originalBaseURL)
    execEngine := engine.NewExecutionEngine(ccClient)

    _, err := execEngine.ReplaceOrder(321, "btc_jpy", "buy", 5500000, 0.02, false)
    if err == nil {
        t.Fatal("ReplaceOrder was expected to return an error due to cancel failure, but it didn't")
    }
    if !strings.Contains(err.Error(), "cannot_cancel_already_filled") {
        t.Errorf("Expected error message to contain 'cannot_cancel_already_filled', got: %s", err.Error())
    }
}

func TestExecutionEngine_ReplaceOrder_NewOrderFails(t *testing.T) {
    var cancelCalledCount int32
    mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/456") {
            atomic.AddInt32(&cancelCalledCount, 1)
            resp := coincheck.CancelResponse{Success: true, ID: 456}
            if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Logf("Error encoding cancel response in mock server (ReplaceOrder_NewOrderFails): %v", err)
				http.Error(w, "failed to encode cancel response", http.StatusInternalServerError)
			}
            return
        }
        if r.Method == http.MethodPost && r.URL.Path == "/api/exchange/orders" {
            resp := coincheck.OrderResponse{Success: false, Error: "some_new_order_error", ErrorDescription: "Details about new order error"}
            if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Logf("Error encoding new order response in mock server (ReplaceOrder_NewOrderFails): %v", err)
				http.Error(w, "failed to encode new order response", http.StatusInternalServerError)
			}
            return
        }
        http.NotFound(w, r)
    }))
    defer mockServer.Close()

    ccClient := coincheck.NewClient("test_api_key", "test_secret_key")
    originalBaseURL := coincheck.GetBaseURL()
    coincheck.SetBaseURL(mockServer.URL)
    defer coincheck.SetBaseURL(originalBaseURL)
    execEngine := engine.NewExecutionEngine(ccClient)

    resp, err := execEngine.ReplaceOrder(456, "btc_jpy", "sell", 6100000, 0.03, false)
    if err == nil {
        t.Fatal("ReplaceOrder was expected to return an error due to new order failure, but it didn't")
    }
     if resp == nil {
        t.Fatal("ReplaceOrder response should not be nil on new order API error")
    }
    if resp.Success {
        t.Error("New order part of ReplaceOrder success was true when expecting API error")
    }
    if !strings.Contains(err.Error(), "some_new_order_error") {
        t.Errorf("Expected error message to contain 'some_new_order_error', got: %s", err.Error())
    }
    if atomic.LoadInt32(&cancelCalledCount) != 1 {
        t.Errorf("Expected cancel to be called once even if new order fails, got %d", atomic.LoadInt32(&cancelCalledCount))
    }
}

// Note: To make SetBaseURL/GetBaseURL work for testing, coincheck/client.go would need:
// var defaultBaseURL = "https://coincheck.com"
// func GetBaseURL() string { return defaultBaseURL }
// func SetBaseURL(url string) { defaultBaseURL = url }
// And the client's newRequest method should use this defaultBaseURL.
// This is a common pattern for making HTTP clients testable.
// For the purpose of this task, I'll assume such utility functions can be added to coincheck package.
// If not, the http client in coincheck.Client would need to be replaceable, or the URL passed in constructor.
// For now, I will add these functions to the `coincheck` package.

func TestMain(m *testing.M) {
	// Setup that might be needed before running tests, e.g. setting up the mock base URL mechanism if needed.
	// For now, direct calls to SetBaseURL in each test that needs the mock server.
	m.Run()
}
