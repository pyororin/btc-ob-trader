# `timescaledb` サービス 詳細設計書

## 1. サービスの概要

`timescaledb`サービスは、本アプリケーション全体のデータストアとして機能する時系列データベースです。公式の`timescale/timescaledb`イメージを利用しており、PostgreSQLをベースに時系列データ処理を高速化・効率化する拡張機能「TimescaleDB」が組み込まれています。

取引ボットが生成する大量の時系列データ（注文簿の更新、取引履歴、損益のスナップショットなど）を効率的に格納し、迅速なクエリ応答を実現する責務を担います。

## 2. Docker Compose上の役割と設定

`docker-compose.yml`における`timescaledb`サービスの定義は以下の通りです。

```yaml
services:
  timescaledb:
    image: timescale/timescaledb:2.11.2-pg14
    container_name: timescaledb-obi
    ports:
      - "5432:5432"
    environment:
      POSTGRES_USER: ${DB_USER}
      POSTGRES_PASSWORD: ${DB_PASSWORD}
      POSTGRES_DB: ${DB_NAME}
    volumes:
      - timescaledb_data:/var/lib/postgresql/data
      - ./db/schema:/docker-entrypoint-initdb.d/01_schema
    command: postgres -c timezone=Asia/Tokyo
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U $$POSTGRES_USER -d $$POSTGRES_DB"]
      interval: 10s
      timeout: 10s
      retries: 10
    restart: unless-stopped
    networks:
      - bot_network
```

### 設定解説

- **`image`**: TimescaleDBの公式イメージを指定しています。開発環境での軽量化のため、高可用性（HA）版から標準版イメージに変更しています。
- **`ports`**: PostgreSQLの標準ポート`5432`をホストマシンに公開し、外部のDBクライアント（Adminerなど）からの接続を可能にしています。
- **`environment`**: `.env`ファイルで定義された変数を用いて、データベースのユーザー名、パスワード、データベース名を設定します。
- **`volumes`**:
    - `timescaledb_data:/var/lib/postgresql/data`: データベースの永続データを格納するための名前付きボリューム。コンテナを削除・再作成してもデータが保持されます。
    - `./db/schema:/docker-entrypoint-initdb.d/01_schema`: `initdb`スクリプト用のディレクトリ。コンテナの初回起動時に、このディレクトリ内の`.sql`や`.sh`ファイルがアルファベット順に実行され、データベースのスキーマを初期化します。
- **`command`**: `postgres -c timezone=Asia/Tokyo` を指定し、データベースのタイムゾーンを東京時間に設定しています。
- **`healthcheck`**: `pg_isready`コマンドでデータベースが接続を受け付けられる状態か定期的に確認します。他のサービスはこのヘルスチェックが通るのを待ってから起動します。
- **`restart: unless-stopped`**: データベースが停止した場合でも自動的に再起動します。

## 3. データベーススキーマ

データベースのスキーマは、`./db/schema`ディレクトリ内のSQLファイルによって定義されます。

### 3.1. `001_tables.sql` - 主要データテーブル

- **`order_book_updates`**:
    - **目的**: 注文簿の更新履歴を格納します。
    - **主要カラム**: `time` (タイムスタンプ), `pair` (通貨ペア), `side` ('bid' or 'ask'), `price` (価格), `size` (数量)。
    - **特徴**: TimescaleDBの**Hypertable**に変換されており、`time`列で自動的にパーティショニングされます。7日以上経過したデータは自動的に圧縮され、ストレージ効率を高めます。

- **`pnl_summary`**:
    - **目的**: 定期的な損益（PnL）のスナップショットを記録します。
    - **主要カラム**: `time`, `realized_pnl` (実現損益), `unrealized_pnl` (評価損益), `total_pnl` (合計損益), `position_size` (ポジションサイズ)。
    - **特徴**: Hypertable。30日以上経過したデータは圧縮されます。

- **`trades`**:
    - **目的**: WebSocketから受信した全ての約定履歴を記録します。
    - **主要カラム**: `time`, `pair`, `side` ('buy' or 'sell'), `price`, `size`, `transaction_id`。
    - **特徴**: Hypertable。7日以上経過したデータは圧縮されます。

- **`pnl_reports`**:
    - **目的**: `report-generator`サービスが生成した日次などのパフォーマンスレポートの結果を格納します。
    - **主要カラム**: `time`, `total_trades`, `win_rate`, `total_pnl`, `max_drawdown`など、多数の集計指標。
    - **特徴**: Hypertable。

- **`trades_pnl`**:
    - **目的**: 個別の取引ごとの損益を記録します。
    - **主要カラム**: `trade_id`, `pnl`, `cumulative_pnl`。

### 3.2. `002_benchmark.sql` - ベンチマークテーブル

- **`benchmark_values`**:
    - **目的**: 市場の平均的な価格推移（例: BTC/JPYの中値）をベンチマークとして記録します。
    - **主要カラム**: `time`, `price`。
    - **特徴**: Hypertable。ボットのパフォーマンスが市場全体と比較してどうだったかを評価するために使用されます。

### 3.3. `003_views.sql` - 分析用ビュー

- **`v_performance_vs_benchmark`**:
    - **目的**: ボットの損益とベンチマーク価格を1分間隔で集計し、結合するビュー。
    - **特徴**: `pnl_summary`と`benchmark_values`をJOINし、ボットのパフォーマンス（アルファ）を簡単に可視化・分析できるようにします。Grafanaなどのダッシュボードツールからこのビューをクエリすることが想定されます。

## 4. 設計思想と考慮点

- **時系列データへの最適化**: TimescaleDBのHypertable機能と自動圧縮ポリシーを全面的に採用することで、大量の時系列データを扱う際の書き込み性能の維持とストレージコストの削減を両立させています。
- **データの永続化**: Dockerの名前付きボリューム（`timescaledb_data`）を使用することで、コンテナのライフサイクルからデータを分離し、データの永続性を確保しています。
- **自動初期化**: `docker-entrypoint-initdb.d`の仕組みを活用することで、`docker compose up`コマンド一発で、スキーマが適用された状態のデータベースを誰でも簡単にセットアップできます。
- **分析の容易性**: 生データだけでなく、分析用のビュー（`v_performance_vs_benchmark`）を予め用意しておくことで、後のデータ分析や可視化のフェーズを効率化しています。
- **イメージの軽量化**: 開発環境でのビルド時間短縮とリソース消費削減のため、標準の`timescaledb`イメージを使用しています。高可用性が必要な本番環境では`timescaledb-ha`イメージへの切り替えを検討します。
