package cvd

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
// The current implementation for a full buffer returns trades in physical order starting from head.
func (rb *RingBuffer) GetTrades() []Trade {
	if rb.count == 0 {
		return []Trade{}
	}

	result := make([]Trade, rb.count)
	if rb.count < rb.size {
		// Buffer is not full yet. Trades are from index 0 to head-1.
		// Since head points to the next slot, head == count here.
		copy(result, rb.trades[:rb.count])
	} else { // Buffer is full (rb.count == rb.size)
		// Trades are returned in physical order starting from head (oldest physical slot
		// that was most recently updated or will be updated next if we consider head as write pointer).
		// Test expectation is: elements from rb.head to end, then from 0 to rb.head-1.
		copied := copy(result, rb.trades[rb.head:])
		copy(result[copied:], rb.trades[:rb.head])
	}
	return result
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
