# ローカル開発環境 詳細手順 (OPERATIONS-local.md)

このドキュメントは、`obi-scalp-bot` のローカル開発環境を Windows 11 + WSL2 (Windows Subsystem for Linux 2) 環境に構築するための詳細な手順、ヒント、FAQを提供します。`README.md` のクイックスタートを補完するものです。

## 1. 前提となる知識・環境

- Windows 11 Pro/Home
- WSL2 が有効化されていること ([Microsoft公式ドキュメント](https://learn.microsoft.com/ja-jp/windows/wsl/install) 参照)
- Ubuntu (または他のLinuxディストリビューション) がWSL2上にインストール済みであること
- Docker Desktop が Windows にインストールされ、WSL2統合が有効になっていること
- Git が Windows または WSL2 環境にインストールされていること
- Go 1.22+ が WSL2 環境にインストールされていること (ローカルでのビルドやテストのため)
- VS Code (推奨) と Remote - WSL 拡張機能

## 2. WSL2 環境のセットアップと最適化

### 2.1. WSL2 のインストールとディストリビューションの選択

1.  管理者として PowerShell を開き、以下を実行してWSLをインストール (まだの場合):
    ```powershell
    wsl --install
    ```
    これにより、デフォルトで Ubuntu がインストールされます。特定のディストリビューションをインストールしたい場合は、`wsl --list --online` で利用可能なものを確認し、`wsl --install -d <DistributionName>` でインストールします。

2.  WSL2 をデフォルトバージョンに設定:
    ```powershell
    wsl --set-default-version 2
    ```

### 2.2. Docker Desktop WSL2 統合

1.  Docker Desktop を起動します。
2.  Settings > Resources > WSL Integration に移動します。
3.  "Enable integration with my default WSL distro" がオンになっていることを確認します。
4.  プロジェクトで使用するWSL2ディストリビューション (例: Ubuntu) のトグルスイッチもオンにします。
5.  変更を適用します。

これにより、WSL2ターミナル内から `docker` および `docker-compose` コマンドが利用可能になります。

### 2.3. Go 環境のセットアップ (WSL2内)

1.  WSL2ターミナル (例: Ubuntu) を開きます。
2.  [Go公式サイト](https://golang.org/dl/) から最新のLinux用Goアーカイブをダウンロードし、インストールします。
    ```bash
    # 例 (バージョンは適宜最新のものに置き換えてください)
    wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
    sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
    rm go1.22.0.linux-amd64.tar.gz
    ```
3.  `~/.bashrc` (または `~/.zshrc` など、使用しているシェルに応じて) にGoのパスを追加します。
    ```bash
    export PATH=$PATH:/usr/local/go/bin
    export GOPATH=$HOME/go
    export PATH=$PATH:$GOPATH/bin
    ```
    シェル設定ファイルを再読み込みします: `source ~/.bashrc`
4.  インストールを確認: `go version`

### 2.4. Git のセットアップ

- **Windows側でGitを使用する場合**: Windows 用 Git をインストールし、PATHを通します。WSL2内からも `git.exe` としてアクセス可能です。
- **WSL2側でGitを使用する場合 (推奨)**: WSL2ディストリビューション内で `sudo apt update && sudo apt install git` 等でGitをインストールします。

**SSHキーの設定**: GitHub等との連携のためにSSHキーを設定することを強く推奨します。Windows側で生成したキーをWSL2から利用するか、WSL2内で新たにキーを生成します。

**認証情報ヘルパー**: `git config --global credential.helper "/mnt/c/Program\ Files/Git/mingw64/libexec/git-core/git-credential-manager.exe"` のように設定すると、Windowsの認証情報マネージャーを利用できます（Git for Windows をインストールしている場合）。

## 3. プロジェクトのセットアップ

1.  **WSL2ターミナルを開きます。**
2.  **プロジェクトをクローンするディレクトリに移動します。**
    WSL2ファイルシステム内 (`/home/username/projects` など) にクローンすることを推奨します。Windowsファイルシステム (`/mnt/c/Users/...`) よりもパフォーマンスが大幅に向上します。
    ```bash
    mkdir -p ~/projects
    cd ~/projects
    ```
3.  **リポジトリをクローンします。**
    ```bash
    git clone https://github.com/your-org/obi-scalp-bot.git
    cd obi-scalp-bot
    ```
4.  **VS Code で開く (推奨)**
    WSL2ターミナルでプロジェクトディレクトリにいる状態で、以下を実行します。
    ```bash
    code .
    ```
    これにより、VS Code がWSL2に接続された状態で開きます。

5.  **環境設定ファイル `.env` を作成します。**
    `README.md` の指示に従い、`.env.sample` をコピーして `.env` を作成し、APIキー等を設定します。
    ```bash
    cp .env.sample .env
    # nano .env や VS Codeで編集
    ```

## 4. Botの起動とMakefileコマンド

プロジェクトの操作には `Makefile` に定義されたコマンドを使用します。これにより、長くなりがちな `docker-compose` コマンドを簡略化し、意図を明確にできます。

### コマンドの役割分担

意図しない取引の開始を防ぐため、コマンドの役割が明確に分かれています。

- `make up`: **取引Botを含む**すべてのサービス（Bot, DB, Grafana）を起動します。実際の取引を開始する場合に使用します。
- `make monitor`: **取引Botを除く**、モニタリング関連のサービス（DB, Grafana）のみを起動します。リプレイ結果の確認や、過去のデータを分析する際に使用します。
- `make replay`: 過去のデータを用いてバックテストを実行します。内部的に `make monitor` を呼び出し、DBとGrafanaが起動している状態を保証します。
- `make down`: すべてのサービスを停止します。
- `make help`: 利用可能なすべてのコマンドとその説明を表示します。

### 一般的な開発フロー

1.  **リプレイ結果の確認やデータ分析のみを行う場合**:
    ```bash
    # モニタリングサービス (DB, Grafana) を起動
    make monitor
    ```
    その後、ブラウザで `http://localhost:3000` を開いてGrafanaダッシュボードを確認します。

2.  **バックテストを実行する場合**:
    ```bash
    # リプレイを実行（DBとGrafanaも自動で起動します）
    make replay
    ```
    完了後、Grafanaで `TimescaleDB (Replay)` データソースを選択して結果を確認します。

3.  **実際の取引を開始する場合**:
    ```bash
    # Botを含む全サービスを起動
    make up
    ```

## 5. トラブルシューティング / FAQ

### Q1: `make up` や `docker` コマンドがWSL2で見つからない。

**A1:**
- Docker Desktop が起動しているか確認してください。
- Docker Desktop の Settings > Resources > WSL Integration で、使用しているディストリビューションとの統合が有効になっているか確認してください。
- WSL2ターミナルを再起動してみてください。

### Q2: `make up` 実行時にパーミッションエラーが発生する。

**A2:**
- Dockerソケット (`/var/run/docker.sock`) へのアクセス権がない可能性があります。
    - 現在のユーザーを `docker` グループに追加: `sudo usermod -aG docker $USER`。その後、WSLセッションを再起動 (ターミナルを閉じて再度開くか、`wsl --shutdown` 後に再度ディストリビューションを起動)。
- プロジェクトファイルがWindowsファイルシステム (`/mnt/c/...`) にあり、WSL2からアクセスする際のパーミッションメタデータの問題。プロジェクトをWSL2ファイルシステム (`/home/...`) に置くことを推奨します。

### Q3: Botコンテナは起動するが、Coincheck APIに接続できない。

**A3:**
- `.env` ファイルの `COINCHECK_API_KEY` と `COINCHECK_API_SECRET` が正しく設定されているか確認してください。
- ファイアウォールやプロキシが通信をブロックしていないか確認してください (Windows側、ルーターなど)。
- Coincheck APIのステータスを確認してください (メンテナンス中など)。

### Q4: `make replay` が動作しない (または実装されていない)。

**A4:**
- `TASK_MANAGEMENT.md` や `Makefile` を確認し、`replay` 機能の現在の実装ステータスを確認してください。
- `replay` 機能が実装されている場合、必要なデータファイルや設定 (`config-replay.yaml` など) が正しく配置されているか確認してください。
- **DoD (Definition of Done) の「新規 PC で `make replay` 成功」は、この `OPERATIONS-local.md` ドキュメント作成タスクの完了後、ユーザーが実際に `replay` 機能を実装・テストする際の目標となります。**

### Q5: WSL2 のパフォーマンスが悪い (特にディスクI/O)。

**A5:**
- プロジェクトファイルは必ずWSL2ファイルシステム内 (`/home/username/...` など) に配置してください。`/mnt/c/...` 経由でのアクセスは非常に遅くなります。
- WSL2のメモリ割り当てを確認・調整します。Windowsホームディレクトリに `.wslconfig` ファイルを作成して設定できます。
  ```
  # C:\Users\<YourUserName>\.wslconfig
  [wsl2]
  memory=4GB  # 例: 4GBに設定 (物理メモリの半分以下程度が目安)
  processors=2 # 例: 2コアに設定
  ```
  変更後は `wsl --shutdown` を実行し、WSL2を再起動する必要があります。

### Q6: VS Code Remote - WSL 拡張機能がうまく動作しない。

**A6:**
- VS Code と Remote - WSL 拡張機能が最新版であることを確認してください。
- VS Code のコマンドパレット (`Ctrl+Shift+P`) から `WSL: Rebuild VS Code Server` を試してみてください。
- WSL2 インスタンスを再起動 (`wsl --shutdown` 後に再度ディストリビューションを起動)。

## 6. その他ヒント

- **WSL2ターミナルのカスタマイズ**: Windows Terminal を使うと、タブ機能やカスタマイズが容易です。Oh My Zsh や Starship などを導入して、より快適なターミナル環境を構築することも可能です。
- **データベースクライアント**: TimescaleDB (PostgreSQL) に接続するには、DBeaver, pgAdmin, または VS Code の PostgreSQL 拡張機能などを Windows 側または WSL2 側にインストールして使用できます。`docker-compose.yml` でDBのポート (`5432`) がホストに公開されていれば、`localhost:5432` で接続できます。
- **リソース監視**: WindowsのタスクマネージャーでWSL2 (`vmmemWSL` プロセスなど) のリソース使用状況を確認できます。WSL2内では `top` や `htop` コマンドが利用できます。

---
*このドキュメントは、一般的なWSL2環境構築とプロジェクト利用のガイドです。個別の環境差異により追加の調整が必要になる場合があります。*
*DoD: 新規 PC で `make replay` 成功 (このドキュメントの記述完了後、`replay`機能が実装され、ユーザーが新規環境でテストする際の目標)*

## 7. リプレイモード (`make replay`) の詳細

リプレイモードは、過去の市場データ（板情報や取引履歴）をCSVファイルなどから読み込み、あたかもリアルタイムでデータを受信しているかのようにBotを動作させる機能です。これにより、実際の資金を使わずに、特定の期間の市場に対する戦略のパフォーマンスを検証・デバッグできます。

### 7.1. 目的

- **戦略のバックテスト**: 新しいロジックやパラメータが過去のデータに対してどのように機能するかを評価します。
- **バグの再現とデバッグ**: 特定の市場状況で発生した不具合を、同じデータを使って再現し、原因を特定します。
- **パフォーマンスチューニング**: パラメータ（TP/SL、OBI閾値など）を変更しながら複数回リプレイを実行し、最適な組み合わせを探します。

### 7.2. 前提条件

1.  **リプレイ用データの準備**:
    - リプレイに使用する過去データが `fixtures/` ディレクトリなどに配置されている必要があります。
    - データ形式は、リプレイ機能が読み込める形式（例: `trades.csv`, `orderbook.csv`）に準拠している必要があります。
    - データにはタイムスタンプが含まれており、時系列に沿って処理されます。

2.  **リプレイ用設定ファイルの確認**:
    - `config/config-replay.yaml` ファイルがリプレイの挙動を制御します。
    - この設定ファイルで、使用する戦略パラメータや、結果を保存するデータベース名を指定します。本番用とは別のデータベース (`obi_scalp_bot_db_replay` など) を指定することが強く推奨されます。

### 7.3. 実行手順

1.  **WSL2ターミナルを開き、プロジェクトのルートディレクトリに移動します。**
    ```bash
    cd ~/projects/obi-scalp-bot
    ```

2.  **環境をクリーンな状態にします (推奨)。**
    現在起動中のコンテナがある場合は、一度停止・削除します。
    ```bash
    make down
    ```

3.  **リプレイコマンドを実行します。**
    ```bash
    make replay
    ```
    このコマンドは、`docker-compose.yml` に定義された `replay` 用のサービス（またはコマンド）を実行します。通常、`bot` サービスを `config-replay.yaml` を読み込む設定で起動し、リプレイ用のエントリポイントスクリプトを実行するよう設定されています。

4.  **ログを確認します。**
    リプレイが開始されると、ターミナルにBotの動作ログがリアルタイムで出力されます。
    - シグナルの発生
    - 仮想的な注文の発注・約定
    - PnL (損益) の計算
    - 処理の進捗状況
    などがログから確認できます。

### 7.4. 結果の確認方法

リプレイが完了した後、結果は `config-replay.yaml` で指定されたデータベースに保存されています。

1.  **データベースに接続します。**
    DBeaver や pgAdmin などのGUIツール、または `psql` コマンドラインツールを使用して、リプレイ用のデータベースに接続します。
    - **ホスト**: `localhost`
    - **ポート**: `5432` (または `.env` で指定した `DB_PORT`)
    - **データベース名**: `obi_scalp_bot_db_replay` (または `config-replay.yaml` で指定した名前)
    - **ユーザー/パスワード**: `.env` で指定した値

2.  **結果をクエリで確認します。**
    - `trades` テーブルや `pnl_history` テーブルなどをSELECT文で照会し、リプレイ中の取引履歴や損益の推移を確認します。
    - 例:
      ```sql
      -- 仮想的な取引履歴を確認
      SELECT * FROM trades ORDER BY created_at;

      -- 損益の推移を確認
      SELECT * FROM pnl_history ORDER BY timestamp;
      ```

3.  **Grafana などで可視化 (オプション)**
    もし `docker-compose.yml` にGrafanaサービスが定義されており、リプレイ用データベースをデータソースとして設定していれば、ダッシュボードでリプレイ結果を視覚的に分析することも可能です。

## 8. パフォーマンス可視化 (Grafana)

プロジェクトには、Botのパフォーマンス（特に損益）を視覚的に監視するための Grafana ダッシュボードが含まれています。

### 8.1. Grafanaへのアクセス

1.  **モニタリングサービスを起動します。**
    WSL2ターミナルで、プロジェクトのルートディレクトリから以下のコマンドを実行します。
    ```bash
    make monitor
    ```
    これにより、取引Botを起動せずに、`timescaledb` と `grafana` サービスが安全に起動します。
    実際の取引を開始したい場合は `make up` を使用します。

2.  **ブラウザでGrafanaを開きます。**
    Webブラウザで以下のURLにアクセスします。
    - **URL**: `http://localhost:3000`

3.  **ログインします。**
    - **ユーザー名**: `admin`
    - **パスワード**: `admin`
    (初回ログイン時にパスワードの変更を求められる場合があります)

### 8.2. ダッシュボードの使い方

1.  **ダッシュボードを開く**:
    ログイン後、左側のメニューから `Dashboards` > `Browse` を選択し、「OBI Scalp Bot PnL」という名前のダッシュボードをクリックします。

2.  **データソースの切り替え**:
    ダッシュボードの左上には `Datasource` というドロップダウンメニューがあります。ここで、監視したいデータソースを選択できます。
    - `TimescaleDB (Production)`: `make up` で起動している本番用Botのパフォーマンスを表示します。
    - `TimescaleDB (Replay)`: `make replay` を実行した後のバックテスト結果を表示します。

3.  **表示パネルの説明**:
    - **Cumulative PnL Over Time**: 累積損益の推移を時系列グラフで表示します。Botの全体的なパフォーマンスを一目で確認できます。
    - **Total Trades**: 総取引回数を表示します。
    - **Win Rate**: 勝率（利益が出た取引の割合）を表示します。
    - **Total PnL**: 全期間の合計損益を表示します。
    - **Sharpe Ratio (placeholder)**: シャープレシオ（リスク調整後リターン）を表示します。現在はプレースホルダーであり、正確な計算ロジックの実装が必要です。
    - **Recent Trades**: 直近の取引履歴をテーブル形式で表示します。

4.  **時間範囲の調整**:
    ダッシュボードの右上にある時間範囲セレクター（例: `Last 6 hours`）をクリックすることで、表示するデータの期間を自由に変更できます。
