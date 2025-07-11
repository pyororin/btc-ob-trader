# 板情報を核とした BTC/JPY スキャルピング Bot (obi-scalp-bot)

Coincheck BTC/JPY のオーダーブックと出来高フローをリアルタイム解析し、**超短期スキャルピング**で高効率に資金を回転させることを目的としたトレーディングボットです。

## 📜 プロジェクト概要

本プロジェクトは、Coincheck Exchange API および WebSocket β版を利用して、以下の戦略に基づいた自動取引を実現します。

- **戦略目標**: Coincheck BTC/JPY のオーダーブックと出来高フローをリアルタイム解析し、超短期スキャルピングで約200万円を高効率に回転させる。
- **実装言語**: Go 1.22+
- **本番環境**: Google Cloud Run + Cloud SQL (PostgreSQL + TimescaleDB) を想定 (コンテナ化前提)

詳細な仕様については、包括仕様書（別途管理）を参照してください。

## 🚀 5分クイックスタート (ローカル開発環境)

ローカルマシンでBotを起動し、動作を確認するための手順です。

### 前提条件

- [Git](https://git-scm.com/)
- [Docker Desktop](https://www.docker.com/products/docker-desktop/) (Docker Engine および Docker Compose v2 を含む)
- Go 1.22+ (ローカルでのビルドやテストに必要。Botの実行自体はDocker内で行われます)

### セットアップと実行

1.  **リポジトリをクローン:**
    ```bash
    git clone https://github.com/your-org/obi-scalp-bot.git
    cd obi-scalp-bot
    ```

2.  **環境設定ファイルを作成:**
    `.env.sample` をコピーして `.env` ファイルを作成し、CoincheckのAPIキーとシークレットを追記します。
    ```bash
    cp .env.sample .env
    ```
    エディタで `.env` を開き、以下の項目を実際の値に置き換えてください。
    ```dotenv
    COINCHECK_API_KEY="YOUR_API_KEY"
    COINCHECK_API_SECRET="YOUR_API_SECRET"
    # 他のDB設定等は開発初期段階ではデフォルトのままでOK
    ```

3.  **Botを起動:**
    以下のコマンドで、Dockerコンテナ内でBotをビルドし、バックグラウンドで起動します。
    ```bash
    make up
    ```
    初回起動時はイメージのビルドに数分かかることがあります。

4.  **ログを確認:**
    Botの動作状況はログで確認できます。
    ```bash
    make logs
    ```
    Ctrl+C でログのフォローを停止できます。

5.  **Botを停止:**
    Botを停止し、関連するコンテナを削除するには以下のコマンドを実行します。
    ```bash
    make down
    ```

### 主な Makefile コマンド

- `make up`: Botを起動します (バックグラウンド実行、必要なら再ビルド)。
- `make down`: Botを停止し、コンテナを削除します。
- `make logs`: Botのログをリアルタイムで表示します。
- `make shell`: 実行中のBotコンテナ内でシェルを起動します (デバッグ用)。
- `make clean`: Botを停止し、コンテナと関連ボリューム (将来的にDBデータ等) を削除します。
- `make help`: 利用可能なすべての `make` コマンドを表示します。

<!--
## 🛠️ アーキテクチャ概要

(後日追記: システム構成図など)

## 📚 依存ライブラリ

(後日追記: 主なGoパッケージなど)

## 🔗 参考リソース

(後日追記: APIドキュメント、関連論文など)
-->

## 📝 ドキュメント

- `OPERATIONS-local.md`: Windows 11 + WSL2 環境での詳細な構築手順、FAQ。
- `OPERATIONS.md`: Google Cloud Run を使った本番運用手順、監視、障害対応。
- `TASK_MANAGEMENT.md`: 開発タスク一覧と進捗。

---
*このプロジェクトは学習および実験を目的としています。実際の取引に利用する際は、十分なテストとリスク管理を行った上で、自己責任でお願いします。*
