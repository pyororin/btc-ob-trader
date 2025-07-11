// Package engine contains the core trading logic components.
package engine

import (
	"fmt"
	"log" // Temporary logging, replace with proper logger from pkg/logger

	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
)

// ExecutionEngine handles order placement, cancellation, and replacement.
type ExecutionEngine struct {
	exchangeClient *coincheck.Client
	// Potentially add a logger here: logger *logger.Logger
}

// NewExecutionEngine creates a new ExecutionEngine.
func NewExecutionEngine(client *coincheck.Client) *ExecutionEngine {
	return &ExecutionEngine{
		exchangeClient: client,
	}
}

// PlaceOrder places a new order.
// If postOnly is true, it attempts to place a post-only order.
func (e *ExecutionEngine) PlaceOrder(pair string, orderType string, rate float64, amount float64, postOnly bool) (*coincheck.OrderResponse, error) {
	if e.exchangeClient == nil {
		return nil, fmt.Errorf("ExecutionEngine: exchange client is not initialized")
	}

	req := coincheck.OrderRequest{
		Pair:      pair,
		OrderType: orderType, // "buy" or "sell"
		Rate:      rate,
		Amount:    amount,
	}

	if postOnly {
		req.TimeInForce = "post_only"
	}

	// Temporary logging
	log.Printf("Placing order: %+v\n", req)

	resp, err := e.exchangeClient.NewOrder(req)
	if err != nil {
		// Temporary logging
		log.Printf("Error placing order: %v, Response: %+v\n", err, resp)
		if resp != nil && !resp.Success {
			// More detailed error if available from response
			return resp, fmt.Errorf("failed to place order: %s. API Error: %s %s", err.Error(), resp.Error, resp.ErrorDescription)
		}
		return nil, fmt.Errorf("failed to place order: %w", err)
	}

	// Temporary logging
	log.Printf("Order placed successfully: %+v\n", resp)
	return resp, nil
}

// CancelOrder cancels an existing order by its ID.
func (e *ExecutionEngine) CancelOrder(orderID int64) (*coincheck.CancelResponse, error) {
	if e.exchangeClient == nil {
		return nil, fmt.Errorf("ExecutionEngine: exchange client is not initialized")
	}

	// Temporary logging
	log.Printf("Cancelling order ID: %d\n", orderID)

	resp, err := e.exchangeClient.CancelOrder(orderID)
	if err != nil {
		// Temporary logging
		log.Printf("Error cancelling order: %v, Response: %+v\n", err, resp)
		if resp != nil && !resp.Success {
			return resp, fmt.Errorf("failed to cancel order: %s. API Error: %s", err.Error(), resp.Error)
		}
		return nil, fmt.Errorf("failed to cancel order %d: %w", orderID, err)
	}

	// Temporary logging
	log.Printf("Order cancelled successfully: %+v\n", resp)
	return resp, nil
}

// ReplaceOrder replaces an existing order by cancelling it and placing a new one.
// This is not an atomic operation on Coincheck; it's a two-step process.
func (e *ExecutionEngine) ReplaceOrder(orderIDToCancel int64, pair string, orderType string, newRate float64, newAmount float64, postOnly bool) (*coincheck.OrderResponse, error) {
	if e.exchangeClient == nil {
		return nil, fmt.Errorf("ExecutionEngine: exchange client is not initialized")
	}

	// Step 1: Cancel the existing order
	// Temporary logging
	log.Printf("Replacing order ID: %d. Attempting to cancel first.\n", orderIDToCancel)
	cancelResp, err := e.CancelOrder(orderIDToCancel)
	if err != nil {
		// Temporary logging
		log.Printf("Failed to cancel order %d during replace: %v, Response: %+v\n", orderIDToCancel, err, cancelResp)
		// If cancellation failed and it wasn't because the order was already filled/cancelled,
		// we might not want to proceed with placing a new order.
		// However, the task implies "cancel/replace", so we proceed if the error is not critical for replacement.
		// For example, if the order was already filled or doesn't exist, cancellation will fail.
		// We should check cancelResp.Error for specific Coincheck errors if necessary.
		// For now, let's assume that if err is not nil, we should return it.
		// This behavior might need adjustment based on specific error types from Coincheck.
		return nil, fmt.Errorf("failed to cancel order %d during replace operation: %w", orderIDToCancel, err)
	}
	if !cancelResp.Success {
		// Temporary logging
		log.Printf("Cancellation of order %d was not successful during replace: %s. Response: %+v\n", orderIDToCancel, cancelResp.Error, cancelResp)
		return nil, fmt.Errorf("cancellation of order %d not successful during replace: %s", orderIDToCancel, cancelResp.Error)
	}

	// Temporary logging
	log.Printf("Order ID %d cancelled successfully. Now placing new order.\n", orderIDToCancel)

	// Step 2: Place the new order
	newOrderResp, err := e.PlaceOrder(pair, orderType, newRate, newAmount, postOnly)
	if err != nil {
		// Temporary logging
		log.Printf("Failed to place new order during replace: %v, Response: %+v\n", err, newOrderResp)
		// If placing the new order fails, the original order is already cancelled.
		// This could leave the system in an unintended state (e.g., no order when one was expected).
		// The caller needs to handle this.
		return newOrderResp, fmt.Errorf("new order placement failed during replace operation (original order %d cancelled): %w", orderIDToCancel, err)
	}

	// Temporary logging
	log.Printf("New order placed successfully during replace: %+v\n", newOrderResp)
	return newOrderResp, nil
}
