package learning

import (
	"context"
	"sync"
)

// InMemoryFeatureStream はインメモリで特徴量ストリームを実現します。
// goroutine-safeです。
type InMemoryFeatureStream struct {
	mu      sync.RWMutex
	subs    map[chan *Feature]struct{}
	bufSize int
}

// NewInMemoryFeatureStream は新しいInMemoryFeatureStreamを生成します。
func NewInMemoryFeatureStream(bufferSize int) *InMemoryFeatureStream {
	return &InMemoryFeatureStream{
		subs:    make(map[chan *Feature]struct{}),
		bufSize: bufferSize,
	}
}

// Publishは特徴量をストリームに発行します。
// 登録されている全てのsubscriberに特徴量を送信します。
func (s *InMemoryFeatureStream) Publish(ctx context.Context, feature *Feature) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for sub := range s.subs {
		select {
		case sub <- feature:
		case <-ctx.Done():
			return ctx.Err()
		default:
			// subscriberが詰まっている場合はブロックしない
		}
	}
	return nil
}

// Subscribeはストリームから特徴量を受け取るためのチャネルを返します。
// subscriberは不要になったらチャネルを閉じてunsubscribeする必要があります。
func (s *InMemoryFeatureStream) Subscribe(ctx context.Context) (<-chan *Feature, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan *Feature, s.bufSize)
	s.subs[ch] = struct{}{}

	// contextがキャンセルされたらunsubscribeする
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		defer s.mu.Unlock()
		close(ch)
		delete(s.subs, ch)
	}()

	return ch, nil
}
