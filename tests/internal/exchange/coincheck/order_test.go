package coincheck_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
)

func TestGetTransactions(t *testing.T) {
	// Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that the limit parameter is correctly passed
		query := r.URL.Query()
		limit := query.Get("limit")
		assert.Equal(t, "10", limit, "Expected limit parameter to be 10")

		// Respond with a mock transaction response
		response := coincheck.TransactionsResponse{
			Success: true,
			Transactions: []coincheck.Transaction{
				{
					ID:      12345,
					OrderID: 54321,
					Rate:    "3000000",
					Pair:    "btc_jpy",
					Side:    "buy",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(response)
		if err != nil {
			t.Fatalf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	// Use the mock server's URL for the client
	coincheck.SetBaseURL(server.URL)

	// Create a new client
	client := coincheck.NewClient("test_key", "test_secret")

	// Call GetTransactions with a limit
	transactions, err := client.GetTransactions(10)

	// Assertions
	assert.NoError(t, err, "GetTransactions should not return an error")
	assert.NotNil(t, transactions, "Transactions should not be nil")
	assert.True(t, transactions.Success, "Expected success to be true")
	assert.Len(t, transactions.Transactions, 1, "Expected one transaction")
	assert.Equal(t, int64(12345), transactions.Transactions[0].ID, "Unexpected transaction ID")
}

func TestGetTransactions_NoLimit(t *testing.T) {
	// Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that the limit parameter is NOT present
		query := r.URL.Query()
		_, hasLimit := query["limit"]
		assert.False(t, hasLimit, "Expected no limit parameter")

		// Respond with a mock transaction response
		response := coincheck.TransactionsResponse{
			Success: true,
			Transactions: []coincheck.Transaction{
				{
					ID:      67890,
					OrderID: 98765,
					Rate:    "3100000",
					Pair:    "btc_jpy",
					Side:    "sell",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(response)
		if err != nil {
			t.Fatalf("Failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	// Use the mock server's URL for the client
	coincheck.SetBaseURL(server.URL)

	// Create a new client
	client := coincheck.NewClient("test_key", "test_secret")

	// Call GetTransactions without a limit
	transactions, err := client.GetTransactions(0)

	// Assertions
	assert.NoError(t, err, "GetTransactions should not return an error")
	assert.NotNil(t, transactions, "Transactions should not be nil")
	assert.True(t, transactions.Success, "Expected success to be true")
	assert.Len(t, transactions.Transactions, 1, "Expected one transaction")
	assert.Equal(t, int64(67890), transactions.Transactions[0].ID, "Unexpected transaction ID")
}
