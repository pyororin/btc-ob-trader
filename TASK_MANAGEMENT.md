# タスク管理 (TASK_MANAGEMENT.md)

## 凡例

| ステータス   | 説明                               |
| -------- | ---------------------------------- |
| `todo`   | 未着手                               |
| `wip`    | 作業中 (Work In Progress)            |
| `review` | レビュー待ち                             |
| `done`   | 完了                               |
| `icebox` | 保留中 (現時点ではスコープ外だが将来的に検討) |

---

## タスク一覧

| ID   | タイトル                | ステータス | 目的 / 手順 / **DoD**                                                                                                       | 担当者 | 期限   | 備考        |
| ---- | ------------------- | ----- | ----------------------------------------------------------------------------------------------------------------------- | ---- | ---- | ----------- |
| T-01 | WebSocket クライアント    | wip   | **目的**：orderbook・trades 購読、再接続<br>**手順**：gorilla/websocket、ping/pong 実装、指数バックオフ<br>**DoD**：10 分連続欠落ゼロ、TimescaleDB 保存 OK |      |      |             |
| T-02 | L2 & OBI            | todo  | **目的**：OBI₈/₁₆ 計算。ヒープ構造、300 ms 更新。<br>**手順**：(詳細未定)<br>**DoD**：単体テストで理論値一致                                                              |      |      |             |
| T-03 | TradeHandler & CVD  | done | **目的**：500 ms ロール CVD。ring-buffer 実装。<br>**手順**：(詳細未定)<br>**DoD**：csv テストで符号一致                                                                          |      |      |             |
| T-04 | SignalEngine v1     | todo  | **目的**：50 ms 判定・300 ms 継続ロジック。<br>**手順**：(詳細未定)<br>**DoD**：ロング5/ショート5 シグナル発火                                                                            |      |      |             |
| T-05 | ExecutionEngine     | todo  | **目的**：POST\_ONLY 指値・cancel/replace。<br>**手順**：(詳細未定)<br>**DoD**：Mock 50 注文全成功                                                                          |      |      |             |
| T-06 | TimescaleDB Writer  | todo  | **目的**：板差分・PnL 保存、圧縮。<br>**手順**：(詳細未定)<br>**DoD**：10 万行→圧縮率 >60 %                                                                                       |      |      |             |
| T-07 | README.md           | wip   | **目的**：日本語クイックスタート。<br>**手順**：プロジェクト概要、ローカル起動手順、Makefileコマンド説明<br>**DoD**：新環境で `make up` 成功                                                                 |      |      | README.md 更新済み、DoDは`make up`の成功で判断 |
| T-08 | OPERATIONS.md       | todo  | **目的**：本番手順・監視 SOP。<br>**手順**：(詳細未定)<br>**DoD**：Staging healthcheck PASS                                                                                |      |      |             |
| T-10 | OPERATIONS-local.md | todo  | **目的**：Win11+WSL2 手順。<br>**手順**：(詳細未定)<br>**DoD**：新規 PC で `make replay` 成功                                                                              |      |      |             |
| T-11 | docker-compose.yml  | wip   | **目的**：Bot単体起動、Healthcheck・ボリューム永続。<br>**手順**：Botサービス定義、.env・configマウント<br>**DoD**：`docker-compose up bot` 成功、再起動後データ保持(DB接続後)                                                              |      |      | Bot単体起動は実装済み。Healthcheck, Volume永続(DB)は後続 |
| T-12 | Makefile            | wip   | **目的**：`up/replay/down` ラッパ。<br>**手順**：`up, down, logs, shell, clean`実装<br>**DoD**：Win11 & Linux 両対応 (`make up`, `make down` 動作確認)                                                                    |      |      | `replay`は未実装 |
| T-13 | MicroPrice & OFI    | todo  | **目的**：追加指標実装。<br>**手順**：(詳細未定)<br>**DoD**：単体テスト誤差 0                                                                                                    |      |      |             |
| T-14 | ボラ閾値スケール            | todo  | **目的**：σ に基づく動的 OBI 閾値。<br>**手順**：(詳細未定)<br>**DoD**：高ボラ期発火率安定                                                                                           |      |      |             |
| T-15 | Long/Short 非対称 R/R  | todo  | **目的**：方向別 TP/SL・閾値。<br>**手順**：(詳細未定)<br>**DoD**：Backtest Sharpe +10 %、DD −5 %                                                                          |      |      |             |
| T-16 | 回帰ベンチ / A-B         | todo  | **目的**：v0↔v1 性能比較レポート。<br>**手順**：(詳細未定)<br>**DoD**：README に結果貼付                                                                                         |      |      |             |

*注: 「担当者」「期限」「備考」列は、実際のプロジェクト管理ツールやチームでの運用に合わせて活用してください。*
*DoD (Definition of Done) はタスク完了の明確な基準です。*
*手順の「(詳細未定)」部分は、各タスクに着手する際に具体化します。*
