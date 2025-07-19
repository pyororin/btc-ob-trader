package learning

import (
	"context"
	"fmt"
	"time"
)

// Pipelineはオンライン学習のパイプラインを管理します。
type Pipeline struct {
	featureStream FeatureStream
	model         Model
	updateTicker  *time.Ticker
	featureBuffer []*Feature
}

// NewPipelineは新しいPipelineを生成します。
func NewPipeline(stream FeatureStream, model Model, updateInterval time.Duration) *Pipeline {
	return &Pipeline{
		featureStream: stream,
		model:         model,
		updateTicker:  time.NewTicker(updateInterval),
		featureBuffer: make([]*Feature, 0, 1024),
	}
}

// Startは学習パイプラインを開始します。
// このメソッドはgoroutineとして実行されることを想定しています。
func (p *Pipeline) Start(ctx context.Context) {
	fmt.Println("Starting learning pipeline...")
	sub, err := p.featureStream.Subscribe(ctx)
	if err != nil {
		fmt.Printf("Failed to subscribe to feature stream: %v\n", err)
		return
	}

	for {
		select {
		case feature, ok := <-sub:
			if !ok {
				fmt.Println("Feature stream closed.")
				return
			}
			p.featureBuffer = append(p.featureBuffer, feature)
		case <-p.updateTicker.C:
			if len(p.featureBuffer) > 0 {
				fmt.Printf("Ticker triggered. Starting model training with %d features.\n", len(p.featureBuffer))
				err := p.model.Train(ctx, p.featureBuffer)
				if err != nil {
					fmt.Printf("Failed to train model: %v\n", err)
				}
				// 訓練に使ったバッファをクリア
				p.featureBuffer = p.featureBuffer[:0]
			} else {
				fmt.Println("Ticker triggered, but no new features to train on.")
			}
		case <-ctx.Done():
			fmt.Println("Stopping learning pipeline...")
			return
		}
	}
}

// Stopは学習パイプラインを停止します。
func (p *Pipeline) Stop() {
	p.updateTicker.Stop()
}
