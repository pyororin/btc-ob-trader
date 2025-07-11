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

`README.md` に記載されている `make` コマンド (`make up`, `make logs`, `make down` など) は、WSL2ターミナル内のプロジェクトルートディレクトリで実行します。

### `make up` の仕組み (WSL2 + Docker Desktop)

- `make up` を実行すると、WSL2内の `docker-compose` コマンドが呼び出されます。
- Docker Desktop の WSL2 統合により、Windows側で動作している Docker Engine が使用されます。
- コンテナイメージのビルド (Dockerfileに基づく) やコンテナの起動が行われます。
- ソースコードはWSL2ファイルシステム上にあり、これがDockerコンテナにマウントされます (もし `docker-compose.yml` でボリュームマウントが設定されていれば)。

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
