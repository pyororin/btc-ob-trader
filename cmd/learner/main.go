package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/learning"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

func main() {
	var configPath string
	var tradeConfigPath string
	flag.StringVar(&configPath, "config", "config/app_config.yaml", "path to config file")
	flag.StringVar(&tradeConfigPath, "trade-config", "config/trade_config.yaml", "path to trade config file")
	flag.Parse()

	// 設定とロガーを初期化
	cfg, err := config.LoadConfig(configPath, tradeConfigPath)
	if err != nil {
		panic(fmt.Sprintf("failed to load config: %v", err))
	}
	logger.SetGlobalLogLevel(cfg.App.LogLevel)
	logger.Info("Logger initialized")

	// 学習パイプラインのコンポーネントを初期化
	// 本来はgRPCやKafkaなど、botサービスと連携する仕組みが必要
	stream := learning.NewInMemoryFeatureStream(1024)
	model := learning.NewDummyLinearModel()

	// 10分間隔でモデルを更新
	pipeline := learning.NewPipeline(stream, model, 10*time.Minute)

	// アプリケーションのコンテキストとシグナルハンドリング
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	// 学習パイプラインを起動
	go pipeline.Start(ctx)
	defer pipeline.Stop()

	// ダミーの特徴量を定期的に生成してストリームに流す（テスト用）
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		gen := learning.NewDummyFeatureGenerator("dummy_obi")
		for {
			select {
			case <-ticker.C:
				feature, _ := gen.Generate()
				logger.Infof("Generated dummy feature: %v", feature)
				if err := stream.Publish(ctx, feature); err != nil {
					logger.Warnf("Failed to publish feature: %v", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	logger.Info("Online learner service started.")
	<-c // シグナルを待つ
	logger.Info("Shutting down online learner service...")
}
