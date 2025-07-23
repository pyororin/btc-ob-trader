# 本番運用手順 (OPERATIONS.md)

このドキュメントは、`obi-scalp-bot` をDocker Composeを利用して単一サーバー上で本番運用するための手順、監視、標準的な運用手順 (SOP) を記述します。

## 1. アーキテクチャ概要

本システムは、`docker-compose.yml` で定義された複数のサービスが連携して動作します。主要なコンポーネントは以下の通りです。

-   **アプリケーション**: `bot` (Go), `optimizer` (Python), `drift-monitor` (Python)
-   **データベース**: `timescaledb` (PostgreSQL + TimescaleDB)
-   **監視・管理**: `grafana`, `adminer`

詳細は `README.md` のアーキテクチャ図とサービス概要を参照してください。

## 2. 前提条件とサーバー設定

-   **OS**: Ubuntu 22.04 LTSなどの安定したLinuxディストリビューション。
-   **ソフトウェア**:
    -   `git`
    -   `make`
    -   `docker`
    -   `docker-compose`
-   **推奨設定**:
    -   運用ユーザーを`docker`グループに追加し、`sudo`なしでDockerコマンドを実行できるようにしてください。
      ```bash
      sudo usermod -aG docker $USER
      ```
      (実行後、再ログインが必要です)
    -   システムのファイアウォールを設定し、Grafana (3000), Adminer (8888) などのポートへのアクセスを信頼できるIPアドレスのみに制限してください。

## 3. 初回デプロイ手順

1.  **リポジトリのクローン**:
    ```bash
    git clone https://github.com/your-org/obi-scalp-bot.git
    cd obi-scalp-bot
    ```

2.  **環境変数の設定**:
    `.env.sample`をコピーして`.env`を作成し、APIキーやデータベースの認証情報を設定します。
    ```bash
    cp .env.sample .env
    nano .env
    ```

3.  **設定ファイルの確認**:
    `config/app_config.yaml` と `config/trade_config.yaml.template` の内容を確認し、必要に応じて調整します。特に、`trade_config.yaml.template` は自動最適化の際のパラメータ範囲を定義するため重要です。

4.  **Dockerイメージのビルド**:
    初回起動前、またはアプリケーションのコードを更新した際に実行します。
    ```bash
    make build
    ```

5.  **初回起動**:
    データベースの初期化なども含め、すべてのサービスをバックグラウンドで起動します。
    ```bash
    make up
    ```

6.  **起動確認**:
    ```bash
    # すべてのコンテナが "Up" または "healthy" 状態であることを確認
    docker-compose ps -a

    # ボットのログを確認し、エラーなく起動していることを確認
    make logs s=bot
    ```

## 4. 日常運用

### 4.1. サービスの操作

基本的なサービスの操作（起動、停止、ログ確認など）は `Makefile` に集約されています。詳細は `README.md` の「実行コマンド (Makefile)」セクションを参照してください。

-   **状態確認**: 全サービスの稼働状態、ポート、ヘルスチェック状況を確認するには、以下のコマンドを実行します。
    ```bash
    docker-compose ps -a
    ```

-   **単体サービスの再起動**: 特定のサービス（例: `bot`）だけを再起動したい場合は、以下のコマンドが便利です。
    ```bash
    docker-compose restart bot
    ```

### 4.2. パフォーマンス監視 (Grafana)

**URL**: `http://<サーバーIP>:3000`

Grafanaはシステムの健全性とパフォーマンスを監視するための主要ツールです。

-   **確認すべきダッシュボード**:
    -   **PNL Dashboard**: 累積損益、勝率、ドローダウンなど、ボットの経済的パフォーマンスを評価します。
    -   **Latency Dashboard**: 注文の遅延などを監視し、システムの応答性能を確認します。
    -   **Optimizer Dashboard**: パラメータ最適化の履歴やパフォーマンスを確認します。
-   **監視のポイント**:
    -   累積損益が予期せず下降トレンドになっていないか？
    -   勝率やプロフィットファクターが時間とともに悪化していないか？ (ドリフトの兆候)
    -   レイテンシーが異常に増加していないか？

### 4.3. データ確認 (Adminer)

**URL**: `http://<サーバーIP>:8888`

緊急時や詳細な調査が必要な場合に、データベースの内容を直接確認・操作します。
接続情報は `README.md` を参照してください。

## 5. パラメータ最適化の運用

`drift-monitor`と`optimizer`サービスが連携し、取引パラメータを自律的に更新します。

-   **自動フロー**:
    1. `drift-monitor`が定期的に`bot`のパフォーマンスをDBから評価します。
    2. パフォーマンスの低下（ドリフト）や市場の急変動を検知すると、`optimizer`の実行をトリガーします。
    3. `optimizer`が過去データを元に最適なパラメータを計算し、検証後に`/data/params/trade_config.yaml`を更新します。
    4. `bot`は設定ファイルの変更を検知し、自動で新しいパラメータをリロードします。

