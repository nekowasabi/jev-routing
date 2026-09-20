# Process 07: 予算付き一括判断と種類別の適用制御を完成させる

## goal

同じstateで独立した自動対象だけを一括評価する。メンバー分割時は全体期限を共有し、期限切れ後は再試行せず従来値へ戻る。既存キャッシュのキーを維持し、能力版・方針版・権限条件を判断入力に含める。キャッシュ命中後も適用前の稼働状態確認を省略しない。全段階・失敗・キャッシュの計測を既存の試行フックへ統合する。種類別にoff/observe/applyを設け、新種類はobserve、ateamはfixedを既定とする。評価を通った対象だけapply/autoへ移すための比較出力を既存集計器に追加する。未計測ならobserveのままとし、改善を断言しない。

選定数と実適用数・結果受信数・成果検証数を分け、未配達・不明・承認待ちを集計する。観測モードやカタログ絞込みの成功を実行成功へ流用しない。機能の全体完了には工程08の実接続と受入が必要である。

## files

- `internal/jev/jev.go`、`internal/jev/jev_test.go`
- `internal/plan/routing.go`、`internal/plan/routing_test.go`
- `internal/proxy/options.go`、`internal/proxy/rewrite.go`、`internal/proxy/gateway_observe_test.go`
- `cmd/jev-routing/route.go`、`cmd/jev-routing/route_test.go`
- `scripts/summarize_selection_benchmark.py`、`scripts/test_summarize_selection_benchmark.py`
- `scripts/testdata/capability-routing/input.json`（新規）
- `README.md`

## verify

```sh
go test ./internal/jev -list '^TestRoutingBatchBudget$' | rg '^TestRoutingBatchBudget$'
go test ./... -count=1
python3 -m unittest discover -s scripts -p 'test_summarize_*.py'
python3 scripts/summarize_selection_benchmark.py scripts/testdata/capability-routing
```

応答順に依存しない質問ID対応、一部欠落、分割要求の合計usage、欠測保持、全体期限、モデル・接続先・能力版・権限変更時のキャッシュ不一致を検証する。固定・observe・applyで実際の引数が契約どおりになることを確認する。実Jev／実LLM比較では分析書の採点契約に従い、成功品質・総トークン・完了時間を併記する。模擬データの集計成功を実測改善と呼ばない。

## depends_on

01〜06。
