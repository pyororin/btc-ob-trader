# `report-generator` サービス 詳細設計書

## 1. サービスの概要

`report-generator`サービスは、`timescaledb`に蓄積された生の取引履歴データを定期的に分析し、詳細なパフォーマンスレポートを生成・保存する責務を担うバッチ処理サービスです。

このサービスによって、生の取引データが意味のある統計指標（総損益、勝率、リスクリワードレシオなど）に変換され、`pnl_reports`テーブルに格納されます。`grafana`サービスは主にこの集計済みテーブルを参照することで、効率的にパフォーマンスダッシュボードを表示します。

## 2. Docker Compose上の役割と設定

`docker-compose.yml`における`report-generator`サービスの定義は以下の通りです。

```yaml
services:
  report-generator:
    build:
      context: .
      dockerfile: cmd/report/Dockerfile
    container_name: obi-scalp-report-generator
    env_file:
      - .env
    environment:
      - DATABASE_URL=postgres://${DB_USER}:${DB_PASSWORD}@timescaledb:${DB_PORT}/${DB_NAME}?sslmode=disable
      - REPORT_INTERVAL_MINUTES=1
    depends_on:
      timescaledb:
        condition: service_healthy
    restart: unless-stopped
    networks:
      - bot_network
```

### 設定解説

- **`build`**: `cmd/report/Dockerfile`を使用して、本サービス専用の軽量なGoバイナリを含むDockerイメージをビルドします。
- **`environment`**:
    - `DATABASE_URL`: `timescaledb`サービスに接続するための完全な接続文字列を指定します。
    - `REPORT_INTERVAL_MINUTES=1`: レポート生成処理を実行する間隔を1分に設定しています。
- **`depends_on`**: `timescaledb`サービスが正常に起動してから本サービスを開始するように依存関係を定義しています。
- **`restart: unless-stopped`**: サービスが何らかの理由で停止した場合でも、自動的に再起動します。

## 3. 処理フローとロジック

### 3.1. 起動と初期化

1.  コンテナが起動すると、`cmd/report/main.go`の`main`関数が実行されます。
2.  `DATABASE_URL`環境変数を読み取り、`pgxpool`ライブラリを使って`timescaledb`への接続プールを確立します。
3.  `REPORT_INTERVAL_MINUTES`環境変数を読み取り、`time.ParseDuration`で`time.Duration`型に変換します。この間隔で時を刻む`time.Ticker`を初期化します。
4.  アプリケーションの起動時に、まず一度目のレポート生成処理 `runReportGeneration` を即時実行します。

### 3.2. 定期実行ループ

1.  `main`関数は無限ループに入り、`Ticker`からの通知を待ち受けます。
2.  `docker-compose.yml`の設定に基づき、1分ごとに`Ticker`が通知を発行します。
3.  通知を受け取るたびに、`runReportGeneration`関数を再度実行します。

### 3.3. レポート生成処理 (`runReportGeneration`)

`runReportGeneration`関数は以下のステップで処理を実行します。

1.  **最終取引IDの取得**: `pnl_reports`テーブルにアクセスし、最後に生成されたレポートがどの取引IDまでを対象としていたか (`last_trade_id`) を取得します。これにより、処理の重複を防ぎます。初回実行時など、レポートが存在しない場合は `0` からスタートします。
2.  **新規取引データの取得**: `trades`テーブルにアクセスし、前回処理した`last_trade_id`よりも新しい**自分自身の**取引レコード（`is_my_trade = TRUE`）をすべて取得します。
3.  **処理対象の有無をチェック**: 新しい取引レコードが存在しない場合、ログに「No new trades」と記録し、今回の処理はここで終了します。
4.  **パフォーマンス分析**: 新しい取引レコードが存在する場合、それらを`report.Service`の`AnalyzeTrades`メソッドに渡します。このメソッド内で、以下のような様々なパフォーマンス指標が計算されます。
    -   総取引回数、勝ちトレード数、負けトレード数、勝率
    -   ロング/ショート別の勝率
    -   総損益 (Total PnL)
    -   平均利益、平均損失
    -   リスクリワードレシオ
    -   プロフィットファクター、シャープレシオ、最大ドローダウンなど
5.  **レポートの保存**: `AnalyzeTrades`から返された分析結果の構造体を、`SavePnlReport`メソッドを使って`pnl_reports`テーブルの新しい行として挿入します。
6.  処理が完了したことを示すログを出力して、`Ticker`からの次の通知を待ちます。

## 4. 設計思想と考慮点

-   **関心の分離**: 取引を実行する`bot`サービスと、レポートを生成する`report-generator`サービスを分離することで、それぞれの責務が明確になっています。`bot`サービスは取引執行のレイテンシを最優先し、重い集計処理は`report-generator`にオフロードする設計です。
-   **効率的なデータ処理**: 全ての取引履歴を毎回スキャンするのではなく、最後に処理した取引IDを記録しておくことで、差分データのみを効率的に処理しています。これにより、データベースへの負荷を最小限に抑えています。
-   **回復力**: `restart: unless-stopped`ポリシーにより、データベース接続の一次的な問題などでサービスが停止しても自動で復旧します。また、差分処理の仕組みにより、停止していた時間分の未処理データを復旧後にまとめて処理することができます。
-   **設定の柔軟性**: レポートの生成間隔を環境変数 `REPORT_INTERVAL_MINUTES` で変更できるため、ユースケース（例: テスト環境ではより短く、本番環境ではより長く）に応じて挙動を容易に調整できます。

## 5. 想定ユースケースとトリガー

-   **ユースケース**: `bot`サービスによって`trades`テーブルに蓄積されていく生の取引データを、`grafana`などのダッシュボードツールで可視化・分析しやすいように、事前に集計・加工しておく。
-   **トリガー**: サービスの起動時、およびその後`REPORT_INTERVAL_MINUTES`で設定された時間間隔（デフォルトでは1分）ごとに自動的に実行されます。
