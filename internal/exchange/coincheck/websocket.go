// Package coincheck handles interactions with the Coincheck exchange.
package coincheck

import (
	"errors"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/websocket"
)

const (
	coincheckWebSocketURL = "wss://ws-api.coincheck.com/"
)

// WebSocketClient represents a Coincheck WebSocket client.
type WebSocketClient struct {
	conn *websocket.Conn
	// TODO: Add channels for sending/receiving messages and managing state
}

// NewWebSocketClient creates a new WebSocketClient.
func NewWebSocketClient() *WebSocketClient {
	return &WebSocketClient{}
}

// Connect establishes a WebSocket connection and handles message receiving and pinging.
// It will attempt to reconnect with exponential backoff if the connection is lost.
func (c *WebSocketClient) Connect() error {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	u, err := url.Parse(coincheckWebSocketURL)
	if err != nil {
		log.Fatalf("Error parsing WebSocket URL: %v", err) // Fatal, as URL should be static and correct
	}
	log.Printf("Attempting to connect to %s", u.String())

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
		log.Printf("Dial error (attempt %d/%d): %v. Retrying in %v...", retryCount+1, maxRetries, dialErr, backoff)
		time.Sleep(backoff)
		backoff *= 2 // Exponential backoff
		retryCount++
	}
	if dialErr != nil {
		log.Printf("Failed to connect after %d attempts: %v", maxRetries, dialErr)
		return dialErr
	}

	c.conn = conn
	log.Printf("Successfully connected to %s", u.String())
	defer func() {
		log.Println("Closing WebSocket connection.")
		c.conn.Close()
	}()

	done := make(chan struct{})  // Signals that the read goroutine has finished
	reconnect := make(chan bool) // Signals that a reconnect is needed

	go func() {
		defer close(done)
		for {
			messageType, message, err := c.conn.ReadMessage()
			if err != nil {
				log.Println("Read error:", err)
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNormalClosure) {
					log.Println("Unexpected close error, attempting to reconnect...")
					reconnect <- true
				} else if err == websocket.ErrCloseSent {
					log.Println("Connection closed by client.")
				} else if opError, ok := err.(*net.OpError); ok && opError.Err.Error() == "use of closed network connection" {
					log.Println("Connection closed, possibly by server or network issue.")
				} else {
					log.Printf("Unhandled read error: %T %v", err, err)
				}
				return // Exit goroutine on error
			}
			switch messageType {
			case websocket.TextMessage:
				log.Printf("recv: %s", message)
			case websocket.BinaryMessage:
				log.Printf("recv binary: %s", message) // Or handle as appropriate
			case websocket.PongMessage:
				log.Println("Pong received") // Good for confirming connection health
			default:
				log.Printf("Received unhandled message type: %d", messageType)
			}
		}
	}()

	// Subscribe to orderbook and trades
	if err := c.subscribe("btc_jpy-orderbook"); err != nil {
		log.Printf("Failed to subscribe to orderbook: %v", err)
		// Depending on requirements, might want to return err or retry
	}
	if err := c.subscribe("btc_jpy-trades"); err != nil {
		log.Printf("Failed to subscribe to trades: %v", err)
		// Depending on requirements, might want to return err or retry
	}

	pingTicker := time.NewTicker(30 * time.Second) // As per spec, ping/pong 30s
	defer pingTicker.Stop()

	for {
		select {
		case <-done: // Read goroutine exited, probably due to error
			log.Println("Read goroutine finished.")
			// Wait for reconnect signal or interrupt
			select {
			case <-reconnect:
				log.Println("Reconnect signal received. Attempting to reconnect...")
				c.conn.Close()     // Ensure old connection is closed
				return c.Connect() // Recursive call to reconnect with backoff
			case <-interrupt:
				log.Println("Interrupt signal received while read goroutine was done.")
				return nil
			case <-time.After(5 * time.Second): // Timeout if no reconnect signal
				log.Println("Timeout waiting for reconnect signal after read error.")
				return nil // Or some error indicating failure to maintain connection
			}

		case <-pingTicker.C:
			log.Println("Sending Ping")
			if err := c.conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				log.Println("Ping error:", err)
				// If ping fails, it might indicate a dead connection.
				// Trigger reconnect logic.
				reconnect <- true
				// No return here, wait for the done/reconnect select case above
			}

		case <-interrupt:
			log.Println("Interrupt signal received. Closing connection...")
			// Cleanly close the connection by sending a close message and then
			// waiting (with timeout) for the server to close the connection.
			err := c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("Write close error:", err)
				return err
			}
			select {
			case <-done: // Wait for read goroutine to finish after sending close
				log.Println("Read goroutine finished after sending close message.")
			case <-time.After(2 * time.Second): // Timeout for server to close
				log.Println("Timeout waiting for server to close connection.")
			}
			return nil
		}
	}
}

// subscribe sends a subscription message to the WebSocket server.
// The message format is based on Coincheck API documentation.
type subscriptionMessage struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
}

func (c *WebSocketClient) subscribe(channel string) error {
	if c.conn == nil {
		return errors.New("cannot subscribe: WebSocket connection is not established")
	}
	log.Printf("Subscribing to channel: %s", channel)
	msg := subscriptionMessage{
		Type:    "subscribe",
		Channel: channel,
	}
	err := c.conn.WriteJSON(msg)
	if err != nil {
		log.Printf("Error subscribing to channel %s: %v", channel, err)
	}
	return err
}

// Close closes the WebSocket connection.
// It's a good practice to provide a way to explicitly close the client.
func (c *WebSocketClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
