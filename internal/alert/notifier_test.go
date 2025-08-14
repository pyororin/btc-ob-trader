//go:build unit
// +build unit

package alert

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNoOpNotifier_Unit(t *testing.T) {
	t.Run("Send returns nil", func(t *testing.T) {
		notifier := NewNoOpNotifier()
		err := notifier.Send("test message")
		assert.NoError(t, err, "Send should always return nil for NoOpNotifier")
	})

	t.Run("Close returns nil", func(t *testing.T) {
		notifier := NewNoOpNotifier()
		err := notifier.Close()
		assert.NoError(t, err, "Close should always return nil for NoOpNotifier")
	})
}
