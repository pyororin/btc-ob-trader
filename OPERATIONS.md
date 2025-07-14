# 本番運用手順 (OPERATIONS.md)

このドキュメントは、`obi-scalp-bot` を本番環境 (Google Cloud Run および Cloud SQL for PostgreSQL + TimescaleDB) で運用するための手順、監視、基本的なSOP (Standard Operating Procedure) を記述します。

## 1. アーキテクチャ概要

-   **アプリケーション**: Go言語で実装されたBot本体。Cloud Runサービスとしてコンテナ化され、常に1インスタンスで稼働。
-   **データベース**: Cloud SQL for PostgreSQL に TimescaleDB 拡張を追加したもの。市場データ、取引履歴、PnLなどを保存。
-   **監視**: Cloud Logging, Cloud Monitoring, そしてパフォーマンス可視化のためのGrafanaで構成。

## 2. デプロイ手順

### 2.1. Cloud Run へのデプロイ

デプロイにはサービス定義ファイル (`service.yaml`) の利用を推奨します。

```yaml
# service.yaml
apiVersion: serving.knative.dev/v1
kind: Service
metadata:
  name: obi-scalp-bot-prod
spec:
  template:
    metadata:
      annotations:
        autoscaling.knative.dev/maxScale: '1' # 常に1インスタンスで実行
    spec:
      containerConcurrency: 80
      timeoutSeconds: 300
      containers:
      - image: gcr.io/YOUR_PROJECT_ID/obi-scalp-bot:latest
        ports:
        - name: http1
          containerPort: 8080
        env:
        - name: GIN_MODE
          value: "release"
        # APIキーやDBパスワードはSecret Manager経由でのマウントを強く推奨
        # - name: COINCHECK_API_KEY
        #   valueFrom:
        #     secretKeyRef:
        #       name: bot-secrets
        #       key: coincheck-api-key
        resources:
          limits:
            memory: 512Mi
            cpu: "1"
        startupProbe:
          timeoutSeconds: 300
          periodSeconds: 60
          failureThreshold: 5
          httpGet:
            path: "/health" # ヘルスチェックエンドポイントを指定
            port: 8080
```

**デプロイコマンド:**
```bash
gcloud run services replace service.yaml --region YOUR_REGION --platform managed
```

### 2.2. データベース接続

Goアプリケーション内で `cloud.google.com/go/cloudsqlconn` パッケージを利用し、Cloud SQL Auth Proxyクライアントとして機能させることを推奨します。これにより、IPホワイトリストやSSL証明書の管理が不要になります。

## 3. 監視

効果的な運用には、システムの健全性とBotのパフォーマンスの両面からの監視が不可欠です。

### 3.1. 主要な監視対象とログ

Cloud Logging で以下のキーワードに注目し、必要に応じてログベースのアラートを設定します。

-   `"Health check server starting"`: Botの正常起動を示す。
-   `"Connecting to Coincheck WebSocket API"`: WebSocketへの接続開始。
-   `"Order monitor starting"`: **未約定注文の監視機能**の起動。この機能は、不利な価格に置かれた指値注文を自動でキャンセル・再発注します。
    -   `"Adjusting order"`: このログは、注文調整が実行されたことを示します。頻発する場合は、市場の急変動やパラメータの問題を示唆している可能性があります。
-   `"Starting PnL reporter"`: **PnLレポートの定期生成機能**の起動。
    -   `"Successfully generated and saved PnL report"`: 定期的なレポートが正常に作成されていることを示します。
-   `"Failed to ..."` / `"Error ..."`: 想定外のエラー。特に `PlaceOrder`, `CancelOrder`, `GetOpenOrders` に関連するエラーは取引機会の損失やリスクに直結するため、即時通知アラートを設定します。
-   `"WebSocket client exited with error"`: WebSocketが切断されたことを示す重大なエラー。Botは自動で停止します。

### 3.2. アラート設定 (Cloud Monitoring)

以下の事象に対してアラートを設定します。

-   **ヘルスチェック失敗**: `startupProbe` の失敗はコンテナの起動失敗を意味し、即時対応が必要です。
-   **エラーログの急増**: 上記 3.1 でリストしたエラーログの発生頻度に基づきアラートを設定します。
-   **DB接続エラー**: データベースへの接続失敗を示すログに対するアラート。
-   **取引APIエラー**: Coincheck APIからのエラーレスポンス（4xx, 5xx系）を示すログに対するアラート。
-   **Cloud SQL のリソース逼迫**: ディスク容量、CPU使用率、メモリ使用率が閾値を超えた場合に通知します。

