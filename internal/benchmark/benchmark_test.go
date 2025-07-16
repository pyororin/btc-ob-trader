package benchmark

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestService_Tick_NoPanicWithNilWriter(t *testing.T) {
	logger := zap.NewNop()
	service := NewService(logger, nil) // DBライターがnil

	// Tickを呼び出してもpanicしないことを確認
	assert.NotPanics(t, func() {
		service.Tick(context.Background(), 123.45)
	})
}
