# Process 09: ダッシュボード用の使用量・適用・効果集計を接続する

## goal

ダッシュボード仕様の計算契約を実装する。既存のJev試行・LLM usage・適用状態・保存済み実行結果をIDで関連付け、全Jev使用量、親子を含む総トークン、状態別件数、壁時計完了時間を期間別に返す。Connect経路のJev usage欠落も補う。共有質問・再試行・キャッシュ・重複観測を区別し、欠測を0にしない。比較条件と品質が成立する実測組だけからトークン・時間の差を計算する。既存の推定削減は別項目にする。

イベント履歴の上限と期間累計を分離し、更新を同一IDへ適用する。保存済み比較を既存集計器で再生成できるようにし、模擬データと実測の出典を区別する。既存ダッシュボードAPIへ集計を追加し、別サービスは作らない。

## files

- `internal/proxy/events.go`、`internal/proxy/proxy.go`、`internal/proxy/usage.go`
- `internal/proxy/dashboard.go`、`internal/proxy/dashboard_metrics.go`、`internal/proxy/dashboard_metrics_test.go`（後二者は新規）
- `internal/proxy/gateway_observe_test.go`、`internal/proxy/usage_connect_test.go`
- `cmd/jev-routing/main.go`（実行終了統計の保存）
- `scripts/summarize_selection_benchmark.py`、`scripts/summarize_selection_comparison.py`
- `scripts/test_summarize_selection_benchmark.py`、`scripts/test_summarize_selection_comparison.py`
- `scripts/testdata/dashboard-metrics/`（新規）

## verify

```sh
go test ./internal/proxy -list '^TestDashboardMetrics$' | rg '^TestDashboardMetrics$'
go test ./internal/proxy ./cmd/jev-routing -count=1
python3 -m unittest discover -s scripts -p 'test_summarize_*.py'
```

実測・推定・欠測・悪化・品質失敗・比較不成立の期待値を固定する。親子の二重計上、同一usageの更新、共有質問、キャッシュ、失敗再試行、Connect経路、履歴上限・再起動で誤った削減や成功を出さないことを確認する。並列時間を壁時計時間へ加算しない。

## depends_on

07、08。
