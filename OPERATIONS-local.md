# ローカル開発環境 詳細手順 (OPERATIONS-local.md)

このドキュメントは、`obi-scalp-bot` のローカル開発環境を構築し、効率的な開発・デバッグサイクルを回すための詳細な手順とヒントを提供します。`README.md` のセットアップ手順を補完するものです。

## 1. 推奨開発環境

-   **OS**: Linux, macOS, または Windows 11 + WSL2
-   **必須ツール**: `git`, `make`, `docker`, `docker-compose`
-   **IDE**: VS Code と [Remote - Containers](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers) 拡張機能の組み合わせを強く推奨します。これにより、Dockerコンテナ内で直接コードを編集・デバッグできます。
-   **Go言語環境**: ローカルマシンにもGoをインストールしておくと、IDEの補完機能やLinterがより快適に動作します。

## 2. 開発ワークフロー

ローカルでの開発は、主に2つのシナリオで行います。

### シナリオA: 取引ロジックの改善 (シミュレーション中心)

取引アルゴリズムやパラメータチューニングなど、コアなロジックの改善に最適です。データベースや外部API接続が不要なため、最も高速な開発サイクルです。

1.  **バックテスト用データの準備**:
    `make export-sim-data` を使い、テストしたい期間の市場データをCSVとしてエクスポートします。データは一度生成すれば繰り返し使えます。
    ```bash
    # (本番DBなどに接続できる環境で) 直近24時間分のデータをエクスポート
    make export-sim-data HOURS_BEFORE=24
    ```

2.  **コードとパラメータの修正**:
    -   取引ロジック: `internal/` 配下のGoコードを修正します。
    -   パラメータ: `config/trade_config.yaml` を変更します。

3.  **シミュレーションの実行**:
    `make simulate` でバックテストを実行します。
    ```bash
    make simulate CSV_PATH=./simulation/order_book_updates_...zip
    ```

4.  **結果の確認と反復**:
    コンソールに出力されるパフォーマンス結果を確認し、期待通りでなければステップ2に戻ります。この「修正 → 実行 → 確認」を繰り返します。

### シナリオB: リアルタイム動作の確認 (Dockerコンテナ中心)

WebSocket接続、API連携、データベース書き込みなど、システム全体の動作を確認しながら開発する場合のワークフローです。

1.  **全サービスを起動**:
    ```bash
    make up
    ```
    このコマンドは、コード変更後に実行すると、変更があったサービスのイメージのみを再ビルドして起動します。

2.  **動作確認とデバッグ**:
    -   **ログの確認**: 別のターミナルを開き、リアルタイムでログを確認します。
        ```bash
        # botサービスのログを追う
        make logs s=bot

        # 全サービスのログを追う
        make logs
        ```
    -   **データベースの確認**: `make monitor` を実行（または`make up`が実行中）の状態で、ブラウザから`http://localhost:8888`にアクセスし、AdminerでDBの状態を確認します。
    -   **パフォーマンスの確認**: `http://localhost:3000` のGrafanaで、PNLや各種指標が意図通りに記録されているか確認します。

3.  **コードの修正**:
    コードを修正し、保存します。

4.  **サービスの再起動と確認**:
    `make up` を再度実行すると、変更が反映されたサービスが再起動します。その後、ステップ2に戻って動作を確認します。

## 3. 開発に便利なTips

### データベースの完全リセット

テストデータが溜まったデータベースを完全に初期化し、クリーンな状態からやり直したい場合に実行します。

```bash
# 1. 全サービスを停止
make down

# 2. データベースのボリュームを削除
# ボリューム名は <プロジェクトディレクトリ名>_timescaledb_data です。
docker volume rm obi-scalp-bot_timescaledb_data

# 3. 再度サービスを起動（DBが再初期化される）
make up
```
**注意**: この操作は元に戻せません。`timescaledb_data`ボリューム内のデータはすべて失われます。

### 特定のサービスのみビルド・再起動する

プロジェクト全体ではなく、`bot`だけなど特定のサービスに関連する変更を行った場合、そのサービスだけを対象にすると時間を節約できます。

```bash
# botサービスのみを再ビルド
docker-compose build bot

# botサービスのみを再起動
docker-compose up -d --no-deps --build bot
```
`--no-deps` は依存関係にあるサービスを再起動しないオプション、`--build` は起動前にイメージをビルドするオプションです。

### Goのユニットテスト実行

ロジックの一部をテストケースで検証したい場合は、`make test` を実行します。

```bash
make test
```

## 4. トラブルシューティング / FAQ

### Q1: `make up` 実行時にパーミッションエラーが出る
**A1:** Dockerソケットへのアクセス権がないことが原因です。実行ユーザーを `docker` グループに追加してください。
```bash
sudo usermod -aG docker $USER
```
実行後、ターミナルを再起動するか、再ログインが必要です。

### Q2: Grafanaにデータが表示されない
**A2:**
- `make up` または `make monitor` でサービスが起動していることを `make ps` で確認してください。
- Grafanaダッシュボード右上の時間範囲が、データのある期間（例: "Last 5 minutes"）に設定されているか確認してください。

### Q3: WSL2環境でパフォーマンスが悪い
**A3:** プロジェクトファイルは必ずWSL2ファイルシステム内 (`/home/username/...`) に置いてください。Windows側 (`/mnt/c/...`) に置くとディスクI/Oが極端に遅くなります。

### Q4: `make simulate` で `CSV_PATH is not set` と表示される
**A4:** `make simulate` コマンドには、入力となるCSVファイルのパスを `CSV_PATH` 引数で渡す必要があります。
```bash
make simulate CSV_PATH=./simulation/order_book_updates_...zip
```
`ls -l simulation/` でファイルが存在することを確認してください。
