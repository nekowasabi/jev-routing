# Process 02: 選定元の明示的な切替

## goal

`JEV_SELECTION_MODE=local|jev|hybrid` を追加し、`local` は Jev 選定を呼ばず、`jev` は適格な選定ごとに Jev を呼び、`hybrid` は現行の確信度閾値の挙動を維持する。Jev の不確実・不正・失敗応答は候補を絞らず通過させ、選定用 Jev の報告トークンを欠測と区別して実行統計へ出す。

## files

- `internal/proxy/options.go`
- `internal/proxy/rewrite.go`
- `internal/proxy/events.go`
- `internal/proxy/proxy.go`
- `internal/proxy/gateway_decision_test.go`
- `internal/proxy/rewrite_test.go`
- `cmd/jev-routing/main.go`
- `cmd/jev-routing/options_test.go`
- `README.md`

## verify

```sh
go test ./internal/proxy ./cmd/jev-routing -count=1
```

## depends_on

01
