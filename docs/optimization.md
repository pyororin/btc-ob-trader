# パラメータ最適化 (`optimizer.py`)

このドキュメントでは、`optimizer.py`スクリプトによる取引パラメータの最適化プロセスについて説明します。

## 概要

`optimizer.py`は、過去の市場データを用いて取引ボットのパフォーマンスを最大化するパラメータの組み合わせを見つけ出すためのスクリプトです。このプロセスは、主に以下の2つのフェーズで構成されます。

1.  **In-Sample (IS) 最適化**:
    *   指定された期間のデータ（ISデータ）を使用して、`Optuna`フレームワークによる最適化を実行します。
    *   目的関数（現在はSQN: System Quality Number）を最大化するようなパラメータの組み合わせを探索します。

2.  **Out-of-Sample (OOS) 検証**:
    *   IS最適化で得られた優秀なパラメータセットが、未知のデータ（OOSデータ）に対しても有効であるか（過剰最適化されていないか）を検証します。
    *   この検証を通過したパラメータセットのみが、実際の取引に使用される設定ファイル (`trade_config.yaml`) に反映されます。

## Robust Parameter Selection

Simply picking the best-performing parameter set from the In-Sample (IS) phase carries a high risk of overfitting. A parameter set might perform exceptionally well on the IS data by chance, but fail on unseen Out-of-Sample (OOS) data.

To mitigate this risk, this system implements a robust parameter selection process. Instead of just picking the single best trial, it analyzes the characteristics of a group of top-performing trials to find a parameter set that is more likely to be stable and profitable in the future.

### Algorithm

1.  **IS Optimization**: The optimizer runs a large number of trials on the IS data to explore the parameter space.
2.  **Top-Tier Selection**: After the IS optimization is complete, the system selects a subset of the best-performing trials (e.g., the top 10% based on the SQN score).
3.  **Robustness Analysis**: The `analyzer.py` script is executed. It analyzes the distribution of each parameter within this top-tier group.
    *   For numerical parameters, it uses Kernel Density Estimation (KDE) to find the value where the highest density of top-performing trials is concentrated (the mode).
    *   For categorical parameters, it selects the most frequently occurring value (the mode).
4.  **Parameter Recommendation**: The combination of these "mode" values forms a new, robust parameter set. This set represents a region in the parameter space where good performance is consistently found, rather than a single, potentially anomalous, peak.
5.  **OOS Validation**: This single, robust parameter set is then validated against the OOS data.
    *   If it passes the predefined criteria (e.g., `oos_min_profit_factor`), it is adopted as the new official parameter set.
    *   If it fails, the optimization run is considered unsuccessful, and no changes are made. The system will wait for the next scheduled optimization.

This approach prioritizes the stability and generalizability of the parameters over the raw performance on the IS data, leading to a more reliable trading strategy.

### Configuration

The behavior of the analysis is controlled by the `analyzer` section in the `optimizer_config.yaml` file.

*   `top_trials_quantile`: This determines the percentage of top trials to include in the robustness analysis. For example, a value of `0.1` means the top 10% of trials (by SQN score) will be used to find the robust parameter set.

### 結果の確認

最適化の実行結果は、`optimization_history`データベーステーブルに保存されます。Grafanaのダッシュボードを通じて、各試行の結果を視覚的に確認できます。

*   **is_rank**: 最終的に採用された、または最後に試行されたパラメータのIS時点でのランキング。
*   **retries_attempted**: OOS検証を試行した回数。
*   **validation_passed**: OOS検証に最終的に合格したかどうか (`true`/`false`)。

これらの指標をモニタリングすることで、最適化プロセスが健全に機能しているかを判断できます。例えば、`retries_attempted`が頻繁に`MAX_RETRY`に達し、`validation_passed`が`false`となるケースが多い場合、市場環境の変化やモデルの劣化が考えられ、再最適化のトリガーや取引戦略自体の見直しが必要になる可能性があります。