-   **手動介入**:
    市場の大きな変動の後など、能動的に最適化を実行したい場合は、以下のコマンドを使用します。
    ```bash
    make optimize
    ```
    実行後、`make logs s=optimizer`で進捗を確認できます。

-   **設定ファイルの管理**:
    `optimizer`によって更新される`/data/params/trade_config.yaml`は、Dockerボリューム(`params_volume`)内に保存されています。重要なファイルなので、定期的にバックアップすることを推奨します。

## 6. 緊急時対応手順 (SOP)

#### シナリオ1: システム全体が不安定、または意図しない動作をしている

1.  **全サービスを即時停止**:
    ```bash
    make down
    ```
2.  **原因調査**:
    `make logs`で各サービスのログを確認し、エラーの原因を特定します。特に`bot`, `optimizer`, `timescaledb`のログに注目します。
3.  **対応**:
    -   **設定ミスの場合**: `.env`や`config/*.yaml`を修正します。
    -   **データ破損の疑いがある場合**: `Adminer`や`psql`でDBの状態を確認します。必要であればバックアップからリストアします。
    -   **コードのバグの場合**: 修正パッチを適用し、`make build`でイメージを再ビルドします。
4.  **再起動**:
    原因を解決した後、`make up`でサービスを再起動し、`make logs`で正常に動作することを確認します。

#### シナリオ2: 過大な損失が発生している

1.  **全サービスを即時停止**:
    ```bash
    make down
    ```
2.  **ポジションの手動クローズ**:
    取引所のWebサイトにログインし、残っているポジションを手動で決済します。
3.  **原因分析**:
    -   `make monitor`でGrafanaとDBのみを起動します。
    -   Grafanaダッシュボードで問題の取引履歴を特定し、市場チャートと照らし合わせて原因（ロジックの欠陥、パラメータ不適合、市場の異常な動きなど）を分析します。
4.  **修正とバックテスト**:
    原因を特定し、コードや設定を修正します。修正後は、必ず**ローカル環境**で十分なシミュレーション(`make simulate`)を行い、安全性を確認してから本番環境にデプロイします。

## 7. バックアップとリストア

### 7.1. バックアップ対象

-   **データベース**: `timescaledb_data` Dockerボリューム。最も重要なデータです。
-   **パラメータ設定**: `params_volume` Dockerボリューム。`optimizer`が生成した`trade_config.yaml`が保存されています。
-   **設定ファイル**: ` .env`, `config/` ディレクトリ。Gitで管理されていますが、` .env`は別途バックアップが必要です。

### 7.2. バックアップ手順 (例)

`docker-compose down`でサービスを停止してから実行することを推奨します。

```bash
# データベースボリュームのバックアップ
docker run --rm -v timescaledb_data:/data -v $(pwd)/backups:/backup ubuntu tar cvf /backup/timescaledb_data_$(date +%Y%m%d).tar /data

# パラメータボリュームのバックアップ
docker run --rm -v params_volume:/data -v $(pwd)/backups:/backup ubuntu tar cvf /backup/params_volume_$(date +%Y%m%d).tar /data

# .envファイルのバックアップ
cp .env ./backups/.env_$(date +%Y%m%d)
```
バックアップファイルは、サーバー外の安全な場所に保管してください。

### 7.3. リストア手順 (例)

新しいサーバーやクリーンな状態で復元する場合の手順です。

1.  リポジトリをクローンし、`.env`ファイルを復元します。
2.  `docker-compose up -d timescaledb` などでボリュームを初回作成します。
3.  `docker-compose down`で一度停止します。
4.  バックアップファイルからデータをボリュームに展開します。
    ```bash
    # データベースボリュームのリストア
    docker run --rm -v timescaledb_data:/data -v $(pwd)/backups:/backup ubuntu bash -c "cd /data && tar xvf /backup/timescaledb_data_YYYYMMDD.tar --strip 1"

    # パラメータボリュームのリストア
    docker run --rm -v params_volume:/data -v $(pwd)/backups:/backup ubuntu bash -c "cd /data && tar xvf /backup/params_volume_YYYYMMDD.tar --strip 1"
    ```
5.  `make up`で全サービスを起動します。

## 8. アップデート手順

1.  **最新のコードを取得**:
    ```bash
    git pull origin main
    ```
2.  **（必要に応じて）依存関係の更新**:
    `go.mod`, `requirements.txt` に変更があった場合は、ビルド前に反映させます。
3.  **Dockerイメージの再ビルド**:
    ```bash
    make build
    ```
4.  **サービスの再起動**:
    `make up`コマンドは、イメージが更新されているサービスのみを再作成して起動します。
    ```bash
    make up
    ```
5.  **動作確認**:
    `make ps`と`make logs`で、すべてのサービスが正常に新しいバージョンで起動していることを確認します。
