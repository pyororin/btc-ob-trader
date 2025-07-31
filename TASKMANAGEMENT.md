# タスク管理

## タスク一覧

| Priority | タスク | 具体アクション | 想定工数 |
| :--- | :--- | :--- | :--- |
| P0 | 多目的化 & ゆるい制約 | ① Optuna の MOTPE に切替え、maximize=[Sharpe, win_rate] / minimize=[max_dd] で Pareto フロント探索 ② 合格判定を soft filter（例：dd < 25 % ならペナルティ追加）に変更 | 0.5 d |
| P1 | 探索空間の再設計 | ① loguniform で感度の高い閾値を対数スケール化 ② 相関の強いパラメータは階層構造に（例：adaptive_* は enabled 時のみサンプル） | 1 d |
| P2 | Walk‑forward CV の導入 | backtester.run(params, start, end) を N 分割して平均スコアを返す。テスト期間を 2023→24→25 とローリング | 1.5 d |
| P3 | 早期打ち切り (pruner) 強化 | ① 途中エポックで KPI が劣後 50 ％ 以下なら trials.report/should_prune ② MedianPruner + PercentilePruner 併用 | 0.5 d |
| P4 | パラメータ重要度分析の自動フィードバック | 試行ごとに optuna.importance.get_param_importances → 重要度の低い次元を縮小／凍結し再探索ループ | 1 d |
| P5 | マルチフェーズ粗→細探索 | Phase‑1: 広い範囲 × 300 trial でラフ把握 → Phase‑2: 上位 20 ％ KDE で高密度領域を再サンプル | 1 d |
| P6 | 分散実行 & キャッシュ | Dask または Optuna distributed RDB + TimescaleDB に BT 結果要約をキャッシュし同一 params をスキップ | 2 d |
| P7 | シード再現性とログ整備 | 試行 seed, Git hash, BT 期間を DB に保存し「再現可能な最良試行」を export 可能に | 0.5 d |

## 新機能の一覧

### 🚀 新機能提案
1.  **オンライン適応型 Bayesian Optimization**
    *   本番稼働中の live PnL を “観測ノイズ入り目的関数” に組込み、Optuna を継続学習モードで走らせることで、市場 regime 変更に追従。
    *   実装イメージ: WebSocket で約定結果 → RabbitMQ → optimizer_worker が逐次 study.tell()。
2.  **メタパラメータ探索サービス化**
    *   FastAPI + Redis で「この YAML と損益 CSV を PUT すると最適セットを返す」社内 API を構築。CI で nightly BT & Slack 通知。
3.  **グラフィカル KDE ダッシュボード**
    *   Streamlit で上位試行の KDE／相関ヒートマップを即時確認。高原領域クリック → YAML スニペット自動生成。
4.  **リスクスイッチング・モード**
    *   VIX 代替として realized σ が閾値超え時は risk_max_position_ratio と order_ratio を半減させる安全モードを自動適用。
5.  **遺伝的アルゴリズムによるブートストラップ**
    *   初期世代を GA で多様化 → 収束後に Bayesian に切替えるハイブリッド最適化で局所最適脱出。
