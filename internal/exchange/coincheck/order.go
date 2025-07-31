// Package coincheck handles interactions with the Coincheck exchange.
package coincheck

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/your-org/obi-scalp-bot/pkg/logger"
	// "github.com/google/uuid" // No longer used after removing newNonce
)

var (
	// defaultBaseURL can be overridden for testing.
	defaultBaseURL = "https://coincheck.com"
)

// GetBaseURL returns the current base URL used by the client.
// Useful for testing to confirm the target URL.
func GetBaseURL() string {
	return defaultBaseURL
}

// SetBaseURL sets the base URL for the client.
// This is intended for use in tests to redirect requests to a mock server.
func SetBaseURL(url string) {
	defaultBaseURL = url
}

// Client provides methods to interact with the Coincheck API.
type Client struct {
	apiKey     string
	secretKey  string
	httpClient *http.Client
}

// NewClient creates a new Coincheck API client.
func NewClient(apiKey, secretKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		secretKey:  secretKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) newRequest(method, endpoint string, body io.Reader) (*http.Request, error) {
	url := defaultBaseURL + endpoint // Use the package-level variable
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}

	// Generate a nonce using the current time in nanoseconds for each request.
	// This approach is robust against application restarts, unlike an in-memory counter.
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	message := nonce + url
	if body != nil && body != http.NoBody {
		buf := new(bytes.Buffer)
		// TeeReader creates a reader that writes to buf as it is read from body.
		// This allows us to capture the request body for the signature without consuming it.
		teeReader := io.TeeReader(body, buf)
		bodyBytes, err := io.ReadAll(teeReader)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body for signature: %w", err)
		}
		message += string(bodyBytes)

		// After reading, the original body is consumed, but buf contains the content.
		// We must reset the request's body to be a new reader from the captured bytes
		// so it can be sent by the http.Client.
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	mac := hmac.New(sha256.New, []byte(c.secretKey))
	_, err = mac.Write([]byte(message))
	if err != nil {
		return nil, err // Should not happen
	}
	signature := hex.EncodeToString(mac.Sum(nil))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("ACCESS-KEY", c.apiKey)
	req.Header.Set("ACCESS-NONCE", nonce)
	req.Header.Set("ACCESS-SIGNATURE", signature)

	return req, nil
}

// NewOrder sends a new order request to Coincheck.
func (c *Client) NewOrder(reqBody OrderRequest) (*OrderResponse, time.Time, error) {
	endpoint := "/api/exchange/orders"

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to marshal order request: %w", err)
	}

	httpReq, err := c.newRequest(http.MethodPost, endpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("failed to create new order request: %w", err)
	}

	orderSentTime := time.Now().UTC()
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, orderSentTime, fmt.Errorf("failed to execute new order request: %w", err)
	}
	defer resp.Body.Close()

	var orderResp OrderResponse
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, orderSentTime, fmt.Errorf("failed to read new order response body (status: %d): %w", resp.StatusCode, err)
	}

	if err := json.Unmarshal(bodyBytes, &orderResp); err != nil {
		return nil, orderSentTime, fmt.Errorf("failed to decode new order response (status: %d, body: %s): %w. Raw request body for POST: %s", resp.StatusCode, string(bodyBytes), err, string(jsonBody))
	}

	// Check for API-level errors if success is false
	if !orderResp.Success {
		// If the response includes an error field, use it.
		if orderResp.Error != "" {
			// Handle cases where error_description might be available
			if orderResp.ErrorDescription != "" {
				return &orderResp, orderSentTime, fmt.Errorf("coincheck API error on new order: %s - %s", orderResp.Error, orderResp.ErrorDescription)
			}
			return &orderResp, orderSentTime, fmt.Errorf("coincheck API error on new order: %s", orderResp.Error)
		}
		// Fallback error message if no specific error field is populated
		return &orderResp, orderSentTime, fmt.Errorf("coincheck API returned success=false for new order, status: %d, ID: %d", resp.StatusCode, orderResp.ID)
	}

	return &orderResp, orderSentTime, nil
}

