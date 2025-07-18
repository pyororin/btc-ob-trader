# ローカル開発環境 詳細手順 (OPERATIONS-local.md)

このドキュメントは、`obi-scalp-bot` のローカル開発環境を **Windows 11 + WSL2 (Windows Subsystem for Linux 2)** 環境に構築し、効率的な開発サイクル（特にバックテスト）を回すための詳細な手順、ヒント、FAQを提供します。`README.md` のクイックスタートを補完するものです。

## 1. 環境構築

`README.md` に記載のセットアップ手順に加え、ローカル開発を円滑に進めるための推奨環境です。

-   **WSL2 と Docker Desktop**: パフォーマンス向上のため、プロジェクトファイルは必ずWSL2ファイルシステム内 (`/home/username/projects` など) に配置してください。Docker Desktop の WSL2 Integration は必須です。
-   **Go**: ローカルで `go test` を実行したり、IDEの補完機能を最大限活用するために、WSL2内にGo言語環境をセットアップしてください。
-   **VS Code**: [Remote - WSL](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-wsl) 拡張機能の利用を強く推奨します。`code .` コマンドでWSL2内のプロジェクトを直接開くことで、快適な開発体験が得られます。
-   **データベースクライアント**: DBeaver, DataGrip, pgAdmin など、PostgreSQLに接続できるGUIクライアントを用意すると、DB内のデータ（取引履歴、指標など）の確認が容易になります。`make monitor` 実行後、`localhost:5432` で接続できます。
-   **Adminer (Web UI)**: 本プロジェクトには、Webブラウザベースの軽量データベース管理ツール「Adminer」が含まれています。`make monitor` 実行後、 http://localhost:8888 にアクセスすることで、DBのテーブル構造やレコードを簡単に確認できます。

## 2. ローカルでの開発・テストサイクル

ローカルでの開発とテストは、**CSVシミュレーションによる高速なバックテスト**を中心に行います。これにより、データベースのセットアップやリセットの手間を省き、取引ロジックの検証とパラメータのチューニングを迅速に行うことができます。

**典型的なワークフロー:**

1.  **バックテスト用データの準備 (初回やデータ更新時に実施)**
    本番環境などで稼働しているBotのデータベースに接続できる場合、`make export-sim-data` コマンドを使用して、テストしたい期間の市場データをCSVとしてエクスポートします。
    ```bash
    # データベースサービス（本番用など）が稼働している前提

    # 例: 直近24時間分のデータをエクスポート
    make export-sim-data HOURS_BEFORE=24

    # 例: 特定の期間（ボラティリティが高かった期間など）を指定してエクスポート
    make export-sim-data START_TIME='2024-07-01 00:00:00' END_TIME='2024-07-01 03:00:00'
    ```
    コマンドが成功すると、`./simulation/` ディレクトリにZIPファイルが生成されます。このデータは、一度生成すれば繰り返し利用できます。

2.  **コードとパラメータの修正**
    -   取引ロジックを改善する場合: `internal/` ディレクトリ配下のGoコードを修正します。
    -   パラメータを調整する場合: `config/trade_config.yaml` の値（OBI閾値、利食い・損切り幅など）を変更します。

3.  **シミュレーションの実行**
    `make simulate` コマンドでバックテストを実行します。`CSV_PATH`には、ステップ1で準備したデータ（ZIPまたはCSVファイル）のパスを指定します。
    ```bash
    make simulate CSV_PATH=./simulation/order_book_updates_...zip
    ```
    このコマンドは、`config/trade_config.yaml` の設定を読み込み、指定された過去データに対して取引シミュレーションを実行します。

4.  **結果の確認と反復**
    シミュレーションが完了すると、コンソールに総損益、勝率、取引回数などのパフォーマンスサマリーが出力されます。

    この結果を評価し、期待通りでなければステップ2に戻ってコードやパラメータを修正し、再度シミュレーションを実行します。この「修正 → 実行 → 確認」のサイクルを高速で繰り返すことが、効率的な開発の鍵となります。

## 3. 過去データの分析 (Grafana)

シミュレーションはコンソールに結果を出力しますが、過去の本番トレードや、別途DBに保存したシミュレーション結果を視覚的に分析したい場合は、Grafanaを利用できます。

```bash
# データベースとGrafanaサービスを起動
make monitor
```
ブラウザで `http://localhost:3000` にアクセスし、ダッシュボードを開くことで、DBに保存されている損益曲線や取引履歴などを確認できます。


## 4. トラブルシューティング / FAQ

### Q1: `make simulate` 実行時に `CSV_PATH is not set` と表示される
**A1:** `make simulate` コマンドには、`CSV_PATH` 引数で入力となるCSVファイルを指定する必要があります。
   ```bash
   # 正しい例
   make simulate CSV_PATH=./simulation/order_book_updates_...csv
   ```
   `ls -l simulation/` を実行して、ファイルが存在することを確認してください。

### Q2: `make export-sim-data` がDB接続エラーになる
**A2:** このコマンドは、稼働中のデータベースからデータをエクスポートする必要があります。
- ローカルのDockerコンテナで稼働させているDB（`make up` または `make monitor` で起動）からエクスポートする場合は、まずコンテナが起動していることを確認してください。
- `.env` ファイルに設定した `DB_USER`, `DB_PASSWORD`, `DB_NAME` が、接続したいデータベースのものと一致しているか確認してください。

### Q3: WSL2 のパフォーマンスが悪い (特にディスクI/O)
**A3:** プロジェクトファイルは必ずWSL2ファイルシステム内 (`/home/username/...` など) に配置してください。Windowsファイルシステム (`/mnt/c/...`) 経由でのアクセスは非常に遅くなります。

### Q4: Grafanaにデータが表示されない
**A4:**
- `make monitor` または `make up` でGrafanaとデータベースのコンテナが起動していることを確認してください。
- Grafanaダッシュボードの右上にある **時間範囲セレクター** が、表示したいデータが含まれる期間（例: "Last 24 hours"）に設定されているか確認してください。

### Q5: `make up` 実行時にパーミッションエラーが発生する
**A5:** Dockerソケット (`/var/run/docker.sock`) へのアクセス権がない可能性があります。現在のユーザーを `docker` グループに追加してください: `sudo usermod -aG docker $USER`。その後、WSLセッションを再起動（ターミナルを閉じて再度開く）する必要があります。

---
*このドキュメントは、一般的なWSL2開発環境を想定しています。個別の環境差異により追加の調整が必要になる場合があります。*
