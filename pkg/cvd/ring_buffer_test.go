package cvd

import (
	"reflect"
	"testing"
	"time"
)

func TestNewRingBuffer(t *testing.T) {
	size := 10
	rb := NewRingBuffer(size)
	if rb == nil {
		t.Fatal("NewRingBuffer returned nil")
	}
	if rb.size != size {
		t.Errorf("expected size %d, got %d", size, rb.size)
	}
	if rb.head != 0 {
		t.Errorf("expected head to be 0, got %d", rb.head)
	}
	if rb.count != 0 {
		t.Errorf("expected count to be 0, got %d", rb.count)
	}
	if len(rb.trades) != size {
		t.Errorf("expected trades slice length %d, got %d", size, len(rb.trades))
	}

	// Test with invalid size
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("NewRingBuffer did not panic with non-positive size")
		}
	}()
	NewRingBuffer(0)
}

func TestRingBuffer_Add(t *testing.T) {
	rb := NewRingBuffer(3)
	trade1 := Trade{Timestamp: time.Now(), Price: 100, Size: 1, Side: "buy"}
	trade2 := Trade{Timestamp: time.Now().Add(1 * time.Second), Price: 101, Size: 2, Side: "sell"}
	trade3 := Trade{Timestamp: time.Now().Add(2 * time.Second), Price: 102, Size: 3, Side: "buy"}
	trade4 := Trade{Timestamp: time.Now().Add(3 * time.Second), Price: 103, Size: 4, Side: "sell"} // This will overwrite trade1

	rb.Add(trade1)
	if rb.count != 1 {
		t.Errorf("expected count 1, got %d", rb.count)
	}
	if rb.head != 1 {
		t.Errorf("expected head 1, got %d", rb.head)
	}

	rb.Add(trade2)
	if rb.count != 2 {
		t.Errorf("expected count 2, got %d", rb.count)
	}
	if rb.head != 2 {
		t.Errorf("expected head 2, got %d", rb.head)
	}

	rb.Add(trade3)
	if rb.count != 3 {
		t.Errorf("expected count 3, got %d", rb.count)
	}
	if rb.head != 0 { // Buffer full, head wrapped around
		t.Errorf("expected head 0, got %d", rb.head)
	}

	rb.Add(trade4)     // Overwrite trade1
	if rb.count != 3 { // Count should remain at max size
		t.Errorf("expected count 3, got %d", rb.count)
	}
	if rb.head != 1 { // Head moves to the next slot
		t.Errorf("expected head 1, got %d", rb.head)
	}

	// Verify that trade1 is overwritten by trade4
	// The oldest trade should now be trade2, then trade3, then trade4 (newest)
	// Internal order after adding trade4 (head is 1): trade4, trade2, trade3
	// Chronological: trade2, trade3, trade4
	expectedTrades := []Trade{trade2, trade3, trade4}
	actualTrades := rb.GetChronologicalTrades()

	if !reflect.DeepEqual(actualTrades, expectedTrades) {
		t.Errorf("GetChronologicalTrades after overwrite: expected %+v, got %+v", expectedTrades, actualTrades)
	}
}

func TestRingBuffer_GetTrades_Empty(t *testing.T) {
	rb := NewRingBuffer(5)
	trades := rb.GetTrades()
	if len(trades) != 0 {
		t.Errorf("expected 0 trades from empty buffer, got %d", len(trades))
	}
	chronoTrades := rb.GetChronologicalTrades()
	if len(chronoTrades) != 0 {
		t.Errorf("expected 0 chronological trades from empty buffer, got %d", len(chronoTrades))
	}
}

func TestRingBuffer_GetChronologicalTrades_NotFull(t *testing.T) {
	rb := NewRingBuffer(5)
	trade1 := Trade{Timestamp: time.Now(), Price: 100, Size: 1, Side: "buy"}
	trade2 := Trade{Timestamp: time.Now().Add(1 * time.Second), Price: 101, Size: 2, Side: "sell"}

	rb.Add(trade1)
	rb.Add(trade2)

	expected := []Trade{trade1, trade2}
	actual := rb.GetChronologicalTrades()

	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %+v, got %+v", expected, actual)
	}
}

func TestRingBuffer_GetChronologicalTrades_Full(t *testing.T) {
	rb := NewRingBuffer(3)
	trade1 := Trade{Timestamp: time.Now(), Price: 100, Size: 1, Side: "buy"}
	trade2 := Trade{Timestamp: time.Now().Add(1 * time.Second), Price: 101, Size: 2, Side: "sell"}
	trade3 := Trade{Timestamp: time.Now().Add(2 * time.Second), Price: 102, Size: 3, Side: "buy"}

	rb.Add(trade1)
	rb.Add(trade2)
	rb.Add(trade3)

	expected := []Trade{trade1, trade2, trade3}
	actual := rb.GetChronologicalTrades()

	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %+v, got %+v", expected, actual)
	}
}

