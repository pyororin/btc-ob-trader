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

`make replay` コマンドで、過去の取引データ（データベースに保存されているデータ）を使用してバックテストを実行できます。

バックテストのパラメータは `config/config-replay.yaml` で設定します。

```yaml
replay:
  # バックテストの開始時刻 (UTC)
  # 形式: "YYYY-MM-DDTHH:MM:SSZ"
  start_time: "2024-01-01T00:00:00Z"

  # バックテストの終了時刻 (UTC)
  # 形式: "YYYY-MM-DDTHH:MM:SSZ"
  end_time: "2024-01-02T00:00:00Z"
```

以下のコマンドでバックテストを実行します。

```bash
make replay
```
リプレイモードでは、指定された期間の取引データと板情報がデータベースから読み込まれ、シミュレーションが実行されます。

### シミュレーションモード（CSVバックテスト）

シミュレーションモードでは、データベースを必要とせず、ローカルのCSVファイルを使ってバックテストを実行できます。OBI（Order Book Imbalance）を利用した戦略のパラメータチューニングを高速に行いたい場合に便利です。

**1. CSVデータの準備**

まず、データベースに保存されている過去の板情報（`order_book_updates`テーブル）をCSVファイルにエクスポートします。以下のコマンドを実行してください。`START_TIME`と`END_TIME`には、エクスポートしたいデータの期間を `YYYY-MM-DD HH:MM:SS` 形式で指定します。

```bash
make export-sim-data START_TIME='2024-01-01 00:00:00' END_TIME='2024-01-01 01:00:00'
```

このコマンドは、`./simulation/` ディレクトリに `order_book_updates_YYYYMMDD-HHMMSS_COUNT.csv` という形式でCSVファイルを生成します。

エクスポートされるCSVは、以下のカラムを持ちます（ヘッダー行付き）。

- `time`: 板情報のタイムスタンプ（RFC3339形式: `2023-01-01T15:04:05Z` や `2025-07-14 04:11:13.484971+00` など）
- `pair`: 通貨ペア（例: `btc_jpy`）
- `side`: `bid` または `ask`
- `price`: 価格
- `size`: 数量
- `is_snapshot`: スナップショットかどうかのフラグ (`t` または `f`)

**CSVファイルの例 (`order_book_updates.csv`):**
```csv
time,pair,side,price,size,is_snapshot
2025-07-14 04:11:13.484971+00,btc_jpy,bid,17778168,0,t
2025-07-14 04:11:13.484971+00,btc_jpy,ask,17799246,0,t
2025-07-14 04:11:13.484971+00,btc_jpy,bid,17773410,0.05,t
...
```

**2. パラメータの設定**

シミュレーションには `config/config.yaml` が使用されます。このファイル内の戦略パラメータを調整してください。

**3. シミュレーションの実行**

以下のコマンドを実行します。`CSV_PATH`には準備したCSVファイルのパスを指定します。

```bash
make simulate CSV_PATH=/path/to/your/trades.csv
```

**4. 結果の確認**

シミュレーションが完了すると、コンソールに以下のようなパフォーマンスサマリーが出力されます。

```
==== シミュレーション結果 ====
総損益　     : +1234.56 JPY
取引回数     : 20回
勝率         : 60.00% (6勝/4敗)
平均利益/損失: +150.00 JPY / -75.00 JPY
最大ドローダウン: N/A
==========================
```

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
