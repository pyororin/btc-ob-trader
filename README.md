# OBI Scalp Bot

OBI (Order Book Imbalance) に基づいてスキャルピングを行う取引ボットです。

## 主な機能

-   Coincheck の WebSocket API を利用してリアルタイムに板情報・取引情報を取得
-   OBI (Order Book Imbalance) を計算し、売買シグナルを生成
-   ボラティリティに応じて動的にパラメータを調整
-   TimescaleDB に取引履歴や計算指標を保存
-   Grafana によるパフォーマンスの可視化
-   過去データを利用したリプレイ（バックテスト）機能

## 技術スタック

-   **言語**: Go
-   **データベース**: TimescaleDB (PostgreSQL + TimescaleDB拡張)
-   **可視化**: Grafana
-   **コンテナ化**: Docker, Docker Compose

## セットアップ

### 前提条件

-   Docker および Docker Compose がインストールされていること
-   `make`コマンドが利用できること

### 1. リポジトリのクローン

```bash
git clone https://github.com/your-org/obi-scalp-bot.git
cd obi-scalp-bot
```

### 2. 環境変数の設定

.env.sample ファイルをコピーして .env ファイルを作成し、必要な情報を設定します。

```bash
cp .env.sample .env
```
.envファイルの中身を編集します。

```
# Coincheck API
COINCHECK_API_KEY="YOUR_API_KEY"
COINCHECK_API_SECRET="YOUR_API_SECRET"

# Database
DB_USER="your_db_user"
DB_PASSWORD="your_db_password"
DB_NAME="obi_scalp_bot_db"

# Grafana
GRAFANA_USER="admin"
GRAFana_PASSWORD="admin"
```

### 3. 設定ファイルの確認

`config/config.yaml` が基本的な設定ファイルです。取引ペアや戦略パラメータを調整できます。

## 実行方法

### 通常起動（本番トレード）

以下のコマンドで、ボットと関連サービス（データベース、Grafana）を起動します。

```bash
make up
```

### 監視のみ

ボットを起動せず、データベースとGrafanaのみを起動します。

```bash
make monitor
```

### ログの確認

```bash
make logs
```

### サービスの停止

```bash
make down
```

### リプレイ（バックテスト）の実行

`make replay` コマンドで、過去の取引データ（CSV）を使用してバックテストを実行できます。

```bash
make replay
```
リプレイモードでは、`config/config-replay.yaml` が使用されます。デフォルトでは `pkg/cvd/test_trades.csv` のデータを読み込みます。

**注意**: `docker` コマンドの実行に `sudo` が必要な環境では、`Makefile` が `sudo -E` を使用して環境変数を引き継ぐように設定されています。`sudo` なしで `docker` を実行できるユーザーは、`Makefile` 内の `sudo -E` を削除してください。

## Grafanaダッシュボード

`make up` または `make monitor` を実行後、ブラウザで http://localhost:3000 にアクセスします。
`.env` で設定したユーザー名とパスワードでログインしてください（デフォルト: admin/admin）。

## 開発

### テストの実行

```bash
make test
```

### ローカルビルド

```bash
make build
```