func TestRingBuffer_GetChronologicalTrades_FullAndWrapped(t *testing.T) {
	rb := NewRingBuffer(3)
	now := time.Now()
	trade1 := Trade{Timestamp: now, Price: 100, Size: 1, Side: "buy"}
	trade2 := Trade{Timestamp: now.Add(1 * time.Second), Price: 101, Size: 2, Side: "sell"}
	trade3 := Trade{Timestamp: now.Add(2 * time.Second), Price: 102, Size: 3, Side: "buy"}
	trade4 := Trade{Timestamp: now.Add(3 * time.Second), Price: 103, Size: 4, Side: "sell"} // Overwrites trade1
	trade5 := Trade{Timestamp: now.Add(4 * time.Second), Price: 104, Size: 5, Side: "buy"}  // Overwrites trade2

	rb.Add(trade1) // head=1, count=1, trades=[t1,_,_]
	rb.Add(trade2) // head=2, count=2, trades=[t1,t2,_]
	rb.Add(trade3) // head=0, count=3, trades=[t1,t2,t3] (full) chronological: t1,t2,t3
	rb.Add(trade4) // head=1, count=3, trades=[t4,t2,t3] (t1 overwritten) chronological: t2,t3,t4
	rb.Add(trade5) // head=2, count=3, trades=[t4,t5,t3] (t2 overwritten) chronological: t3,t4,t5

	expected := []Trade{trade3, trade4, trade5}
	actual := rb.GetChronologicalTrades()

	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("expected %+v, got %+v", expected, actual)
		t.Logf("rb.trades: %+v", rb.trades)
		t.Logf("rb.head: %d, rb.count: %d", rb.head, rb.count)
	}

	// Add one more to ensure head wraps correctly
	trade6 := Trade{Timestamp: now.Add(5 * time.Second), Price: 105, Size: 6, Side: "sell"} // Overwrites trade3
	rb.Add(trade6)                                                                          // head=0, count=3, trades=[t4,t5,t6] (t3 overwritten) chronological: t4,t5,t6

	expectedAfterTrade6 := []Trade{trade4, trade5, trade6}
	actualAfterTrade6 := rb.GetChronologicalTrades()

	if !reflect.DeepEqual(actualAfterTrade6, expectedAfterTrade6) {
		t.Errorf("After trade6: expected %+v, got %+v", expectedAfterTrade6, actualAfterTrade6)
		t.Logf("rb.trades: %+v", rb.trades)
		t.Logf("rb.head: %d, rb.count: %d", rb.head, rb.count)
	}
}

// Test GetTrades for basic functionality, acknowledging its non-chronological guarantee when wrapped.
// For most use cases, GetChronologicalTrades is preferred.
func TestRingBuffer_GetTrades_Simple(t *testing.T) {
	rb := NewRingBuffer(3)
	trade1 := Trade{Timestamp: time.Now(), Price: 100, Size: 1, Side: "buy"}
	rb.Add(trade1)
	trades := rb.GetTrades()
	if len(trades) != 1 || !reflect.DeepEqual(trades[0], trade1) {
		t.Errorf("GetTrades with one item failed: got %+v", trades)
	}

	// Fill the buffer
	trade2 := Trade{Timestamp: time.Now().Add(1 * time.Second), Price: 101, Size: 2, Side: "sell"}
	trade3 := Trade{Timestamp: time.Now().Add(2 * time.Second), Price: 102, Size: 3, Side: "buy"}
	rb.Add(trade2)
	rb.Add(trade3) // Buffer is now [trade1, trade2, trade3], head is 0

	// Content of GetTrades() when full and head is 0 should be a direct copy
	tradesFull := rb.GetTrades()
	if len(tradesFull) != 3 {
		t.Fatalf("GetTrades expected 3 trades, got %d", len(tradesFull))
	}
	// Order will be t1,t2,t3
	if !reflect.DeepEqual(tradesFull[0], trade1) || !reflect.DeepEqual(tradesFull[1], trade2) || !reflect.DeepEqual(tradesFull[2], trade3) {
		t.Errorf("GetTrades content mismatch when full (head 0): got %+v", tradesFull)
	}

	// Overwrite one element
	trade4 := Trade{Timestamp: time.Now().Add(3 * time.Second), Price: 103, Size: 4, Side: "sell"} // overwrites trade1
	rb.Add(trade4)                                                                                 // Buffer is now [trade4, trade2, trade3], head is 1

	// Content of GetTrades() when full and head is 1
	// Expected: trade4, trade2, trade3 (internal order)
	// GetTrades is documented to return trades from head to head-1 (wrapped)
	// So it should be [trade2, trade3, trade4]
	tradesOverwritten := rb.GetTrades()
	if len(tradesOverwritten) != 3 {
		t.Fatalf("GetTrades expected 3 trades after overwrite, got %d", len(tradesOverwritten))
	}
	// Expected (as per GetTrades implementation for full buffer): trades[rb.head:], trades[:rb.head]
	// rb.head = 1. So, trades[1:], trades[:1] => [trade2, trade3], [trade4] => trade2, trade3, trade4
	expectedOverwritten := []Trade{trade2, trade3, trade4}
	if !reflect.DeepEqual(tradesOverwritten, expectedOverwritten) {
		t.Errorf("GetTrades content mismatch after overwrite (head 1): expected %+v, got %+v", expectedOverwritten, tradesOverwritten)
		t.Logf("Internal buffer: %+v", rb.trades) // Should be [trade4, trade2, trade3]
	}
}
