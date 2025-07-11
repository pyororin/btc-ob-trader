// Package coincheck handles interactions with the Coincheck exchange.
package coincheck

import (
	"context" // Added for dbWriter operations
	"encoding/json"
	"errors"
	// "log" // Replaced by pkg/logger
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

const (
	coincheckWebSocketURL  = "wss://ws-api.coincheck.com/"
	orderbookChannelSuffix = "-orderbook" // Corrected suffix identifier
)

// WebSocketClient represents a Coincheck WebSocket client.
type WebSocketClient struct {
	conn     *websocket.Conn
	dbWriter *dbwriter.Writer // For writing order book updates
	// TODO: Add channels for sending/receiving messages and managing state
}

// NewWebSocketClient creates a new WebSocketClient.
func NewWebSocketClient(dbw *dbwriter.Writer) *WebSocketClient {
	return &WebSocketClient{dbWriter: dbw}
}

// Connect establishes a WebSocket connection and handles message receiving and pinging.
// It will attempt to reconnect with exponential backoff if the connection is lost.
func (c *WebSocketClient) Connect() error {
	// TODO: Make the pair configurable via WebSocketClient field or method argument
	targetPair := "btc_jpy" // Define targetPair at the beginning of the method

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	u, err := url.Parse(coincheckWebSocketURL)
	if err != nil {
		logger.Fatalf("Error parsing WebSocket URL: %v", err)
	}
	logger.Infof("Attempting to connect to %s", u.String())

	var conn *websocket.Conn
	var dialErr error
	maxRetries := 5 // Example: Max 5 retries for initial connection
	retryCount := 0
	backoff := 1 * time.Second

	for retryCount < maxRetries {
		conn, _, dialErr = websocket.DefaultDialer.Dial(u.String(), nil)
		if dialErr == nil {
			break // Connection successful
		}
		logger.Errorf("Dial error (attempt %d/%d): %v. Retrying in %v...", retryCount+1, maxRetries, dialErr, backoff) // Warnf -> Errorf
		time.Sleep(backoff)
		backoff *= 2 // Exponential backoff
		retryCount++
	}
	if dialErr != nil {
		logger.Errorf("Failed to connect after %d attempts: %v", maxRetries, dialErr)
		return dialErr
	}

	c.conn = conn
	logger.Infof("Successfully connected to %s", u.String())
	defer func() {
		logger.Info("Closing WebSocket connection.")
		c.conn.Close()
	}()

	done := make(chan struct{})  // Signals that the read goroutine has finished
	reconnect := make(chan bool) // Signals that a reconnect is needed

	go func() {
		defer close(done)
		for {
			messageType, message, err := c.conn.ReadMessage()
			if err != nil {
				logger.Errorf("Read error: %v", err)
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNormalClosure) {
					logger.Error("Unexpected close error, attempting to reconnect...") // Warn -> Error
					reconnect <- true
				} else if err == websocket.ErrCloseSent {
					logger.Info("Connection closed by client.")
				} else if opError, ok := err.(*net.OpError); ok && opError.Err.Error() == "use of closed network connection" {
					logger.Info("Connection closed, possibly by server or network issue.")
				} else {
					logger.Errorf("Unhandled read error: %T %v", err, err)
				}
				return // Exit goroutine on error
			}
			switch messageType {
			case websocket.TextMessage:
				var msgArray []json.RawMessage // Use json.RawMessage for deferred parsing
				if err := json.Unmarshal(message, &msgArray); err == nil && len(msgArray) == 2 {
					var channelName string
					if err := json.Unmarshal(msgArray[0], &channelName); err == nil {
						subscribedOrderbookChannel := targetPair + orderbookChannelSuffix
						// subscribedTradesChannel := targetPair + "-trades" // If trades are also handled

						if channelName == subscribedOrderbookChannel {
							var obData OrderBookData
							if err := json.Unmarshal(msgArray[1], &obData); err == nil {
								c.processAndSaveOrderBook(targetPair, obData) // Pass base pair name
							} else {
								logger.Errorf("Error unmarshalling OrderBookData for channel %s: %v. OrderBook JSON: %s", channelName, err, msgArray[1])
							}
						// } else if channelName == subscribedTradesChannel {
							// logger.Infof("Received trades message for %s (processing not implemented yet)", targetPair)
							// TODO: Implement trades processing if needed
						} else if channelName == "keepalive" { // This might not be a channel name but a message type/content
							logger.Info("Received keepalive message.") // Actual keepalive might be ping/pong or specific message
						} else {
							logger.Infof("Received message for unhandled or non-subscribed channel: %s, Data: %s", channelName, msgArray[1])
						}
					} else {
						logger.Errorf("Error unmarshalling channel name from WebSocket message: %v. Original message: %s", err, message)
					}
				} else if err != nil {
					logger.Errorf("Error unmarshalling WebSocket message into array: %v. Original message: %s", err, message)
				} else {
					// Generic text message that doesn't fit the 2-element array structure (or len != 2)
					logger.Infof("Received generic text message (not a 2-element JSON array as expected for channel messages): %s", message)
				}

			case websocket.BinaryMessage:
				logger.Infof("Received binary message: %s", message)
			case websocket.PongMessage:
				logger.Info("Pong received")
			default:
				logger.Errorf("Received unhandled message type: %d", messageType) // Warnf -> Errorf
			}
		}
	}()

	// Subscribe to orderbook and trades
	// Example: "btc_jpy-orderbook", "btc_jpy-trades"
	// TODO: Make the pair configurable
	targetPair := "btc_jpy"
	if err := c.subscribe(targetPair + orderbookChannelSuffix); err != nil {
		logger.Errorf("Failed to subscribe to orderbook %s: %v", targetPair, err)
	}
	// if err := c.subscribe(targetPair + "-trades"); err != nil { // If trades are also needed
	// 	logger.Errorf("Failed to subscribe to trades %s: %v", targetPair, err)
	// }

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-done:
			logger.Info("Read goroutine finished.")
			select {
			case <-reconnect:
				logger.Info("Reconnect signal received. Attempting to reconnect...")
				c.conn.Close()
				return c.Connect()
			case <-interrupt:
				logger.Info("Interrupt signal received while read goroutine was done.")
				return nil
			case <-time.After(5 * time.Second):
				logger.Error("Timeout waiting for reconnect signal after read error.") // Warn -> Error
				return errors.New("failed to maintain WebSocket connection after read error")
			}

		case <-pingTicker.C:
			logger.Infof("Sending Ping") // Debug -> Infof
			if err := c.conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				logger.Errorf("Ping error: %v", err)
				reconnect <- true
			}

		case <-interrupt:
			logger.Info("Interrupt signal received. Closing connection...")
			err := c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				logger.Errorf("Write close error: %v", err)
				return err
			}
			select {
			case <-done:
				logger.Info("Read goroutine finished after sending close message.")
			case <-time.After(2 * time.Second):
				logger.Error("Timeout waiting for server to close connection.") // Warn -> Error
			}
			return nil
		}
	}
}

