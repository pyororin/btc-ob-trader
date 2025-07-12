# 本番運用手順 (OPERATIONS.md)

このドキュメントは、`obi-scalp-bot` を本番環境 (Google Cloud Run および Cloud SQL for PostgreSQL + TimescaleDB) で運用するための手順、監視、基本的なSOP (Standard Operating Procedure) を記述します。

## 1. 前提条件

- Google Cloud Platform (GCP) プロジェクトがセットアップ済みであること。
- `gcloud` CLI がインストールされ、認証済みであること。
- Docker イメージが Google Container Registry (GCR) または Artifact Registry にプッシュ済みであること。
- Cloud SQL for PostgreSQL インスタンス (TimescaleDB拡張機能付き) が作成済みであること。
- 本番用の `.env` ファイルまたは Secret Manager に本番環境設定 (APIキー、DB接続情報など) が安全に保存されていること。

## 2. デプロイ手順

### 2.1. Cloud Run へのデプロイ

1.  **サービス定義ファイル (例: `service.yaml`) の準備 (推奨)**
    ```yaml
    # service.yaml (例)
    apiVersion: serving.knative.dev/v1
    kind: Service
    metadata:
      name: obi-scalp-bot-prod # サービス名
      annotations:
        run.googleapis.com/launch-stage: BETA
    spec:
      template:
        metadata:
          annotations:
            autoscaling.knative.dev/maxScale: '1' # スキャルピングBotは通常1インスタンスで実行
        spec:
          containerConcurrency: 80 # デフォルト値、必要に応じて調整
          timeoutSeconds: 300    # デフォルト値、必要に応じて調整
          containers:
          - image: gcr.io/YOUR_PROJECT_ID/obi-scalp-bot:latest # イメージのパスを適宜変更
            ports:
            - name: http1
              containerPort: 8080 # DockerfileでEXPOSEするポート (ヘルスチェック用など)
            env:
            - name: GIN_MODE
              value: "release"
            - name: GOOGLE_CLOUD_PROJECT
              value: "YOUR_PROJECT_ID" # プロジェクトID
            # - name: DB_USER
            #   valueFrom:
            #     secretKeyRef:
            #       name: obi-scalp-bot-secrets # Secret Manager のシークレット名
            #       key: latest # または特定のバージョン
            # (他の環境変数も同様にSecret Managerから読み込むことを推奨)
            resources:
              limits:
                memory: 512Mi
                cpu: "1"
            startupProbe: # ヘルスチェック (起動プローブ)
              timeoutSeconds: 240
              periodSeconds: 240
              failureThreshold: 1
              tcpSocket:
                port: 8080 # ヘルスチェック用ポート
    ```
    **注意:** 上記は一例です。実際の要件に合わせてメモリ、CPU、環境変数、シークレットの取り扱いなどを調整してください。特にAPIキーやDBパスワードはSecret Managerの使用を強く推奨します。

2.  **Cloud Run サービスのデプロイ**
    `service.yaml` を使用する場合:
    ```bash
    gcloud run services replace service.yaml --region YOUR_REGION --platform managed
    ```
    直接デプロイする場合:
    ```bash
    gcloud run deploy obi-scalp-bot-prod \
      --image gcr.io/YOUR_PROJECT_ID/obi-scalp-bot:latest \
      --platform managed \
      --region YOUR_REGION \
      --allow-unauthenticated \ # 必要に応じて変更 (例: Pub/Subトリガーの場合は認証が必要)
      --max-instances=1 \
      --concurrency=80 \
      --timeout=300 \
      --set-env-vars="GIN_MODE=release,GOOGLE_CLOUD_PROJECT=YOUR_PROJECT_ID" \
      # --update-secrets=DB_USER=obi-scalp-bot-secrets:latest,DB_PASSWORD=...
      # ... 他の環境変数やリソース設定
    ```

### 2.2. Cloud SQL Proxy (推奨)

Cloud Run から Cloud SQL へ安全に接続するために、Cloud SQL Proxy をサイドカーとしてデプロイするか、アプリケーション内でGoのCloud SQLコネクタライブラリを使用します。後者がよりモダンなアプローチです。

Goアプリケーション内で `cloud.google.com/go/cloudsqlconn` パッケージを使用する場合、特別な設定は不要で、DB接続文字列にインスタンス接続名を含めるだけです。

