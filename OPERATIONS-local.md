# ローカル開発環境構築手順 (Windows 11 + WSL2) (OPERATIONS-local.md)

このドキュメントは、Windows 11 上の WSL2 (Ubuntu) 環境で `obi-scalp-bot` の開発を行うための詳細な手順、コマンド例、FAQ を提供します。

## 1. はじめに

- **対象読者**: Windows 11 環境で本Botの開発に参加する開発者
- **目的**: スムーズなローカル開発環境のセットアップと、一般的な問題の解決策の提供
- **標準環境**:
    - OS: Windows 11 Pro/Home
    - WSL2: Ubuntu (最新LTS推奨)
    - Docker: Docker Desktop for Windows (WSL2 Integration有効)
    - Go: 1.22+ (WSL2 Ubuntu内にインストール)
    - Git: (Windows側、またはWSL2 Ubuntu内のどちらでも可。WSL2内を推奨)
    - エディタ: VS Code (Remote - WSL拡張機能の使用を強く推奨)

## 2. 環境構築手順

### 2.1. WSL2 と Ubuntu のインストール

1.  **WSL2 の有効化**:
    - PowerShell を管理者として開き、以下を実行:
      ```powershell
      dism.exe /online /enable-feature /featurename:Microsoft-Windows-Subsystem-Linux /all /norestart
      dism.exe /online /enable-feature /featurename:VirtualMachinePlatform /all /norestart
      ```
    - PCを再起動します。
    - [Linux カーネル更新プログラム パッケージ](https://learn.microsoft.com/ja-jp/windows/wsl/install-manual#step-4---download-the-linux-kernel-update-package) をダウンロードしてインストールします。
    - PowerShell で以下を実行し、WSL2 をデフォルトバージョンに設定:
      ```powershell
      wsl --set-default-version 2
      ```

2.  **Ubuntu のインストール**:
    - Microsoft Store を開き、「Ubuntu」を検索してインストール (最新のLTS版を推奨)。
    - 初回起動時にユーザー名とパスワードを設定します。

### 2.2. Docker Desktop のインストールと設定

1.  [Docker Desktop for Windows](https://www.docker.com/products/docker-desktop/) をダウンロードし、インストールします。
2.  Docker Desktop の設定 (Settings) を開きます。
    - **General**: "Use the WSL 2 based engine" がチェックされていることを確認 (デフォルト)。
    - **Resources > WSL Integration**: "Enable integration with my default WSL distro" がオンになっていること、およびインストールした Ubuntu ディストリビューション (例: `Ubuntu`) が "Enable integration with additional distros..." でオンになっていることを確認します。
    - 変更を適用 (Apply & Restart)。

### 2.3. 開発ツールのインストール (WSL2 Ubuntu内)

WSL2 Ubuntuターミナルを開いて作業します。

1.  **Git のインストール**:
    ```bash
    sudo apt update
    sudo apt install git -y
    ```
    Gitの初期設定 (ユーザー名、メールアドレス) も行います。
    ```bash
    git config --global user.name "Your Name"
    git config --global user.email "you@example.com"
    ```

2.  **Go 1.22+ のインストール**:
    公式サイト ([https://go.dev/dl/](https://go.dev/dl/)) から最新のLinux用アーカイブをダウンロードし、インストールします。
    ```bash
    # 例 (バージョンは適宜最新のものに置き換えてください)
    wget https://go.dev/dl/go1.22.0.linux-amd64.tar.gz
    sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.22.0.linux-amd64.tar.gz
    rm go1.22.0.linux-amd64.tar.gz
    ```
    パスを設定します。`.bashrc` や `.zshrc` に以下を追記します。
    ```bash
    echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc # (または .zshrc)
    source ~/.bashrc # (または .zshrc)
    ```
    インストールを確認:
    ```bash
    go version
    ```

3.  **VS Code と Remote - WSL 拡張機能 (推奨)**:
    - Windows側に [VS Code](https://code.visualstudio.com/) をインストールします。
    - VS Code を起動し、拡張機能マーケットプレイスから「Remote - WSL」をインストールします。
    - これにより、WSL内のプロジェクトをWindows側のVS Codeで直接開いて編集・デバッグできます。

### 2.4. プロジェクトのセットアップ

WSL2 Ubuntuターミナル内で作業します。

1.  **リポジトリをクローン:**
    Windowsのファイルシステム (`/mnt/c/Users/...`) ではなく、WSL2のLinuxファイルシステム内 (例: `~/projects`) にクローンすることを推奨します (パフォーマンス向上のため)。
    ```bash
    mkdir -p ~/projects
    cd ~/projects
    git clone https://github.com/your-org/obi-scalp-bot.git
    cd obi-scalp-bot
    ```
    VS Code で開く場合: WSL2ターミナルで `obi-scalp-bot` ディレクトリに移動し、`code .` と入力すると、VS Code がWSLモードで開きます。

2.  **環境設定ファイルを作成:**
    ```bash
    cp .env.sample .env
    ```
    エディタ (VS Codeなど) で `.env` を開き、CoincheckのAPIキーとシークレットを追記します。
    ```dotenv
    COINCHECK_API_KEY="YOUR_API_KEY"
    COINCHECK_API_SECRET="YOUR_API_SECRET"
    ```

## 3. Bot の起動と操作 (WSL2 Ubuntu内)

プロジェクトルートディレクトリ (`~/projects/obi-scalp-bot`) で以下の `make` コマンドを実行します。

-   **Botを起動:**
    ```bash
    make up
    ```
    Docker Desktopがバックグラウンドでコンテナをビルド・起動します。

-   **ログを確認:**
    ```bash
    make logs
    ```

-   **Botを停止:**
    ```bash
    make down
    ```

-   **コンテナ内でシェルを起動 (デバッグ用):**
    ```bash
    make shell
    ```

-   **クリーンアップ (コンテナとボリュームを削除):**
    ```bash
    make clean
    ```

-   **利用可能な全コマンド表示:**
    ```bash
    make help
    ```

## 4. WSL2 特有の注意点とTips

-   **ファイルシステムパフォーマンス**:
    - WSL2のLinuxファイルシステム (例: `~/projects`) 内でGitリポジトリやソースコードを扱う方が、Windowsファイルシステム (`/mnt/c/...`) 経由よりもI/Oパフォーマンスが大幅に向上します。Dockerのボリュームマウントも同様です。
-   **リソース消費**:
    - Docker Desktop および WSL2 はそれなりにメモリを消費します。Windowsタスクマネージャーで `vmmem` プロセスのメモリ使用量を確認できます。
    - WSL2のメモリ上限を設定したい場合は、Windowsのユーザーディレクトリ (例: `C:\Users\YourUser`) に `.wslconfig` ファイルを作成し、以下のように記述できます (例: 8GBに制限)。
      ```ini
      [wsl2]
      memory=8GB
      # processors=4 # CPUコア数も指定可能
      swap=0
      localhostForwarding=true
      ```
      設定変更後はWSLを再起動 (`wsl --shutdown` をPowerShellで実行後、再度Ubuntuターミナルを開く) が必要です。
-   **ポートフォワーディング**:
    - WSL2内で起動したサービス (例: Dockerコンテナが公開するポート) は、通常 `localhost` 経由でWindows側のブラウザやツールからアクセスできます。
    - `localhost:3000` (Grafana)、`localhost:8080/healthz` (Botヘルスチェック) など。

## 5. FAQ (よくある質問)

-   **Q1: `make up` すると Docker関連のエラーが出る。**
    -   A1: Docker Desktopが起動しているか、WSL2 Integrationが正しく設定されているか確認してください (上記2.2節)。WSL2 Ubuntuターミナルで `docker ps` コマンドがエラーなく実行できるか確認します。
-   **Q2: VS Code で WSL内のプロジェクトを開けない。**
    -   A2: 「Remote - WSL」拡張機能がインストールされているか確認してください。WSL2 Ubuntuターミナルから `code .` で開くのが確実です。
-   **Q3: `git clone` が遅い、または失敗する。**
    -   A3: WSL2内のネットワーク設定やDNS設定が影響している可能性があります。一般的なトラブルシューティングとして、WSL2の再起動 (`wsl --shutdown`) や、Ubuntu内のDNS設定 (`/etc/resolv.conf`) の確認を試みてください。
-   **Q4: Botは起動したが、Coincheckに接続できていないようだ。**
    -   A4: `.env` ファイルのAPIキー/シークレットが正しいか、有効な権限を持っているか確認してください。`make logs` で詳細なエラーメッセージを確認します。
-   **Q5: `make replay` コマンドは何をするのか？ (現状未実装)**
    -   A5: 将来的に、過去のティックデータを読み込ませてBotのロジックを検証 (バックテスト) するためのコマンドです。現時点では実装されていません。

---
*このドキュメントは、Windows 11 + WSL2環境での開発を円滑に進めるためのものです。問題が発生した場合は、このドキュメントを更新・拡充してください。*