// processAndSaveOrderBook converts Coincheck's order book data to dbwriter.OrderBookUpdate
// and saves it to the database.
func (c *WebSocketClient) processAndSaveOrderBook(pair string, data OrderBookData) {
	if c.dbWriter == nil {
		logger.Error("dbWriter is nil, cannot save order book update.") // Warn -> Error
		return
	}

	ctx := context.TODO() // Or a more appropriate context

	timestampMs, err := strconv.ParseInt(data.LastUpdateAt, 10, 64)
	if err != nil {
		logger.Errorf("Error parsing LastUpdateAt timestamp '%s': %v", data.LastUpdateAt, err)
		return
	}
	eventTime := time.Unix(timestampMs, 0) // Assuming LastUpdateAt is in seconds. If ms, use time.UnixMilli.
                                          // Coincheck doc example "1659321701" looks like seconds.

	// Process bids
	for _, bid := range data.Bids {
		if len(bid) == 2 {
			price, errPrice := strconv.ParseFloat(bid[0], 64)
			size, errSize := strconv.ParseFloat(bid[1], 64)
			if errPrice != nil || errSize != nil {
				logger.Errorf("Error parsing bid data for pair %s: price_str='%s', size_str='%s', errors: %v, %v", pair, bid[0], bid[1], errPrice, errSize)
				continue
			}
			obu := dbwriter.OrderBookUpdate{
				Time:       eventTime,
				Pair:       pair,
				Side:       "bid",
				Price:      price,
				Size:       size,
				IsSnapshot: false, // Assuming these are updates, not full snapshots unless specified by API
			}
			if err := c.dbWriter.SaveOrderBookUpdate(ctx, obu); err != nil {
				logger.Errorf("Failed to save order book bid update for pair %s: %v", pair, err)
				// Continue processing other updates
			}
		}
	}

	// Process asks
	for _, ask := range data.Asks {
		if len(ask) == 2 {
			price, errPrice := strconv.ParseFloat(ask[0], 64)
			size, errSize := strconv.ParseFloat(ask[1], 64)
			if errPrice != nil || errSize != nil {
				logger.Errorf("Error parsing ask data for pair %s: price_str='%s', size_str='%s', errors: %v, %v", pair, ask[0], ask[1], errPrice, errSize)
				continue
			}
			obu := dbwriter.OrderBookUpdate{
				Time:       eventTime,
				Pair:       pair,
				Side:       "ask",
				Price:      price,
				Size:       size,
				IsSnapshot: false, // Assuming these are updates
			}
			if err := c.dbWriter.SaveOrderBookUpdate(ctx, obu); err != nil {
				logger.Errorf("Failed to save order book ask update for pair %s: %v", pair, err)
			}
		}
	}
	// logger.Debugf("Processed and attempted to save order book for %s at %s", pair, eventTime)
}


// subscribe sends a subscription message to the WebSocket server.
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
	err := c.conn.WriteJSON(msg)
	if err != nil {
		logger.Errorf("Error subscribing to channel %s: %v", channel, err)
	}
	return err
}

// Close closes the WebSocket connection.
func (c *WebSocketClient) Close() error {
	if c.conn != nil {
		// Inform the read loop to terminate if it's blocked on ReadMessage
		// One way is to send a close message to self, but that's tricky.
		// Simply closing the connection should make ReadMessage return an error.
		return c.conn.Close()
	}
	return nil
}
