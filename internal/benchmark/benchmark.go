package benchmark

import (
	"context"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/dbwriter"
	"go.uber.org/zap"
)

// Service はベンチマークの計算と記録を担当します。
type Service struct {
	logger    *zap.Logger
	dbWriter  dbwriter.DBWriter
	startTime time.Time
}

// NewService は新しいベンチマークサービスを作成します。
func NewService(logger *zap.Logger, dbWriter dbwriter.DBWriter) *Service {
	return &Service{
		logger:    logger.Named("benchmark"),
		dbWriter:  dbWriter,
		startTime: time.Now(),
	}
}

// Tick は現在の価格を受け取り、ベンチマーク値をDBに保存します。
// ここでは単純なBuy-and-Holdを想定し、価格そのものを記録します。
func (s *Service) Tick(ctx context.Context, currentPrice float64) {
	if s.dbWriter == nil {
		s.logger.Warn("DB writer is not initialized, skipping benchmark save.")
		return
	}

	benchmarkValue := dbwriter.BenchmarkValue{
		Time:  time.Now(),
		Price: currentPrice,
	}

	// dbwriterに非同期で書き込みを依頼
	s.dbWriter.SaveBenchmarkValue(ctx, benchmarkValue)
}
