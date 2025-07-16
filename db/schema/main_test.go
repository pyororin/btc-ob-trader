package schema

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// findProjectRoot searches for the project root directory (where go.mod is located)
// starting from the current working directory and moving upwards.
func findProjectRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err, "Failed to get working directory")

	for i := 0; i < 5; i++ { // Limit search to 5 levels up
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		prevWd := wd
		wd = filepath.Dir(wd)
		if wd == prevWd { // Reached the root of the filesystem
			break
		}
	}
	t.Fatalf("Failed to find project root (go.mod)")
	return ""
}

// TestMigrationsNotEmpty ensures that all migration .sql files are not empty.
// This is a basic sanity check to catch accidental empty files.
func TestMigrationsNotEmpty(t *testing.T) {
	rootPath := findProjectRoot(t)
	migrationsPath := filepath.Join(rootPath, "db", "schema")

	files, err := os.ReadDir(migrationsPath)
	require.NoError(t, err, "Failed to read migrations directory: %s", migrationsPath)

	for _, file := range files {
		fileName := file.Name()
		if strings.HasSuffix(fileName, ".sql") {
			filePath := filepath.Join(migrationsPath, fileName)
			content, err := os.ReadFile(filePath)
			require.NoError(t, err, "Failed to read migration file: %s", filePath)
			require.NotEmpty(t, content, "Migration file is empty: %s", fileName)
		}
	}
}

// TestMigrationFileNames ensures that all migration files follow the naming convention.
// The expected format is "NNN_description.sql" where NNN is a number.
func TestMigrationFileNames(t *testing.T) {
	rootPath := findProjectRoot(t)
	migrationsPath := filepath.Join(rootPath, "db", "schema")

	files, err := os.ReadDir(migrationsPath)
	require.NoError(t, err, "Failed to read migrations directory: %s", migrationsPath)

	foundSQLFile := false
	for _, file := range files {
		fileName := file.Name()
		if strings.HasSuffix(fileName, ".sql") {
			foundSQLFile = true
			baseName := strings.TrimSuffix(fileName, ".sql")
			parts := strings.Split(baseName, "_")
			require.True(t, len(parts) >= 2, "File name %q does not match format NNN_description.sql", fileName)

			_, err := strconv.Atoi(parts[0])
			require.NoError(t, err, "File name %q does not start with a number: %v", fileName, err)
		}
	}
	require.True(t, foundSQLFile, "No .sql migration files found in %s", migrationsPath)
}
