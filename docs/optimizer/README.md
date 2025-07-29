# `optimizer` サービス 詳細設計書

## 1. サービスの概要

`optimizer`サービスは、取引戦略のパラメータを自動的に最適化する心臓部です。Python製の最適化フレームワーク`Optuna`を利用して、過去の市場データに対するバックテストを数千回繰り返し、最も収益性が高く、かつ頑健（ロバスト）なパラメータの組み合わせを発見します。

最終的に見つけ出した最適なパラメータ設定を`trade_config.yaml`ファイルとして出力し、`bot`サービスが実際の取引で利用できるようにします。

このサービスは、2つの主要なスクリプトで構成されています。
- **`drift_monitor.py`**: 市場のパフォーマンスを常時監視し、性能の悪化（ドリフト）を検知すると最適化の開始を要求します。
- **`main.py`**: `drift_monitor`からの要求を受け、実際の最適化プロセスを実行します。

## 2. モジュール構成

`optimizer`サービスは、責務に基づいて複数のモジュールに分割されています。

- `main.py`: `optimizer`サービスのメインエントリーポイント。`drift_monitor`によって作成されたジョブファイルを監視し、最適化プロセス全体を管理する。
- `drift_monitor.py`: `pnl_reports`テーブルから定期的にパフォーマンス指標を取得し、市場の変化や戦略のパフォーマンス低下を検知する。ドリフトを検知すると`optimization_job.json`を作成して`main.py`に最適化を要求する。
- `config.py`: `optimizer_config.yaml`から設定を読み込み、サービス全体で利用する定数やパラメータ（DB接続情報、各種閾値など）を一元管理する。
- `data.py`: `timescaledb`からのデータエクスポート、および最適化に使用するIn-Sample (IS)データと検証用のOut-of-Sample (OOS)データへの分割を担当する。
- `simulation.py`: Go言語で実装されたバックテストエンジン（`cmd/bot/main.go --simulate`）をサブプロセスとして呼び出し、特定のパラメータセットでのシミュレーションを実行する。
- `objective.py`: Optunaの`Trial`ごとに実行される目的関数を定義する。パラメータの提案、シミュレーションの実行、評価指標（シャープレシオ、ドローダウン等）の計算、および枝刈り（Pruning）ロジックを含む。
- `study.py`: Optunaの`Study`オブジェクトの作成、最適化の実行、完了後の結果分析（複合スコアでの再評価）、そしてOOS検証までの一連のフローを管理する。
- `analyzer.py`: 最適化で得られた多数の良好な結果から、KDE（カーネル密度推定）を用いてパラメータの分布を分析し、単一の最良値ではなく、最も安定した（頑健な）パラメータ領域を特定する。

## 3. Docker Compose上の役割と設定

`docker-compose.yml`には、`optimizer`と`drift-monitor`の2つのサービスが定義されます。（※実際の定義は異なる場合があります）

```yaml
services:
  optimizer:
    build:
      context: .
      dockerfile: optimizer/Dockerfile
    container_name: obi-scalp-optimizer
    # main.py を実行
    command: python -m optimizer.main
    healthcheck:
      test: ["CMD-SHELL", "test -f /data/params/trade_config.yaml"]
      # ...
    restart: always
    # ...
  drift-monitor:
    build:
      context: .
      dockerfile: optimizer/Dockerfile
    container_name: obi-scalp-drift-monitor
    # drift_monitor.py を実行
    command: python -m optimizer.drift_monitor
    restart: always
    # ...
```

-   **`optimizer`サービス**: `main.py`を起動し、最適化ジョブの発生を待ちます。`healthcheck`は最適化成功の証である`trade_config.yaml`の存在を確認し、`bot`サービスの起動条件となります。
-   **`drift-monitor`サービス**: `drift_monitor.py`を起動し、データベースを監視して継続的にパフォーマンスを評価します。

## 4. 処理フロー

### 4.1. ドリフト検知 (`drift_monitor.py`)

1.  `CHECK_INTERVAL_SECONDS`ごとにデータベースに接続します。
2.  `pnl_reports`テーブルから、短期（15分）・中期（1時間）のパフォーマンス指標と、長期（7日間）のベースライン統計（移動平均、標準偏差）を取得します。
3.  現在の指標がベースラインから大きく悪化していないか（Zスコアの低下、プロフィットファクターの悪化など）を複数のルールでチェックします。
4.  ドリフトを検知した場合、問題の深刻度（`minor`, `normal`, `major`）に応じた設定で`optimization_job.json`ファイルを作成します。

### 4.2. 最適化の実行 (`main.py` と関連モジュール)

1.  **ジョブの待機**: `main.py`は`optimization_job.json`が出現するのを待ち続けます。
2.  **データ準備 (`data.py`)**:
    -   `go run cmd/export/main.go`を実行し、ジョブで指定された期間の市場データをCSVにエクスポートします。
    -   データをIn-Sample (IS)とOut-of-Sample (OOS)に分割します。
3.  **In-Sample (IS) 最適化 (`study.py`, `objective.py`)**:
    -   `study.py`がOptunaの`Study`を新規作成します。
    -   設定された`n_trials`回数、`objective.py`の目的関数が呼び出されます。
    -   目的関数は、`trial.suggest_*`でパラメータを生成し、`simulation.py`経由でバックテストを実行し、結果を評価します。性能の悪い試行は枝刈りされます。
4.  **結果分析と候補選定 (`study.py`, `analyzer.py`)**:
    -   全試行完了後、`study.py`は単一指標だけでなく、複合スコアで全試行を再ランク付けします。
    -   `analyzer.py`を呼び出し、上位の試行群から最も頑健なパラメータセットを特定します。
5.  **Out-of-Sample (OOS) 検証 (`study.py`)**:
    -   IS最適化で選ばれた候補（頑健パラメータ、IS上位パラメータ）を、未知のOOSデータで検証します。
    -   OOSでの結果が設定された最低基準を満たせば「合格」です。
    -   合格した場合はそのパラメータを`trade_config.yaml`として保存し、プロセスを正常終了します。
    -   不合格だった場合は、次の候補でOOS検証を試みます（`max_retry`回まで）。
6.  **完了と待機**: `optimization_job.json`を削除し、次のジョブを待ちます。

## 5. 設計思想と考慮点

-   **責務の分離**: パフォーマンス監視（`drift-monitor`）と実際の最適化計算（`optimizer`）を分離することで、各プロセスをシンプルに保ち、独立してスケールさせることが可能です。
-   **モジュール化**: `optimizer`内のロジックを機能ごとにファイル分割することで、コードの可読性、保守性、テスト容易性を向上させています。
-   **自動化されたWalk-Forward最適化**: 「学習(IS)」と「検証(OOS)」を分離するウォークフォワード分析を自動化し、カーブフィッティングを抑制します。
-   **頑健性の重視**: 単一の最高スコアだけでなく、複合スコアやパラメータ分布の分析を通じて、より信頼性の高いパラメータを選択します。
-   **効率的な探索と自己修正**: Optunaの高度な探索アルゴリズムと、ドリフト検知による自律的な最適化ループを組み合わせることで、市場の変化に継続的に適応するシステムを実現しています。
