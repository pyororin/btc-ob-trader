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
	Bids          [][]string `json:"bids"`
	Asks          [][]string `json:"asks"`
	LastUpdateAt string     `json:"last_update_at"`
}

// OrderBookUpdate represents an update to the order book received via WebSocket.
// The first element is the pair (e.g., "btc_jpy").
// The second element is an object containing bids, asks, and last_update_at.
// We define it this way to make JSON unmarshaling easier.
type OrderBookUpdate []interface{}

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
