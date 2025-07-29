# `botsimulate` サービス 詳細設計書

## 1. サービスの概要

`botsimulate`サービスは、取引ボットのロジックを過去の市場データを用いて検証（バックテスト）するためのシミュレーション環境です。`bot`サービスと同じコードベースを使用し、実行時のフラグによってシミュレーションモードで動作します。

このサービスを利用することで、実際の資金を投入する前に、取引戦略の有効性やパラメータのパフォーマンスを評価することができます。

## 2. Docker Compose上の役割と設定

`docker-compose.yml`における`botsimulate`サービスの定義は以下の通りです。

```yaml
services:
  bot-simulate:
    build:
      context: .
      dockerfile: Dockerfile
    container_name: obi-scalp-bot-simulate
    entrypoint: ["/usr/local/bin/obi-scalp-bot"]
    env_file:
      - .env
    volumes:
      - .:/app
    networks:
      - bot_network
```

### 設定解説

- **`build`**: `bot`サービスと共通の`Dockerfile`を使用してイメージをビルドします。
- **`container_name`**: `obi-scalp-bot-simulate`というコンテナ名が付けられます。
- **`entrypoint`**: Goでビルドされた実行可能ファイル`/usr/local/bin/obi-scalp-bot`を直接指定しています。`bot`サービスと異なり、`entrypoint.sh`を介さないため、`trade_config.yaml`の存在を待つ処理は行われません。
- **`command`**: `docker-compose.yml`内では定義されていません。シミュレーションを実行する際は、`docker compose run`コマンドの引数として、シミュレーション用のフラグ（`--simulate`や`--csv`など）を渡すことが想定されています。

## 3. 処理フローとロジック

### 3.1. 起動シーケンス

`botsimulate`は、通常`docker compose run`コマンドで一時的なコンテナとして起動されます。

**実行コマンド例:**
```bash
docker compose run --rm botsimulate \
  --simulate \
  --csv /app/path/to/market_data.csv \
  --config /app/config/app_config.yaml \
  --trade-config /app/config/trade_config_for_sim.yaml \
  --json-output
```

1.  **`main.go`の初期化**:
    -   `main()`関数が実行されます。
    -   `flag.Parse()`でコマンドライン引数を解釈します。上記の例では`--simulate`が`true`に設定されるため、`f.simulateMode`が`true`になります。
    -   `setupConfig()`で、指定された設定ファイル（`app_config.yaml`と`trade_config_for_sim.yaml`）を読み込みます。
    -   `runMainLoop()`が呼び出されます。

### 3.2. シミュレーションループ (`runSimulation`)

1.  **シミュレーションのセットアップ**:
    -   `f.simulateMode`が`true`のため、`runMainLoop`は`runSimulation` goroutineを起動します。
    -   `indicator.NewOrderBook()`でシミュレーション用の空の注文簿を作成します。
    -   `engine.NewReplayExecutionEngine()`で、実際には注文を行わず、取引の記録だけを行う「リプレイエンジン」を初期化します。
    -   `tradingsignal.NewSignalEngine()`で、シミュレーション用の取引パラメータを使ってシグナルエンジンを初期化します。

2.  **データストリーミング**:
    -   `datastore.StreamMarketEventsFromCSV()`が呼び出され、`--csv`フラグで指定されたCSVファイルから一行ずつ市場イベント（注文簿の更新や取引履歴）を読み込み、チャネルに送信します。

3.  **イベント処理ループ**:
    -   `runSimulation`はループの中でCSVから読み込んだイベントを一つずつ処理します。
    -   **`OrderBookEvent`**:
        -   `orderBook.ApplyUpdate()`で注文簿の状態を更新します。
        -   `orderBook.CalculateOBI()`でOBIを計算します。
        -   `signalEngine.Evaluate()`で、更新されたOBIに基づいて売買シグナルを評価します。
        -   シグナルが生成された場合、`replayEngine.PlaceOrder()`を呼び出します。このエンジンは実際には注文せず、仮想的な取引として記録するだけです。
    -   **`TradeEvent`**:
        -   `replayEngine.UpdateLastPrice()`で最新の取引価格を更新します（損益計算用）。
        -   CVD（Cumulative Volume Delta）の計算のために`signalEngine`に取引データを渡します。
        -   `signalEngine.Evaluate()`で再度シグナルを評価し、必要であれば仮想的な注文を記録します。

4.  **結果の集計と出力**:
    -   CSVファイルのすべての行を処理し終えると、ループが終了します。
    -   `getSimulationSummaryMap()`が呼び出され、リプレイエンジンに記録された全取引の履歴から、総損益、勝率、最大ドローダウンなどの詳細なパフォーマンス指標を計算します。
    -   `--json-output`フラグが指定されている場合、計算されたサマリーはJSON形式で標準出力に出力されます。これは`optimizer`サービスが結果を解釈するために利用します。
    -   最後に、`syscall.SIGTERM`シグナルを送信し、アプリケーションが正常にシャットダウンするように促します。

## 4. 設計思想と考慮点

-   **コードの再利用**: `bot`サービスとほぼ同じコードベースを共有することで、シミュレーションと実際の取引との間でのロジックの乖離を防ぎ、信頼性の高いバックテストを実現しています。
-   **分離された実行エンジン**: `engine.ExecutionEngine`インターフェースを定義し、ライブ取引用の`LiveExecutionEngine`とシミュレーション用の`ReplayExecutionEngine`を実装することで、取引ロジックのコア部分を変更することなく、実行環境（ライブ or シミュレーション）を切り替えられるように設計されています。
-   **再現性**: シミュレーションは単一のスレッドで、CSVファイルのタイムスタンプ順にイベントを処理するため、何度実行しても同じ結果が得られる決定論的な動作をします（`rand.Seed(1)`で乱数も固定）。
-   **パフォーマンス分析**: `getSimulationSummaryMap`で多角的なパフォーマンス指標を算出することで、戦略の長所と短所を詳細に分析できます。
-   **自動最適化との連携**: シミュレーション結果をJSON形式で出力する機能は、`optimizer`サービスが多数のパラメータの組み合わせを自動的にテストし、最適なパラメータを見つけ出すための基盤となっています。

## 5. 想定ユースケースとトリガー

-   **ユースケース1: 手動での戦略検証**:
    -   開発者が新しいロジックやパラメータを試す際に、手動で`docker compose run`を実行し、コンソールに出力されるサマリーを確認する。
-   **ユースケース2: パラメータ最適化**:
    -   `optimizer`サービスから呼び出される。この場合、`botsimulate`は`--serve`モードで起動し、標準入力からJSON形式でシミュレーションのリクエストを受け取り、結果を標準出力に返すサーバーとして機能する。
-   **トリガー**: `docker compose run`コマンドによる手動実行、または`optimizer`サービスからのプロセス起動。
