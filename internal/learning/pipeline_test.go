package learning

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockModel is a mock for the Model interface.
type MockModel struct {
	mock.Mock
}

func (m *MockModel) Train(ctx context.Context, features []*Feature) error {
	args := m.Called(ctx, features)
	return args.Error(0)
}

func (m *MockModel) Predict(ctx context.Context, features []*Feature) (float64, error) {
	args := m.Called(ctx, features)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockModel) Version() string {
	args := m.Called()
	return args.String(0)
}

func TestPipeline_Start(t *testing.T) {
	// --- Setup ---
	mockModel := new(MockModel)
	stream := NewInMemoryFeatureStream(10)
	// テストでは短い間隔で実行
	pipeline := NewPipeline(stream, mockModel, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)

	// --- Expectation ---
	// Trainが少なくとも1回呼ばれることを期待
	mockModel.On("Train", ctx, mock.AnythingOfType("[]*learning.Feature")).Return(nil).Once()

	// --- Action ---
	go func() {
		defer wg.Done()
		pipeline.Start(ctx)
	}()

	// パイプラインがSubscribeするのを少し待つ
	time.Sleep(50 * time.Millisecond)

	// パイプラインに特徴量をいくつか流し込む
	err := stream.Publish(ctx, &Feature{Name: "test_feature", Value: 0.1})
	assert.NoError(t, err)
	err = stream.Publish(ctx, &Feature{Name: "test_feature", Value: 0.2})
	assert.NoError(t, err)

	// Trainが呼ばれるのを待つ
	time.Sleep(150 * time.Millisecond)

	// --- Teardown ---
	cancel()
	wg.Wait()
	pipeline.Stop()

	// --- Assertion ---
	mockModel.AssertExpectations(t)
}

func TestPipeline_BufferIsClearedAfterTraining(t *testing.T) {
	// --- Setup ---
	mockModel := new(MockModel)
	stream := NewInMemoryFeatureStream(10)
	pipeline := NewPipeline(stream, mockModel, 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)

	// --- Expectation ---
	// 最初のTrain呼び出しをキャプチャ
	mockModel.On("Train", ctx, mock.AnythingOfType("[]*learning.Feature")).Run(func(args mock.Arguments) {
		features := args.Get(1).([]*Feature)
		assert.Len(t, features, 1) // 最初の訓練では特徴量は1つのはず
	}).Return(nil).Once()

	// 2回目の呼び出しをキャプチャ
	mockModel.On("Train", ctx, mock.AnythingOfType("[]*learning.Feature")).Run(func(args mock.Arguments) {
		features := args.Get(1).([]*Feature)
		assert.Len(t, features, 1) // バッファがクリアされていれば、次の特徴量は1つのはず
	}).Return(nil).Once()

	// --- Action ---
	go func() {
		defer wg.Done()
		pipeline.Start(ctx)
	}()

	// パイプラインがSubscribeするのを少し待つ
	time.Sleep(50 * time.Millisecond)

	// 1つ目の特徴量を流し、訓練を待つ
	err := stream.Publish(ctx, &Feature{Name: "feature1", Value: 0.1})
	assert.NoError(t, err)
	time.Sleep(150 * time.Millisecond)

	// 2つ目の特徴量を流し、訓練を待つ
	err = stream.Publish(ctx, &Feature{Name: "feature2", Value: 0.2})
	assert.NoError(t, err)
	time.Sleep(150 * time.Millisecond)

	// --- Teardown ---
	cancel()
	wg.Wait()
	pipeline.Stop()

	// --- Assertion ---
	mockModel.AssertExpectations(t)
}
