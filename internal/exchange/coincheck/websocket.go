// Package coincheck handles interactions with the Coincheck exchange.
package coincheck

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/websocket"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

const (
	coincheckWebSocketURL  = "wss://ws-api.coincheck.com/"
	orderbookChannelSuffix = "-orderbook"
	tradesChannelSuffix    = "-trades"
)

// OrderBookHandler is a function type that handles order book updates.
type OrderBookHandler func(data OrderBookData)

// TradeHandler is a function type that handles trade updates.
type TradeHandler func(data TradeData)

// WebSocketClient represents a Coincheck WebSocket client.
type WebSocketClient struct {
	conn             *websocket.Conn
	orderBookHandler OrderBookHandler
	tradeHandler     TradeHandler
	interrupt        chan os.Signal
	done             chan struct{}
	ready            chan bool
}

// NewWebSocketClient creates a new WebSocketClient.
func NewWebSocketClient(obHandler OrderBookHandler, tHandler TradeHandler) *WebSocketClient {
	return &WebSocketClient{
		orderBookHandler: obHandler,
		tradeHandler:     tHandler,
		ready:            make(chan bool),
	}
}

// Connect establishes a WebSocket connection and handles message receiving and pinging.
func (c *WebSocketClient) Connect(ctx context.Context) error {
	targetPair := "btc_jpy"

	c.interrupt = make(chan os.Signal, 1)
	signal.Notify(c.interrupt, os.Interrupt)

	u, err := url.Parse(coincheckWebSocketURL)
	if err != nil {
		logger.Fatalf("Error parsing WebSocket URL: %v", err)
	}
	logger.Infof("Attempting to connect to %s", u.String())

	var conn *websocket.Conn
	var dialErr error
	maxRetries := 10
	retryCount := 0
	backoff := 1 * time.Second

	for retryCount < maxRetries {
		conn, _, dialErr = websocket.DefaultDialer.Dial(u.String(), nil)
		if dialErr == nil {
			break
		}
		logger.Warnf("Dial error (attempt %d/%d): %v. Retrying in %v...", retryCount+1, maxRetries, dialErr, backoff)
		time.Sleep(backoff)
		backoff = time.Duration(float64(backoff) * 1.5)
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
		retryCount++
	}
	if dialErr != nil {
		logger.Warnf("Failed to connect after %d attempts: %v", maxRetries, dialErr)
		return dialErr
	}

	c.conn = conn
	logger.Infof("Successfully connected to %s", u.String())

	c.done = make(chan struct{})
	reconnect := make(chan bool, 1)

	go func() {
		defer close(c.done)
		for {
			_, message, err := c.conn.ReadMessage()
			if err != nil {
				logger.Warnf("Read error: %v", err)
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNormalClosure) {
					logger.Warn("Unexpected close error, attempting to reconnect...")
					reconnect <- true
					return
				}

				// Check for specific close error types
				opError, isOpError := err.(*net.OpError)
				if err == websocket.ErrCloseSent || (isOpError && opError.Err.Error() == "use of closed network connection") {
					logger.Info("Connection closed gracefully or by network.")
				} else {
					logger.Warnf("Unhandled read error: %T %v", err, err)
				}
				return
			}
			logger.Debugf("Received raw message: %s", string(message)) // Raw message logging
			c.handleMessage(message, targetPair)
		}
	}()

	if err := c.subscribe(targetPair + orderbookChannelSuffix); err != nil {
		logger.Warnf("Failed to subscribe to orderbook %s: %v", targetPair, err)
		c.conn.Close()
		return err
	}
	if err := c.subscribe(targetPair + tradesChannelSuffix); err != nil {
		logger.Warnf("Failed to subscribe to trades %s: %v", targetPair, err)
		c.conn.Close()
		return err
	}

	close(c.ready) // Signal that subscriptions are complete

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-c.done:
			logger.Info("Read goroutine finished.")
			select {
			case <-reconnect:
				logger.Info("Reconnect signal received. Attempting to reconnect...")
				c.conn.Close()
				return c.Connect(ctx)
			default:
				return nil // Graceful shutdown
			}
		case <-pingTicker.C:
			if err := c.conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				logger.Warnf("Ping error: %v", err)
				reconnect <- true
			}
		case <-c.interrupt:
			logger.Info("Interrupt signal received. Closing connection...")
			err := c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				logger.Warnf("Write close error: %v", err)
			}
			select {
			case <-c.done:
			case <-time.After(2 * time.Second):
				logger.Warn("Timeout waiting for server to close connection.")
			}
			return nil
		case <-ctx.Done():
			logger.Info("Context cancelled. Closing connection...")
			err := c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				logger.Warnf("Write close error: %v", err)
			}
			return nil
		}
	}
}

type subscriptionMessage struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
}

func (c *WebSocketClient) subscribe(channel string) error {
	if c.conn == nil {
		return errors.New("cannot subscribe: WebSocket connection is not established")
	}
	logger.Infof("Subscribing to channel: %s", channel)
	msg := subscriptionMessage{
		Type:    "subscribe",
		Channel: channel,
	}
	return c.conn.WriteJSON(msg)
}

func (c *WebSocketClient) Ready() <-chan bool {
	return c.ready
}

func (c *WebSocketClient) Close() error {
	if c.conn != nil {
		// Signal the Connect loop to terminate
		if c.interrupt != nil {
			c.interrupt <- os.Interrupt
		}
		return c.conn.Close()
	}
	return nil
}

// handleMessage decodes the incoming message and routes it to the appropriate handler.
func (c *WebSocketClient) handleMessage(message []byte, targetPair string) {
	// Try to parse as an order book message: ["btc_jpy", { ... }]
	var orderBookMsg []json.RawMessage
	if err := json.Unmarshal(message, &orderBookMsg); err == nil && len(orderBookMsg) == 2 {
		var channelName string
		if json.Unmarshal(orderBookMsg[0], &channelName) == nil && channelName == targetPair {
			var obData OrderBookData
			if json.Unmarshal(orderBookMsg[1], &obData) == nil {
				if c.orderBookHandler != nil {
					c.orderBookHandler(obData)
				}
				return
			}
		}
	}

	// Try to parse as a trades message, which can be a nested array.
	var multiTradeMsg [][]string
	if err := json.Unmarshal(message, &multiTradeMsg); err == nil {
		for _, tradeMsg := range multiTradeMsg {
			// Expected format: [timestamp, id, pair, rate, amount, side, buy_order_id, sell_order_id]
			if len(tradeMsg) == 8 && tradeMsg[2] == targetPair {
				tradeData := TradeData{
					ID:        tradeMsg[1], // Use the actual transaction ID
					PairStr:   tradeMsg[2],
					RateStr:   tradeMsg[3],
					AmountStr: tradeMsg[4],
					SideStr:   tradeMsg[5],
				}
				if c.tradeHandler != nil {
					c.tradeHandler(tradeData)
				}
			}
		}
		return
	}

	logger.Warnf("Unknown message format received: %s", string(message))
}
