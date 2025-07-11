package cvd

import "time"

// Trade represents a single trade event.
type Trade struct {
	Timestamp time.Time
	Price     float64
	Size      float64
	Side      string // "buy" or "sell"
}

// RingBuffer holds trades in a circular buffer.
type RingBuffer struct {
	trades []Trade
	size   int
	head   int // Points to the next available slot for writing
	count  int // Number of elements currently in the buffer
}

// NewRingBuffer creates a new RingBuffer with the given size.
func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		// Or return an error, depending on desired behavior
		panic("ring buffer size must be positive")
	}
	return &RingBuffer{
		trades: make([]Trade, size),
		size:   size,
		head:   0,
		count:  0,
	}
}

// Add adds a trade to the RingBuffer.
// If the buffer is full, the oldest trade is overwritten.
func (rb *RingBuffer) Add(trade Trade) {
	rb.trades[rb.head] = trade
	rb.head = (rb.head + 1) % rb.size
	if rb.count < rb.size {
		rb.count++
	}
}

// GetTrades returns all trades currently in the RingBuffer.
// The order of trades is not guaranteed to be chronological if the buffer has wrapped.
// If chronological order is needed, it should be handled by the caller or by modifying this method.
func (rb *RingBuffer) GetTrades() []Trade {
	if rb.count == 0 {
		return []Trade{}
	}

	// If the buffer hasn't wrapped around yet, or is full and head is at 0
	if rb.count < rb.size || (rb.count == rb.size && rb.head == 0) {
		// chronological order: from index 0 to count-1
		// but internal storage might be wrapped if head is not 0 and count == size
		if rb.count == rb.size && rb.head == 0 { // full and wrapped, head is at the start
			return append([]Trade(nil), rb.trades...)
		}
		// not full yet, or full but head is not 0 (meaning oldest is at head)
		// This part needs careful handling for chronological order when buffer is full.

		// Simpler: return a copy of the populated part.
		// For chronological order when full:
		if rb.count == rb.size { // Buffer is full
			// Trades are from rb.head (oldest) to rb.head-1 (newest, wrapped)
			// Example: size=5, head=2. Order: trades[2], trades[3], trades[4], trades[0], trades[1]
			res := make([]Trade, rb.size)
			copy(res, rb.trades[rb.head:])
			copy(res[rb.size-rb.head:], rb.trades[:rb.head])
			return res
		}
		// Buffer is not full, trades are from 0 to rb.head-1
		return append([]Trade(nil), rb.trades[:rb.head]...)
	}

	// Should not happen with current Add logic, but as a fallback
	return append([]Trade(nil), rb.trades[:rb.count]...)
}

// GetChronologicalTrades returns trades in the order they were added.
func (rb *RingBuffer) GetChronologicalTrades() []Trade {
	if rb.count == 0 {
		return []Trade{}
	}

	result := make([]Trade, rb.count)
	if rb.count < rb.size { // Buffer not yet full
		// Elements are from index 0 to head-1
		copy(result, rb.trades[:rb.head])
	} else { // Buffer is full
		// Oldest element is at rb.head, newest is at (rb.head - 1 + rb.size) % rb.size
		// Copy elements from rb.head to end of slice
		copied := copy(result, rb.trades[rb.head:])
		// Copy elements from start of slice to rb.head
		copy(result[copied:], rb.trades[:rb.head])
	}
	return result
}
