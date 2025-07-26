package coincheck

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetTransactions_Pagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/exchange/orders/transactions_pagination?limit=50", r.URL.String())
		assert.Equal(t, http.MethodGet, r.Method)

		// Create a mock paginated response
		mockResponse := struct {
			Success    bool          `json:"success"`
			Data       []Transaction `json:"data"`
			Pagination struct{}      `json:"pagination"`
		}{
			Success: true,
			Data: []Transaction{
				{
					ID:        123,
					OrderID:   456,
					CreatedAt: time.Now().Format(time.RFC3339),
					Pair:      "btc_jpy",
					Rate:      "5000000",
					Side:      "buy",
				},
			},
			Pagination: struct{}{},
		}

		jsonBytes, _ := json.Marshal(mockResponse)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonBytes)
	}))
	defer server.Close()

	// Use the mock server's URL
	SetBaseURL(server.URL)
	defer SetBaseURL("https://coincheck.com") // Reset after test

	client := NewClient("test_key", "test_secret")
	resp, err := client.GetTransactions(50)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Len(t, resp.Transactions, 1)
	assert.Equal(t, int64(123), resp.Transactions[0].ID)
	assert.Equal(t, "btc_jpy", resp.Transactions[0].Pair)
}

func TestGetTransactions_ApiError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock an error response from the API
		mockResponse := map[string]interface{}{
			"success": false,
			"error":   "Invalid API key",
		}
		jsonBytes, _ := json.Marshal(mockResponse)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write(jsonBytes)
	}))
	defer server.Close()

	SetBaseURL(server.URL)
	defer SetBaseURL("https://coincheck.com")

	client := NewClient("invalid_key", "invalid_secret")
	_, err := client.GetTransactions(10)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "coincheck API error on get paginated transactions: Invalid API key")
}
