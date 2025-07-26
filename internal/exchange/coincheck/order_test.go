package coincheck

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewOrder_RequestBodyIsPreserved(t *testing.T) {
	var capturedBody string
	var capturedHeaders http.Header

	// Mock server to capture the request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("Failed to read request body: %v", err)
		}
		capturedBody = string(bodyBytes)
		capturedHeaders = r.Header

		// Send a valid JSON response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(OrderResponse{
			Success: true,
			ID:      12345,
			Rate:    "3000000",
			Amount:  "0.01",
			Pair:    "btc_jpy",
		})
	}))
	defer server.Close()

	// Override the default base URL to point to our mock server
	originalBaseURL := GetBaseURL()
	SetBaseURL(server.URL)
	defer SetBaseURL(originalBaseURL)

	client := NewClient("test_api_key", "test_secret_key")

	orderReq := OrderRequest{
		Pair:      "btc_jpy",
		OrderType: "buy",
		Rate:      3000000,
		Amount:    0.01,
	}

	_, _, err := client.NewOrder(orderReq)
	if err != nil {
		t.Fatalf("NewOrder failed: %v", err)
	}

	// 1. Verify that the request body is not empty and is valid JSON
	if capturedBody == "" {
		t.Error("Expected request body to be non-empty, but it was empty.")
	}

	var sentOrderReq OrderRequest
	err = json.Unmarshal([]byte(capturedBody), &sentOrderReq)
	if err != nil {
		t.Fatalf("Failed to unmarshal captured request body: %v. Body was: %s", err, capturedBody)
	}

	// 2. Verify the content of the request body
	if sentOrderReq.Pair != orderReq.Pair || sentOrderReq.OrderType != orderReq.OrderType || sentOrderReq.Rate != orderReq.Rate || sentOrderReq.Amount != orderReq.Amount {
		t.Errorf("Request body content mismatch. Got %+v, want %+v", sentOrderReq, orderReq)
	}

	// 3. Verify that the necessary headers were set
	if capturedHeaders.Get("ACCESS-KEY") == "" {
		t.Error("ACCESS-KEY header was not set.")
	}
	if capturedHeaders.Get("ACCESS-NONCE") == "" {
		t.Error("ACCESS-NONCE header was not set.")
	}
	if capturedHeaders.Get("ACCESS-SIGNATURE") == "" {
		t.Error("ACCESS-SIGNATURE header was not set.")
	}
}

func TestNewOrder_NonceIncrement(t *testing.T) {
	var nonces []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonces = append(nonces, r.Header.Get("ACCESS-NONCE"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(OrderResponse{Success: true, ID: 1})
	}))
	defer server.Close()

	originalBaseURL := GetBaseURL()
	SetBaseURL(server.URL)
	defer SetBaseURL(originalBaseURL)

	client := NewClient("test_api_key", "test_secret_key")
	orderReq := OrderRequest{Pair: "btc_jpy", OrderType: "buy", Rate: 3000000, Amount: 0.01}

	// Make two requests quickly
	_, _, _ = client.NewOrder(orderReq)
	_, _, _ = client.NewOrder(orderReq)

	if len(nonces) != 2 {
		t.Fatalf("Expected 2 requests, got %d", len(nonces))
	}
	if nonces[0] == "" || nonces[1] == "" {
		t.Fatal("Nonces should not be empty")
	}
	if nonces[0] >= nonces[1] {
		t.Errorf("Expected second nonce to be greater than the first. Got %s, then %s", nonces[0], nonces[1])
	}
}


func TestNewOrder_ApiError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest) // 400 Bad Request
		json.NewEncoder(w).Encode(OrderResponse{
			Success: false,
			Error:   "invalid_order_amount",
		})
	}))
	defer server.Close()

	originalBaseURL := GetBaseURL()
	SetBaseURL(server.URL)
	defer SetBaseURL(originalBaseURL)

	client := NewClient("test_api_key", "test_secret_key")
	orderReq := OrderRequest{Pair: "btc_jpy", OrderType: "buy", Rate: 3000000, Amount: 0.0001} // Invalid amount

	resp, _, err := client.NewOrder(orderReq)
	if err == nil {
		t.Fatal("Expected an error, but got nil")
	}
	if !strings.Contains(err.Error(), "invalid_order_amount") {
		t.Errorf("Expected error message to contain 'invalid_order_amount', but got: %s", err.Error())
	}
	if resp.Success {
		t.Error("Expected response success to be false")
	}
}

func TestNewRequest_Signature(t *testing.T) {
	client := NewClient("key", "secret")
	body := `{"pair":"btc_jpy","amount":"0.1","price":"3000000","order_type":"buy"}`

	// Set a fixed time for consistent nonce generation.
	// This is a bit tricky since the nonce is internal, but we can check the signature logic.
	// We will not check the exact signature, but ensure it's generated.

	req, err := client.newRequest("POST", "https://coincheck.com/api/exchange/orders", strings.NewReader(body))
	if err != nil {
		t.Fatalf("newRequest failed: %v", err)
	}

	signature := req.Header.Get("ACCESS-SIGNATURE")
	if signature == "" {
		t.Fatal("Signature should not be empty")
	}

	nonce := req.Header.Get("ACCESS-NONCE")
	if nonce == "" {
		t.Fatal("Nonce should not be empty")
	}
}

// Helper function to set a fixed time for testing nonce generation if needed.
// This requires modifying the client or using interfaces, which might be overkill.
// For now, we rely on the high-resolution timestamp to likely be unique.
func TestClient_Concurrency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond) // Simulate network latency
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(OrderResponse{Success: true})
	}))
	defer server.Close()

	originalBaseURL := GetBaseURL()
	SetBaseURL(server.URL)
	defer SetBaseURL(originalBaseURL)

	client := NewClient("key", "secret")
	orderReq := OrderRequest{Pair: "btc_jpy", OrderType: "buy", Rate: 3000000, Amount: 0.01}

	numRequests := 10
	errChan := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			_, _, err := client.NewOrder(orderReq)
			errChan <- err
		}()
	}

	for i := 0; i < numRequests; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent request failed: %v", err)
		}
	}
}
