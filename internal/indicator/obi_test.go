package indicator_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/indicator"
)

// newOrderBookData is a helper function from orderbook_test.go.
// To avoid import cycles or making it public, we redefine it here for testing purposes.
// In a real-world scenario, this might be part of a shared test utility package.
func newOrderBookData(bids, asks [][]string, lastUpdateAt string) coincheck.OrderBookData {
	return coincheck.OrderBookData{
		Bids:         bids,
		Asks:         asks,
		LastUpdateAt: lastUpdateAt,
	}
}

func TestOBICalculator(t *testing.T) {
	// 1. Setup
	ob := indicator.NewOrderBook()
	calcInterval := 50 * time.Millisecond // Use a short interval for testing
	calc := indicator.NewOBICalculator(ob, calcInterval)

	// 2. Start the calculator
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calc.Start(ctx)

	// Subscribe to the output channel
	resultsCh := calc.Subscribe()

	// 3. Test case: Initial state (empty book)
	// Apply an initial snapshot to the order book.
	// The calculator should start producing results after this.
	initialSnapshot := newOrderBookData(
		[][]string{{"100", "10"}},
		[][]string{{"101", "5"}},
		"1678886400",
	)
	ob.ApplySnapshot(initialSnapshot)

	// Wait for the first calculation
	var firstResult indicator.OBIResult
	select {
	case firstResult = <-resultsCh:
		// good
	case <-time.After(2 * calcInterval):
		t.Fatal("timed out waiting for the first OBI result")
	}

	expectedFirstResult := indicator.OBIResult{
		OBI8:      (10.0 - 5.0) / (10.0 + 5.0),
		OBI16:     (10.0 - 5.0) / (10.0 + 5.0),
		BestBid:   100,
		BestAsk:   101,
		Timestamp: time.Unix(1678886400, 0),
	}

	if !cmp.Equal(expectedFirstResult, firstResult, cmpopts.EquateApprox(0.000001, 0)) {
		t.Errorf("first OBI result mismatch:\n%s", cmp.Diff(expectedFirstResult, firstResult))
	}

	// 4. Test case: Update the book and check for new result
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		// Wait for the next result, which should reflect the updated book
		updatedSnapshot := newOrderBookData(
			[][]string{{"200", "20"}},
			[][]string{{"201", "15"}},
			"1678886401",
		)
		ob.ApplyUpdate(updatedSnapshot) // ApplyUpdate is same as ApplySnapshot

		// Keep draining the channel until we get the updated result
		timeout := time.After(3 * calcInterval)
		for {
			select {
			case nextResult := <-resultsCh:
				if nextResult.Timestamp.Unix() == 1678886401 {
					expectedNextResult := indicator.OBIResult{
						OBI8:      (20.0 - 15.0) / (20.0 + 15.0),
						OBI16:     (20.0 - 15.0) / (20.0 + 15.0),
						BestBid:   200,
						BestAsk:   201,
						Timestamp: time.Unix(1678886401, 0),
					}
					if !cmp.Equal(expectedNextResult, nextResult, cmpopts.EquateApprox(0.000001, 0)) {
						t.Errorf("next OBI result mismatch:\n%s", cmp.Diff(expectedNextResult, nextResult))
					}
					return // Success
				}
			case <-timeout:
				t.Error("timed out waiting for the updated OBI result")
				return
			}
		}
	}()

	wg.Wait()

	// 5. Test case: Stop the calculator
	cancel() // Use context cancellation to stop

	// Allow a moment for the goroutine to exit
	time.Sleep(2 * calcInterval)

	// Try to receive from the channel again. It should not produce new results.
	// The channel is not closed by design, so we check for a timeout.
	ob.ApplySnapshot(newOrderBookData([][]string{{"300", "30"}}, [][]string{}, "1678886402"))
	select {
	case unexpectedResult := <-resultsCh:
		// It's possible one last result was sent before the stop took effect.
		// We check if it's the one from *before* the last update.
		if unexpectedResult.Timestamp.Unix() >= 1678886402 {
			t.Errorf("received unexpected OBI result after stop: %+v", unexpectedResult)
		}
	case <-time.After(2 * calcInterval):
		// This is the expected outcome: no new results are sent.
	}
}

func TestOBICalculator_Stop(t *testing.T) {
	ob := indicator.NewOrderBook()
	calc := indicator.NewOBICalculator(ob, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	calc.Start(ctx)

	// immediately stop
	cancel()

	// check if the calculator is stopped
	// this is tricky to test directly without exposing internal state.
	// We can check that the output channel doesn't produce anything.
	ob.ApplySnapshot(newOrderBookData([][]string{{"100", "10"}}, nil, "1"))

	select {
	case res := <-calc.Subscribe():
		t.Errorf("received result after stopping: %+v", res)
	case <-time.After(100 * time.Millisecond):
		// success
	}
}
