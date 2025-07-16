package jsonnet

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGeneratedDashboardSchema は、生成されたダッシュボードJSONがスキーマに準拠しているかテストします。
func TestGeneratedDashboardSchema(t *testing.T) {
	// Makefileを使ってダッシュボードを生成
	cmd := exec.Command("make", "all")
	cmd.Dir = "." // Makefileのあるディレクトリ
	err := cmd.Run()
	require.NoError(t, err, "failed to generate dashboard json")

	// 生成されたJSONを読み込む
	jsonPath := filepath.Join("../dashboards", "bench_dashboard.json")
	dashboardBytes, err := os.ReadFile(jsonPath)
	require.NoError(t, err)

	// スキーマ検証
	valid, errors, err := ValidateDashboard(dashboardBytes)
	require.NoError(t, err)

	assert.True(t, valid, "dashboard schema is not valid: %v", errors)
}
