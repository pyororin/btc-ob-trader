package jsonnet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xeipuuv/gojsonschema"
)

// TestGenerateAndValidateDashboards はJsonnetからJSONを生成し、スキーマ検証を行います。
func TestGenerateAndValidateDashboards(t *testing.T) {
	// このテストは `make grafana-lint` から実行されることを想定
	// jsonnet コマンドがなければスキップ
	if _, err := exec.LookPath("jsonnet"); err != nil {
		t.Skip("jsonnet command not found, skipping dashboard generation and validation")
	}

	jsonnetFiles, err := filepath.Glob("*.jsonnet")
	assert.NoError(t, err)

	for _, jf := range jsonnetFiles {
		t.Run(fmt.Sprintf("Validating %s", jf), func(t *testing.T) {
			// 1. Jsonnet -> JSON
			outputFile := strings.Replace(jf, ".jsonnet", ".json", 1)
			// grafonnet-libをインポートするために -J vendor が必要
			// vendorディレクトリは `jb install` で作成される想定
			cmd := exec.Command("jsonnet", "-J", "../../vendor", "-o", outputFile, jf)
			cmd.Stderr = os.Stderr
			err := cmd.Run()
			if err != nil {
				// vendorがない場合のエラーメッセージを表示
				if _, err := os.Stat("../../vendor"); os.IsNotExist(err) {
					t.Fatalf("jsonnet command failed. Did you forget to run 'make vendor' (jb install)? Error: %v", err)
				}
				t.Fatalf("jsonnet command failed: %v", err)
			}
			defer os.Remove(outputFile) // 生成したJSONファイルをクリーンアップ

			// 2. スキーマ検証
			// Grafanaの公式スキーマは巨大なので、ここでは単純な構造のみをチェックするダミースキーマを使用
			// 実際には公式スキーマを取得して使うのが望ましい
			schemaLoader := gojsonschema.NewStringLoader(`{
				"type": "object",
				"properties": {
					"title": {"type": "string"},
					"panels": {"type": "array"}
				},
				"required": ["title", "panels"]
			}`)
			documentLoader := gojsonschema.NewReferenceLoader(fmt.Sprintf("file://%s", outputFile))

			result, err := gojsonschema.Validate(schemaLoader, documentLoader)
			assert.NoError(t, err)

			if !result.Valid() {
				t.Errorf("The document %s is not valid. see errors:", jf)
				for _, desc := range result.Errors() {
					t.Errorf("- %s", desc)
				}
			}
			assert.True(t, result.Valid(), "Generated dashboard should be valid against the schema")
		})
	}
}
