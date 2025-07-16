//go:build sqltest
// +build sqltest

package dbwriter

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/DATA-DOG/go-txdb"
	_ "github.com/lib/pq" // PostgreSQL driver
)

func init() {
	// DSNは実際には接続しないのでダミーで良い
	// 接続試行自体を避けるため、無効なホストを指定する
	txdb.Register("txdb", "postgres", "user=test password=test dbname=test host=/var/run/postgresql sslmode=disable")
}

func TestMigrations(t *testing.T) {
	// ../../migrations からプロジェクトルート基準のmigrationsディレクトリを探す
	migrationsDir := "../../migrations"

	files, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("failed to read migrations directory: %v", err)
	}

	for _, file := range files {
		if filepath.Ext(file.Name()) == ".sql" {
			t.Run(file.Name(), func(t *testing.T) {
				db, err := sql.Open("txdb", file.Name()) // DSNはtxdb.Registerで指定したもの
				if err != nil {
					t.Fatalf("failed to open database: %v", err)
				}
				defer db.Close()

				content, err := os.ReadFile(filepath.Join(migrationsDir, file.Name()))
				if err != nil {
					t.Fatalf("failed to read migration file: %v", err)
				}

				// トランザクション内でスキーマを実行し、エラーがなければOK
				tx, err := db.Begin()
				if err != nil {
					t.Fatalf("failed to begin transaction: %v", err)
				}
				defer tx.Rollback() // 常にロールバックしてDB状態を変更しない

				if _, err := tx.Exec(string(content)); err != nil {
					t.Errorf("migration failed: %v", err)
				}
			})
		}
	}
}
