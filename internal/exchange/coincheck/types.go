// Package coincheck handles interactions with the Coincheck exchange.
package coincheck

import "strconv"

// BookLevel represents a single price level in the order book.
// Rate and Amount are strings as received from the WebSocket API.
type BookLevel struct {
	Rate   string
	Amount string
}

// RateFloat64 converts the Rate string to float64.
func (bl *BookLevel) RateFloat64() (float64, error) {
	return strconv.ParseFloat(bl.Rate, 64)
}

// AmountFloat64 converts the Amount string to float64.
func (bl *BookLevel) AmountFloat64() (float64, error) {
	return strconv.ParseFloat(bl.Amount, 64)
}

// OrderBookData contains the bids and asks arrays.
type OrderBookData struct {
	PairStr      string     `json:"pair"`
	Bids         [][]string `json:"bids"`
	Asks         [][]string `json:"asks"`
	LastUpdateAt string     `json:"last_update_at"`
}

// OrderBookUpdate represents an update to the order book received via WebSocket.
// The first element is the pair (e.g., "btc_jpy").
// The second element is an object containing bids, asks, and last_update_at.
// We define it this way to make JSON unmarshaling easier.
type OrderBookUpdate []interface{}

// OrderRequest defines the parameters for placing a new order.
type OrderRequest struct {
	Pair          string  `json:"pair"`
	OrderType     string  `json:"order_type"`
	Rate          float64 `json:"rate,omitempty"`         // omit if market order
	Amount        float64 `json:"amount,omitempty"`       // omit if market buy order
	MarketBuyAmount float64 `json:"market_buy_amount,omitempty"` // for market buy orders
	TimeInForce   string  `json:"time_in_force,omitempty"` // e.g., "post_only"
	// StopLossRate  float64 `json:"stop_loss_rate,omitempty"` // Not used in this task
}

// OrderResponse defines the structure for the response when a new order is created.
type OrderResponse struct {
	Success      bool    `json:"success"`
	ID           int64   `json:"id"`
	Rate         string  `json:"rate"` // Rate can be string (e.g., "30010.0") or null for market orders
	Amount       string  `json:"amount"`
	OrderType    string  `json:"order_type"`
	TimeInForce  string  `json:"time_in_force"`
	StopLossRate *string `json:"stop_loss_rate"` // Use pointer for nullable fields
	Pair         string  `json:"pair"`
	CreatedAt    string  `json:"created_at"`
	Error        string  `json:"error,omitempty"`       // For error responses
	ErrorDescription string `json:"error_description,omitempty"` // For error responses like {"error": "...", "error_description": "..."}
}


// CancelResponse defines the structure for the response when an order is cancelled.
type CancelResponse struct {
	Success bool  `json:"success"`
	ID      int64 `json:"id"`
	Error   string `json:"error,omitempty"` // For error responses
}


// Pair returns the trading pair string.
func (obu OrderBookUpdate) Pair() string {
	if len(obu) > 0 {
		if pair, ok := obu[0].(string); ok {
			return pair
		}
	}
	return ""
}

// Data returns the order book data.
func (obu OrderBookUpdate) Data() (OrderBookData, bool) {
	if len(obu) > 1 {
		if dataMap, ok := obu[1].(map[string]interface{}); ok {
			var data OrderBookData
			if bids, ok := dataMap["bids"].([]interface{}); ok {
				for _, b := range bids {
					if bidSlice, ok := b.([]interface{}); ok && len(bidSlice) == 2 {
						rate, rateOk := bidSlice[0].(string)
						amount, amountOk := bidSlice[1].(string)
						if rateOk && amountOk {
							data.Bids = append(data.Bids, []string{rate, amount})
						}
					}
				}
			}
			if asks, ok := dataMap["asks"].([]interface{}); ok {
				for _, a := range asks {
					if askSlice, ok := a.([]interface{}); ok && len(askSlice) == 2 {
						rate, rateOk := askSlice[0].(string)
						amount, amountOk := askSlice[1].(string)
						if rateOk && amountOk {
							data.Asks = append(data.Asks, []string{rate, amount})
						}
					}
				}
			}
			if lastUpdateAt, ok := dataMap["last_update_at"].(string); ok {
				data.LastUpdateAt = lastUpdateAt
			}
			return data, true
		}
	}
	return OrderBookData{}, false
}

// TradeData represents a single trade update from the WebSocket API.
// It's a JSON array: [transaction_id, pair, rate, amount, taker_side]
type TradeData struct {
	ID        string
	PairStr   string
	RateStr   string
	AmountStr string
	SideStr   string
}

// TransactionID returns the transaction ID from the trade data.
func (td TradeData) TransactionID() string {
	return td.ID
}

// Pair returns the trading pair from the trade data.
func (td TradeData) Pair() string {
	return td.PairStr
}

// Rate returns the rate from the trade data.
func (td TradeData) Rate() string {
	return td.RateStr
}

// Amount returns the amount from the trade data.
func (td TradeData) Amount() string {
	return td.AmountStr
}

// TakerSide returns the taker side ("buy" or "sell") from the trade data.
func (td TradeData) TakerSide() string {
	return td.SideStr
}