```go
// 例: internal/config/config.go や main.go でのDB接続設定
// dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable",
//    cfg.DB.Host, cfg.DB.User, cfg.DB.Password, cfg.DB.Name, cfg.DB.Port)
// の代わりに、インスタンス接続名を使用
// dsn := fmt.Sprintf("user=%s password=%s database=%s host=/cloudsql/%s",
//    cfg.DB.User, cfg.DB.Password, cfg.DB.Name, "YOUR_PROJECT:YOUR_REGION:YOUR_INSTANCE")
```

### 2.3. データベーススキーマの適用

Cloud SQLインスタンスに初めて接続する際、またはスキーマの更新が必要な場合は、手動で `db/schema/*.sql` を実行するか、マイグレーションツール (例: `golang-migrate/migrate`) を導入して適用します。

## 3. 監視

### 3.1. Cloud Logging & Monitoring

- **ログ**: Cloud Run は標準出力/標準エラーに出力されたログを自動的に Cloud Logging に収集します。Botのログレベルやフォーマットを適切に設定し、重要なイベント (エラー、取引実行、ポジション変更など) が記録されるようにします。
- **メトリクス**: Cloud Run はリクエスト数、レイテンシ、コンテナCPU/メモリ使用率などの標準メトリクスを提供します。これらをCloud Monitoringで監視し、アラートを設定します。
- **カスタムメトリクス**: Botの運用状況 (例: PnL、アクティブオーダー数、エラーレート) を示すカスタムメトリクスを OpenCensus や OpenTelemetry などで収集し、Cloud Monitoring に送信することを検討します。

### 3.2. アラート設定

Cloud Monitoring で以下のような状況に対するアラートを設定します。

- **Botインスタンスの異常終了/ヘルスチェック失敗**
- **エラーログの急増**
- **DB接続エラー**
- **取引APIエラーの頻発**
- **大きな損失の発生 (カスタムメトリクスベース)**
- **Cloud SQLインスタンスの高負荷、ディスク容量逼迫**

### 3.3. ダッシュボード

Cloud Monitoring で、リソース使用率などのインフラ関連メトリクスを表示するダッシュボードを作成します。

加えて、本プロジェクトでは **Grafana** を用いて、よりビジネスロジックに近いパフォーマンス指標（損益、取引統計など）を可視化します。

#### Grafana ダッシュボードの利用

本番環境の `docker-compose.yml` (または相当するCloud Runのデプロイ定義) に `grafana` サービスが含まれていることを前提とします。

1.  **アクセス**:
    - 本番環境のGrafana URLにアクセスします (例: `http://<your-prod-ip>:3000`)。アクセス制御が適切に設定されていることを確認してください。
    - ログイン後、「OBI Scalp Bot PnL」ダッシュボードを開きます。

2.  **監視ポイント**:
    - **累積損益 (Cumulative PnL)**: Botの全体的なパフォーマンスを把握します。予期せぬ下降トレンドが発生していないか監視します。
    - **勝率 (Win Rate) と合計損益 (Total PnL)**: 戦略の有効性を評価します。これらの指標が著しく悪化した場合、ロジックの見直しや市場適応性の調査が必要です。
    - **取引履歴 (Recent Trades)**: 個別の取引が想定通りに行われているか（価格、サイズ、方向）を確認します。エラーや異常な取引がないかスポットチェックします。

3.  **データソース**:
    - ダッシュボードは本番用のデータベース (`TimescaleDB (Production)`) をデータソースとしています。
    - リプレイ結果を確認したい場合は、データソースを `TimescaleDB (Replay)` に切り替えることで、同じダッシュボードでバックテスト結果を分析できます（ローカル環境での利用が主）。

## 4. 標準運用手順 (SOP)

### 4.1. 定期チェック

- **毎日**:
    - ダッシュボードで主要メトリクスを確認。
    - エラーログを確認し、異常がないか調査。
    - 取引履歴と残高を取引所の画面と照合 (可能な範囲で)。
- **毎週**:
    - より詳細なログ分析。
    - パフォーマンスレビュー (レイテンシ、リソース使用率)。
    - 証拠金維持率や資金状況の確認。

### 4.2. 緊急対応

#### 4.2.1. Botが停止した場合

