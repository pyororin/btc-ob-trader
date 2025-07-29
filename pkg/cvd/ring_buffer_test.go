package cvd

import (
	"testing"
)

func TestRingBuffer_AddAndSize(t *testing.T) {
	rb := NewRingBuffer(3)

	if rb.Size() != 0 {
		t.Errorf("expected initial size 0, got %d", rb.Size())
	}

	rb.Add(Trade{ID: "1"})
	if rb.Size() != 1 {
		t.Errorf("expected size 1, got %d", rb.Size())
	}

	rb.Add(Trade{ID: "2"})
	rb.Add(Trade{ID: "3"})
	if rb.Size() != 3 {
		t.Errorf("expected size 3, got %d", rb.Size())
	}

	// Test overflow
	rb.Add(Trade{ID: "4"})
	if rb.Size() != 3 {
		t.Errorf("expected size to remain 3 after overflow, got %d", rb.Size())
	}
}

func TestRingBuffer_Capacity(t *testing.T) {
	rb := NewRingBuffer(5)
	if rb.Capacity() != 5 {
		t.Errorf("expected capacity 5, got %d", rb.Capacity())
	}
}

func TestRingBuffer_Do(t *testing.T) {
	rb := NewRingBuffer(3)
	trade1 := Trade{ID: "1", Price: 100}
	trade2 := Trade{ID: "2", Price: 200}
	trade3 := Trade{ID: "3", Price: 300}

	rb.Add(trade1)
	rb.Add(trade2)
	rb.Add(trade3)

	var results []Trade
	rb.Do(func(item interface{}) {
		if trade, ok := item.(Trade); ok {
			results = append(results, trade)
		}
	})

	if len(results) != 3 {
		t.Fatalf("expected 3 items from Do, got %d", len(results))
	}
	if results[0].ID != "1" || results[1].ID != "2" || results[2].ID != "3" {
		t.Errorf("Do returned items in wrong order: %+v", results)
	}

	// Test overflow and order
	trade4 := Trade{ID: "4", Price: 400}
	rb.Add(trade4)

	results = nil
	rb.Do(func(item interface{}) {
		if trade, ok := item.(Trade); ok {
			results = append(results, trade)
		}
	})

	if len(results) != 3 {
		t.Fatalf("expected 3 items after overflow, got %d", len(results))
	}
	// After trade4 is added, trade1 is overwritten. The order should be 2, 3, 4.
	if results[0].ID != "2" || results[1].ID != "3" || results[2].ID != "4" {
		t.Errorf("Do returned items in wrong order after overflow: %+v", results)
	}
}

func TestRingBuffer_DoEmpty(t *testing.T) {
	rb := NewRingBuffer(5)
	called := false
	rb.Do(func(item interface{}) {
		called = true
	})
	if called {
		t.Error("Do callback was called on an empty buffer")
	}
}
