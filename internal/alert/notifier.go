// Package alert handles sending notifications.
// Currently, it only provides a no-op implementation.
package alert

// Notifier is the interface for sending alert messages.
type Notifier interface {
	Send(message string) error
	Close() error
}

// NoOpNotifier is a notifier that does nothing. It is used when alerting is disabled.
type NoOpNotifier struct{}

// NewNoOpNotifier creates a new NoOpNotifier.
func NewNoOpNotifier() *NoOpNotifier {
	return &NoOpNotifier{}
}

// Send does nothing and returns nil. It's a no-op implementation.
func (n *NoOpNotifier) Send(message string) error {
	// This is a no-op, so we don't send any alert and just return nil.
	return nil
}

// Close does nothing and returns nil.
func (n *NoOpNotifier) Close() error {
	return nil
}