### 3.3. パフォーマンス監視 (Grafana)

本番環境のGrafanaダッシュボード (`http://<your-prod-ip>:3000` など、アクセス制御されたURL) は、Botの経済的パフォーマンスを評価する上で最も重要なツールです。

-   **累積損益 (Cumulative PnL)**: 最も重要な指標。予期せぬ下降トレンドが発生していないか、常に監視します。
-   **勝率 (Win Rate) とプロフィットファクター**: 戦略の有効性を示します。これらの指標が市場の変化によって悪化していないか確認します。
-   **取引履歴 (Recent Trades)**: Botが想定通りのロジックで取引しているか（価格、量、タイミング）をスポットチェックします。

## 4. 標準運用手順 (SOP)

### 4.1. 定期チェック

-   **毎日**:
    -   Grafanaダッシュボードで累積損益、勝率、ドローダウンを確認。
    -   Cloud Loggingで前日分のエラーログを確認。
    -   取引所の口座残高とBotが認識している残高に大きな乖離がないか確認。
-   **毎週**:
    -   PnLレポートの結果を確認し、週次のパフォーマンスを評価。
    -   Cloud SQLのディスク使用量やリソース使用率のトレンドを確認。

### 4.2. 緊急対応

#### シナリオ1: Botインスタンスがクラッシュ・再起動を繰り返す

1.  **Cloud Runのログ確認**: Cloud Loggingでコンテナの終了直前のログを確認し、`panic` や `fatal` エラーの原因を特定します。
2.  **原因調査**: 設定ミス（環境変数）、外部APIの障害、コードのバグなどが考えられます。
3.  **対応**:
    -   設定ミスなら、修正して再デプロイ。
    -   コードのバグなら、安全なリビジョンにロールバックし、修正後に再度デプロイ。

#### シナリオ2: 過大な損失が発生している

1.  **即時Bot停止**: Cloud Runコンソールまたはgcloudコマンドで、インスタンス数を0にスケールダウンします。
    ```bash
    gcloud run services update obi-scalp-bot-prod --update-annotations autoscaling.knative.dev/maxScale=0 --region YOUR_REGION
    ```
2.  **ポジションの手動クローズ**: 取引所のWebサイト等で、残っている危険なポジションを手動で解消します。
3.  **原因分析**: Grafanaの取引履歴と市場チャートを照らし合わせ、ロジックの欠陥か、予期せぬ市場の急変動かを分析します。
4.  **修正とバックテスト**: 原因を特定し、修正後に十分なバックテスト（DBリプレイ）を行ってから再稼働させます。

#### シナリオ3: 注文が約定せず、一方的に溜まり続ける

1.  **ログ確認**: Cloud Loggingで `"Adjusting order"` ログが頻発していないか確認します。
2.  **原因調査**:
    -   ログが頻発している場合: `orderMonitor` が機能しているが、市場の流動性が極端に低いか、価格変動が激しすぎて追いつけていない可能性があります。
    -   ログが出ていない場合: `orderMonitor` 自体が機能していないか、閾値の設定が緩すぎる可能性があります。
3.  **対応**:
    -   一時的にBotを停止し、`config.yaml` の注文調整に関するパラメータを見直します。
    -   市場が落ち着くのを待ってから再開するか、ロジックを修正してデプロイします。

## 5. セキュリティ

-   **APIキー**: 必要最小限の権限（取引、残高確認など）を付与し、Secret Managerで管理します。出金権限は絶対に付与しないでください。
-   **DBアクセス**: 強力なパスワードを使用し、Secret Managerで管理します。
-   **サービスアクセス**: Cloud Runサービスは、原則として外部からのリクエストを受け付けないように設定します (`--no-allow-unauthenticated`)。ヘルスチェックはVPC内部からのみ行われるように構成します。
-   **脆弱性スキャン**: `govulncheck` をCI/CDパイプラインに組み込み、依存関係の脆弱性を定期的にスキャンします。

---
*このドキュメントは一般的なガイドラインです。実際の運用に合わせて、具体的な監視クエリ、アラート閾値、障害復旧コマンドなどを整備・追記してください。*
