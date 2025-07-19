package learning

import "context"

// FeatureStreamは特徴量のストリームを扱うインターフェースです。
type FeatureStream interface {
	// Publishは特徴量をストリームに発行します。
	Publish(ctx context.Context, feature *Feature) error
	// Subscribeはストリームから特徴量を受け取るためのチャネルを返します。
	Subscribe(ctx context.Context) (<-chan *Feature, error)
}
