# Process 01: ローカル確定と意味判断の保留を分離する

## goal

`DecideSpecs`の疑似confidenceをJev省略の根拠に使わず、確定／除外／保留と根拠コードを返す。明示指定・完全に定義された規則・既存の重複起動防止を残し、語一致と難易度判断は保留する。`hybrid`でJev未接続・失敗なら全許可候補を保持する。`local`は比較用として残す。終了判定・圧縮・`JEV_ARGS_MODEL`は別責務を維持する。既存ベンチマークへ否定・曖昧語・複数目的・候補一件不適合を追加し、正解保持を検証する。

## files

- `internal/plan/plan.go`、`internal/plan/plan_test.go`
- `internal/proxy/rewrite.go`、`internal/proxy/gateway_decision_test.go`
- `scripts/testdata/selection-benchmark/input.json`

## verify

```sh
go test ./internal/plan ./internal/proxy -count=1
go test ./internal/proxy -list '^TestHybridDefersHeuristics$' | rg '^TestHybridDefersHeuristics$'
go test ./internal/proxy -run '^TestHybridDefersHeuristics$' -count=1
python3 -m unittest discover -s scripts -p 'test_summarize_selection_benchmark.py'
```

新規テストは固定高confidenceでJevを省略した旧挙動なら失敗すること。固定指定時のJev要求数0、曖昧時の要求、未接続・不正応答時の候補保持を検査する。

## depends_on

なし。