// CancelOrder sends a cancel order request to Coincheck.
func (c *Client) CancelOrder(orderID int64) (*CancelResponse, error) {
	endpoint := fmt.Sprintf("/api/exchange/orders/%d", orderID)

	httpReq, err := c.newRequest(http.MethodDelete, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cancel order request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute cancel order request: %w", err)
	}
	defer resp.Body.Close()

	var cancelResp CancelResponse
	if err := json.NewDecoder(resp.Body).Decode(&cancelResp); err != nil {
		return nil, fmt.Errorf("failed to decode cancel order response (status: %d): %w", resp.StatusCode, err)
	}

	if !cancelResp.Success {
		if cancelResp.Error != "" {
			return &cancelResp, fmt.Errorf("coincheck API error on cancel order: %s", cancelResp.Error)
		}
		return &cancelResp, fmt.Errorf("coincheck API returned success=false for cancel order, status: %d, ID: %d", resp.StatusCode, cancelResp.ID)
	}

	return &cancelResp, nil
}

// GetBalance retrieves the account balance from Coincheck.
func (c *Client) GetBalance() (*BalanceResponse, error) {
	endpoint := "/api/accounts/balance"

	httpReq, err := c.newRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create get balance request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute get balance request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read get balance response body (status: %d): %w", resp.StatusCode, err)
	}

	// Log the raw response body for debugging purposes.
	// This helps verify the structure and content of the data received from Coincheck.
	logger.Debugf("GetBalance response body: %s", string(bodyBytes))

	var balanceResp BalanceResponse
	if err := json.Unmarshal(bodyBytes, &balanceResp); err != nil {
		return nil, fmt.Errorf("failed to decode get balance response (status: %d, body: %s): %w", resp.StatusCode, string(bodyBytes), err)
	}

	if !balanceResp.Success {
		if balanceResp.Error != "" {
			return &balanceResp, fmt.Errorf("coincheck API error on get balance: %s", balanceResp.Error)
		}
		return &balanceResp, fmt.Errorf("coincheck API returned success=false for get balance, status: %d", resp.StatusCode)
	}

	return &balanceResp, nil
}

// GetOpenOrders retrieves the open orders from Coincheck.
func (c *Client) GetOpenOrders() (*OpenOrdersResponse, error) {
	endpoint := "/api/exchange/orders/opens"

	httpReq, err := c.newRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create get open orders request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute get open orders request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read get open orders response body (status: %d): %w", resp.StatusCode, err)
	}

	var openOrdersResp OpenOrdersResponse
	if err := json.Unmarshal(bodyBytes, &openOrdersResp); err != nil {
		return nil, fmt.Errorf("failed to decode get open orders response (status: %d, body: %s): %w", resp.StatusCode, string(bodyBytes), err)
	}

	if !openOrdersResp.Success {
		if openOrdersResp.Error != "" {
			return &openOrdersResp, fmt.Errorf("coincheck API error on get open orders: %s", openOrdersResp.Error)
		}
		return &openOrdersResp, fmt.Errorf("coincheck API returned success=false for get open orders, status: %d", resp.StatusCode)
	}

	return &openOrdersResp, nil
}

// GetTransactions retrieves the transaction history from Coincheck.
func (c *Client) GetTransactions(limit int) (*TransactionsResponse, error) {
	endpoint := fmt.Sprintf("/api/exchange/orders/transactions_pagination?limit=%d", limit)

	httpReq, err := c.newRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create get transactions request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute get transactions request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read get transactions response body (status: %d): %w", resp.StatusCode, err)
	}

	// The pagination endpoint returns a different structure, so we need to adapt.
	// The actual transaction data is in a "data" field.
	var paginatedResp struct {
		Success    bool                   `json:"success"`
		Data       []Transaction          `json:"data"`
		Pagination map[string]interface{} `json:"pagination"`
		Error      string                 `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &paginatedResp); err != nil {
		return nil, fmt.Errorf("failed to decode get transactions paginated response (status: %d, body: %s): %w", resp.StatusCode, string(bodyBytes), err)
	}

	if !paginatedResp.Success {
		if paginatedResp.Error != "" {
			return nil, fmt.Errorf("coincheck API error on get paginated transactions: %s", paginatedResp.Error)
		}
		return nil, fmt.Errorf("coincheck API returned success=false for get paginated transactions, status: %d", resp.StatusCode)
	}

	// Adapt the paginated response to the existing TransactionsResponse structure.
	transactionsResp := &TransactionsResponse{
		Success:      paginatedResp.Success,
		Transactions: paginatedResp.Data,
	}

	return transactionsResp, nil
}
