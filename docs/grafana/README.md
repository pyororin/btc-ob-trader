# `grafana` サービス 詳細設計書

## 1. サービスの概要

`grafana`サービスは、`timescaledb`に格納されたデータを可視化するためのダッシュボードを提供します。公式の`grafana/grafana-oss`イメージを利用しており、取引ボットのパフォーマンスをリアルタイムで監視し、分析するためのユーザーインターフェースとなります。

Grafanaのプロビジョニング機能により、コンテナ起動時にデータソースとダッシュボードが自動的に設定されるため、手動でのセットアップは不要です。

## 2. Docker Compose上の役割と設定

`docker-compose.yml`における`grafana`サービスの定義は以下の通りです。

```yaml
services:
  grafana:
    image: grafana/grafana-oss:latest
    container_name: grafana-obi
    ports:
      - "3000:3000"
    env_file:
      - .env
    environment:
      - GF_SECURITY_ADMIN_USER=${GRAFANA_USER:-admin}
      - GF_SECURITY_ADMIN_PASSWORD=${GRAFANA_PASSWORD:-admin}
      - GF_PATHS_PROVISIONING=/etc/grafana/provisioning
    volumes:
      - ./grafana/provisioning/datasources:/etc/grafana/provisioning/datasources:ro
      - ./grafana/provisioning/dashboards:/etc/grafana/provisioning/dashboards:ro
      - ./grafana/dashboards:/var/lib/grafana/dashboards:ro
      - grafana_data:/var/lib/grafana
    depends_on:
      - timescaledb
    restart: unless-stopped
    networks:
      - bot_network
```

### 設定解説

-   **`ports`**: GrafanaのWebインターフェースが提供されるポート`3000`をホストマシンに公開します。ブラウザで`http://localhost:3000`にアクセスすることでダッシュボードを閲覧できます。
-   **`environment`**: 管理者ユーザーのユーザー名とパスワードを環境変数経由で設定します。
-   **`volumes`**:
    -   `./grafana/provisioning/datasources` -> `/etc/grafana/provisioning/datasources`: データソースの自動設定（プロビジョニング）ファイルをマウントします。
    -   `./grafana/provisioning/dashboards` -> `/etc/grafana/provisioning/dashboards`: ダッシュボードのプロビジョニング設定ファイルをマウントします。
    -   `./grafana/dashboards` -> `/var/lib/grafana/dashboards`: ダッシュボードの定義本体であるJSONファイルをマウントします。
    -   `grafana_data:/var/lib/grafana`: Grafana自体の設定やユーザーが作成したダッシュボードなどを永続化するためのボリュームです。
-   **`depends_on`**: `timescaledb`サービスが利用可能になってからGrafanaを起動するように設定されています。

## 3. プロビジョニングされるリソース

### 3.1. データソース (`datasource.yml`)

-   **名前**: `TimescaleDB`
-   **タイプ**: `postgres`
-   **接続先**: `timescaledb:5432` (Dockerネットワーク内の`timescaledb`サービス)
-   **認証情報**: 環境変数 `${DB_USER}` と `${DB_PASSWORD}` を使用。
-   **特徴**: `timescaledb: true`オプションが有効になっており、TimescaleDBの時系列関数を最適に利用できます。

### 3.2. ダッシュボード (`dashboards.yml` と `*.json`)

`./grafana/dashboards`ディレクトリ内のJSONファイルが、ダッシュボードとして自動的に読み込まれます。

#### a. 損益レポート (`pnl_dashboard.json`)

-   **目的**: ボットの全体的なパフォーマンス指標（KPI）を一覧表示します。
-   **主要パネル**:
    -   **累積損益グラフ**: `trades_pnl`テーブルから取得したデータで、時間の経過に伴う損益の推移を視覚化します。
    -   **KPI統計パネル**: `report-generator`が生成した`pnl_reports`テーブルの最新データを表示します。
        -   合計損益
        -   勝率
        -   リスクリワードレシオ
        -   最大ドローダウン
        -   プロフィットファクター など
-   **データソース**: 主に`report-generator`によって集計済みの`pnl_reports`テーブルと、`trades_pnl`テーブル。これにより、ダッシュボード表示時のクエリ負荷が軽減されています。

#### b. Performance vs. Benchmark (`bench_dashboard.json`)

-   **目的**: ボットのパフォーマンスを、市場の単純な「買い持ち戦略」と比較評価します。
-   **主要パネル**:
    -   **PnL vs. Benchmarkグラフ**: ボットの累積損益と、正規化された市場価格（ベンチマーク）を一つのグラフに重ねて表示します。これにより、ボットが市場平均を上回っているかどうかが一目でわかります。
    -   **Performance Alphaグラフ**: ボットの損益とベンチマークの差分（アルファ）を時系列で表示します。アルファがプラスであれば、ボットが市場平均を上回るリターンを生み出していることを意味します。
-   **データソース**: `timescaledb`に定義された`v_performance_vs_benchmark`ビュー。このビューが事前にPnLとベンチマークのデータを結合・計算しているため、Grafana側でのクエリがシンプルになっています。

## 4. 設計思想と考慮点

-   **Infrastructure as Code (IaC)**: データソースやダッシュボードの定義をコード（YAML/JSON）としてバージョン管理することで、誰でも同じ監視環境を再現できるようになっています。手動での設定作業を排除し、一貫性と効率性を高めています。
-   **責務の分離**: `report-generator`が集計処理を行い、`grafana`は可視化に専念するという役割分担が明確です。これにより、ダッシュボードの表示速度が向上し、`timescaledb`への負荷も軽減されます。
-   **ユーザー中心の設計**: 2種類のダッシュボードを用意することで、異なる視点からの分析を可能にしています。「損益レポート」はボット自体の絶対的なパフォーマンスを評価するのに役立ち、「Performance vs. Benchmark」は市場全体との比較でパフォーマンスを相対的に評価するのに役立ちます。

## 5. 想定ユースケース

-   **リアルタイム監視**: ライブ取引中に`http://localhost:3000`にアクセスし、ボットの損益やポジションの状況をリアルタイムで把握する。
-   **パフォーマンスレビュー**: 定期的にダッシュボードを確認し、勝率やドローダウンなどの指標から戦略の健全性を評価する。
-   **戦略改善のインサイト**: ベンチマークとの比較を通じて、現在の戦略がどのような市場環境で強く、どのような環境で弱いのかといったインサイトを得る。
