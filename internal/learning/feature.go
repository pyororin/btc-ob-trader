package learning

import (
	"math/rand"
	"time"
)

// Featureは学習に使われる特徴量を表します。
type Feature struct {
	Timestamp time.Time
	Name      string
	Value     float64
}

// FeatureGeneratorは特徴量を生成するインターフェースです。
type FeatureGenerator interface {
	Generate() (*Feature, error)
}

// DummyFeatureGenerator はダミーの特徴量ジェネレーターです。
type DummyFeatureGenerator struct {
	name string
}

// NewDummyFeatureGenerator は新しいDummyFeatureGeneratorを生成します。
func NewDummyFeatureGenerator(name string) *DummyFeatureGenerator {
	return &DummyFeatureGenerator{name: name}
}

// Generate はランダムな値を持つダミーの特徴量を生成します。
func (g *DummyFeatureGenerator) Generate() (*Feature, error) {
	return &Feature{
		Timestamp: time.Now(),
		Name:      g.name,
		Value:     rand.Float64(),
	}, nil
}