1.  **Cloud Run のログとステータス確認**:
    - `gcloud run services describe obi-scalp-bot-prod --region YOUR_REGION`
    - Cloud Logging でエラーメッセージを確認。
2.  **原因調査**:
    - 設定ミス (環境変数、APIキーなど)
    - コードのバグ
    - 外部API (取引所、DB) の障害
    - リソース不足 (メモリ、CPU)
3.  **対応**:
    - 設定ミスであれば修正して再デプロイ。
    - コードのバグであれば、修正版をデプロイするか、問題のある機能を無効化して一時的にロールバック。
    - 外部要因であれば、要因の解消を待つか、Botを一時停止。
4.  **再起動**:
    - Cloud Run は通常自動で再起動を試みますが、手動で新しいリビジョンをデプロイすることで強制的に再起動も可能です。

#### 4.2.2. 過大な損失が発生している場合

1.  **即時Bot停止**:
    - Cloud Run のインスタンス数を0にスケールダウン (コンソールまたは `gcloud` コマンド)。
    `gcloud run services update obi-scalp-bot-prod --update-annotations autoscaling.knative.dev/maxScale=0 --region YOUR_REGION`
2.  **原因調査**:
    - ログ、取引履歴、市場状況の分析。
    - ロジックの欠陥か、予期せぬ市場の動きか。
3.  **ポジションの手動クローズ (必要な場合)**:
    - 取引所のウェブサイトまたはAPI経由で、Botが保有する危険なポジションを手動で解消。
4.  **修正とテスト**:
    - 原因を特定し、コードや設定を修正。
    - 十分なテスト (バックテスト、ペーパーテスト) を経てから再稼働。

#### 4.2.3. 取引所APIエラーが多発する場合

1.  **取引所のステータスページ確認**:
    - 取引所側で障害が発生していないか確認。
2.  **Botのログ確認**:
    - APIキーの権限、IP制限、リクエストレート制限などを確認。
3.  **対応**:
    - 取引所側の問題であれば、復旧を待つ。
    - Bot側の設定やロジックの問題であれば修正。
    - 必要であれば一時的にBotを停止。

### 4.3. 設定変更とアップデート

1.  **開発環境/ステージング環境でのテスト**:
    - 新しいコードや設定変更は、必ずローカルやステージング環境で十分にテストします。
2.  **段階的ロールアウト (可能な場合)**:
    - Cloud Run はリビジョン管理とトラフィックスプリット機能を提供しており、新しいバージョンを一部のトラフィックにのみ公開して様子を見ることができます。 (スキャルピングBotでは難しい場合もある)
3.  **デプロイ**:
    - 上記「2.1. Cloud Run へのデプロイ」の手順に従って新しいリビジョンをデプロイ。
4.  **監視**:
    - デプロイ後、ログやメトリクスを注意深く監視し、問題が発生した場合は速やかにロールバック (`gcloud run services update-traffic` で旧リビジョンに100%戻すなど)。

## 5. セキュリティ

- **APIキーの管理**:
    - 必要最小限の権限を付与。
    - Secret Manager に保存し、Cloud Run から安全にアクセス。
    - 定期的なローテーションを検討。
- **DBアクセス**:
    - 強力なパスワードを使用し、Secret Manager に保存。
    - Cloud SQL Proxy や Goコネクタライブラリ経由で安全に接続。
    - データベースユーザーの権限を最小限に。
- **Cloud Run サービスの保護**:
    - `allow-unauthenticated` は慎重に使用。Botが外部からのHTTPリクエストを受け付ける必要がない場合は、IAM認証を有効にするか、内部トラフィックのみ許可。
- **依存関係の脆弱性スキャン**:
    - 定期的にGoモジュールの脆弱性をスキャン (例: `govulncheck`)。

## 6. バックアップとリストア (Cloud SQL)

- Cloud SQL は自動バックアップとポイントインタイムリカバリ (PITR) を提供します。
- バックアップ設定を確認し、リストア手順を把握しておきます。
- 定期的にリストアテストを実施することを推奨します。

---
*このドキュメントは一般的なガイドラインです。実際の運用に合わせて詳細を追記・修正してください。*
*特に、具体的な監視クエリ、アラート閾値、障害復旧コマンドなどはプロジェクト固有のものを整備する必要があります。*
*DoD: Staging healthcheck PASS (このドキュメントの記述完了後、ステージング環境でのヘルスチェック成功を確認)*
