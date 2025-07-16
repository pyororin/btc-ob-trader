package jsonnet

import (
	"fmt"
	"io"
	"net/http"

	"github.com/xeipuuv/gojsonschema"
)

const grafanaSchemaURL = "https://raw.githubusercontent.com/grafana/grafana/main/docs/sources/developers/plugins/json-reference.json"

// ValidateDashboard は、与えられたダッシュボードのJSONがGrafanaのスキーマに準拠しているか検証します。
func ValidateDashboard(dashboardJSON []byte) (bool, []gojsonschema.ResultError, error) {
	// スキーマをダウンロード
	resp, err := http.Get(grafanaSchemaURL)
	if err != nil {
		return false, nil, fmt.Errorf("failed to download grafana schema: %w", err)
	}
	defer resp.Body.Close()

	schemaBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, nil, fmt.Errorf("failed to read schema body: %w", err)
	}

	schemaLoader := gojsonschema.NewBytesLoader(schemaBytes)
	documentLoader := gojsonschema.NewBytesLoader(dashboardJSON)

	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		return false, nil, fmt.Errorf("validation error: %w", err)
	}

	if result.Valid() {
		return true, nil, nil
	}
	return false, result.Errors(), nil
}
