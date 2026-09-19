# Process 02: 実通信・上流使用量・要求結果の計測

## goal

既存の `/stats` と `JEV_RUN_STATS` を拡張し、要求IDごとに選択元、適用分岐、不適合理由、元モデル・送信モデル、Jevの実呼出回数・目的・入力量・質問数・結果、上流呼出回数・状態・使用量・経過時間を記録する。`Engine=live` は実通信回数へ読み替えず、`Client.Ask` のHTTP送信境界で記録する。圧縮と選択を分離し、キャッシュ命中・未送信・失敗も区別する。
応答のJSONと既存SSE転送から報告された入力・出力・推論・キャッシュ使用量を収集する。未報告値は欠落として保持し、文字数からトークン数や費用を捏造しない。SSEは逐次転送を維持し、行・収集量を制限しても元応答を破損させない。取消・切断・上流エラーでも結果を確定させる。
現在 `Client.Ask` にない要求コンテキストを圧縮・選択の全呼出し元から伝播し、切断後の残存通信を抑える。既存のタイムアウトは維持し、再試行は増やさない。要求取消と内部期限を区別して記録する。
既存集計キーと透過転送を維持し、履歴は上限付きメモリに限定する。本文、引数、認証情報は統計へ保存しない。終了時の集計と画面用の取得は同じ計測値を使う。
`TestAnalysisMetrics` を追加し、ローカル選択でも圧縮通信あり、Jev失敗、キャッシュ、JSON/SSE使用量、欠落使用量、並行要求、取消、レスポンス不変を模擬HTTPで確認する。`TestAnalysisRunStats` で既存キーとの互換性を検証する。

## files

- `internal/jev/jev.go`
- `internal/jev/jev_test.go`
- `internal/proxy/proxy.go`
- `internal/proxy/rewrite.go`
- `internal/proxy/events.go`（新規）
- `internal/proxy/usage.go`（新規）
- `internal/proxy/metrics_test.go`（新規）
- `cmd/jev-routing/main.go`
- `cmd/jev-routing/main_test.go`

## verify

```sh
go test ./internal/proxy -list '^TestAnalysisMetrics$' | rg '^TestAnalysisMetrics$'
go test ./cmd/jev-routing -list '^TestAnalysisRunStats$' | rg '^TestAnalysisRunStats$'
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./internal/jev ./internal/proxy ./cmd/jev-routing
```

## depends_on

01
