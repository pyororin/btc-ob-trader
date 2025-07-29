package cvd

import "sync"

// RingBuffer is a circular buffer that is not safe for concurrent use.
type RingBuffer struct {
	buffer []Trade
	size   int
	head   int
	tail   int
	mu     sync.RWMutex
}

// NewRingBuffer creates a new RingBuffer.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		buffer: make([]Trade, size),
		size:   0,
		head:   0,
		tail:   0,
	}
}

// Add adds an item to the buffer. If the buffer is full, it overwrites the oldest item.
func (rb *RingBuffer) Add(item Trade) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.buffer[rb.tail] = item
	rb.tail = (rb.tail + 1) % len(rb.buffer)

	if rb.size < len(rb.buffer) {
		rb.size++
	} else {
		// Overwriting, so head needs to move as well
		rb.head = (rb.head + 1) % len(rb.buffer)
	}
}

// Do applies a function to all elements in the buffer in order.
func (rb *RingBuffer) Do(f func(interface{})) {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.size == 0 {
		return
	}

	current := rb.head
	for i := 0; i < rb.size; i++ {
		f(rb.buffer[current])
		current = (current + 1) % len(rb.buffer)
	}
}

// Size returns the number of elements in the buffer.
func (rb *RingBuffer) Size() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.size
}

// Capacity returns the capacity of the buffer.
func (rb *RingBuffer) Capacity() int {
	return len(rb.buffer)
}
