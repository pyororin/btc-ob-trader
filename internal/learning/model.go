package learning

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Modelは学習済みモデルのインターフェースです。
type Model interface {
	// Trainは与えられた特徴量でモデルを訓練します。
	Train(ctx context.Context, features []*Feature) error
	// Predictは与えられた特徴量から予測値を返します。
	Predict(ctx context.Context, features []*Feature) (float64, error)
	// Versionはモデルのバージョンを返します。
	Version() string
}

// DummyLinearModel は線形回帰のダミーモデルです。
type DummyLinearModel struct {
	version string
	weights []float64
}

// NewDummyLinearModel は新しいDummyLinearModelを生成します。
func NewDummyLinearModel() *DummyLinearModel {
	return &DummyLinearModel{
		version: fmt.Sprintf("model-%s", uuid.New().String()),
		weights: []float64{0.5, 0.5}, // ダミーの重み
	}
}

// Train はモデルを訓練します（ダミー実装）。
func (m *DummyLinearModel) Train(ctx context.Context, features []*Feature) error {
	fmt.Printf("[%s] Training model with %d features...\n", time.Now().Format(time.RFC3339), len(features))
	// ダミー実装なので何もしない
	m.version = fmt.Sprintf("model-%s", uuid.New().String())
	fmt.Printf("[%s] Training complete. New version: %s\n", time.Now().Format(time.RFC3339), m.version)
	return nil
}

// Predict は予測値を返します（ダミー実装）。
func (m *DummyLinearModel) Predict(ctx context.Context, features []*Feature) (float64, error) {
	// ダミー実装: 最初の特徴量の値を返す
	if len(features) == 0 {
		return 0, fmt.Errorf("no features provided")
	}
	return features[0].Value * m.weights[0], nil
}

// Version はモデルのバージョンを返します。
func (m *DummyLinearModel) Version() string {
	return m.version
}
