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

	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	message := nonce + url
	if body != nil && body != http.NoBody {
		buf := new(bytes.Buffer)
		_, err := buf.ReadFrom(body) // bodyの内容を読み取る
		if err != nil {
			return nil, fmt.Errorf("failed to read request body for signature: %w", err)
		}
		message += buf.String() // 読み取った内容をmessageに追加
		// bodyを再度設定するために、元のbodyを複製するか、bufを再度利用する
		// ここではbufを再度利用する
		req.Body = io.NopCloser(buf)
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
func (c *Client) NewOrder(reqBody OrderRequest) (*OrderResponse, error) {
	endpoint := "/api/exchange/orders"

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal order request: %w", err)
	}

	httpReq, err := c.newRequest(http.MethodPost, endpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create new order request: %w", err)
	}
	// After newRequest, the body of httpReq is already set with bytes.NewBuffer(jsonBody)
    // If newRequest consumes the body for signature generation, it should also reset it.
    // Let's ensure the body is correctly set here if it was consumed and not reset.
    // Based on the newRequest implementation, it seems to handle the body by reading and potentially needing a reset.
    // For POST, we definitely need the body.
    httpReq.Body = io.NopCloser(bytes.NewBuffer(jsonBody))


	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute new order request: %w", err)
	}
	defer resp.Body.Close()

	var orderResp OrderResponse
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read new order response body (status: %d): %w", resp.StatusCode, err)
	}

	if err := json.Unmarshal(bodyBytes, &orderResp); err != nil {
		return nil, fmt.Errorf("failed to decode new order response (status: %d, body: %s): %w. Raw request body for POST: %s", resp.StatusCode, string(bodyBytes), err, string(jsonBody))
	}

    // Check for API-level errors if success is false
	if !orderResp.Success {
        // If the response includes an error field, use it.
        if orderResp.Error != "" {
             // Handle cases where error_description might be available
            if orderResp.ErrorDescription != "" {
                return &orderResp, fmt.Errorf("coincheck API error on new order: %s - %s", orderResp.Error, orderResp.ErrorDescription)
            }
            return &orderResp, fmt.Errorf("coincheck API error on new order: %s", orderResp.Error)
        }
		// Fallback error message if no specific error field is populated
		return &orderResp, fmt.Errorf("coincheck API returned success=false for new order, status: %d, ID: %d", resp.StatusCode, orderResp.ID)
	}


	return &orderResp, nil
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
