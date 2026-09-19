# Process 05: 無引数・定数引数の限定直接生成

## goal

直接生成用ツール許可リストを追加する。既定は空で無効。`forced` と組み合わせ、引数生成モデル分離とは同時に有効化できない設定とし、独立比較を可能にする。無効設定は無引数ツールでも確実に上流を呼ぶ。
工程03の検証済み選択・非ストリーミングChat functionに限り、全引数が確定する場合にChatの `tool_calls` 応答を合成して上流LLM要求を省略する。ランダムな一意IDと正しい終了理由を付与し、実ツールは実行しない。
対応スキーマは `type: object`、明示的な `properties`・`required`・`additionalProperties: false` と注釈に限定する。全プロパティがrequiredで、各値が型の一致するstring/boolean/number/integerの単一 `const` である場合だけ対応する。空properties・空requiredは無引数として許可する。未知キーワード、合成、参照、自由引数、enum、null、配列、ネスト、型不一致、不正数値は通常上流へ戻す。全候補への追加引数質問は作らない。
無効・対象外・不正スキーマは工程03の上流経路へ戻し、Jev通信と上流0回を区別して記録する。`allOf` の必須引数見落としと無効設定の無視を回帰ケースに含める。
`TestAnalysisDirect` に上流0回/1回、許可リスト、応答構造、ID、全対応外条件をまとめる。模擬クライアントで承認前未実行・拒否・承認後の結果を伴う次要求を確認する。実ホストの承認互換性は未検証とREADMEに明示し、既定有効にはしない。

## files

- `internal/proxy/direct.go`（新規）
- `internal/proxy/direct_test.go`（新規）
- `internal/proxy/proxy.go`
- `internal/proxy/rewrite.go`
- `internal/proxy/options.go`
- `internal/proxy/events.go`
- `cmd/jev-routing/main.go`
- `cmd/jev-routing/main_test.go`
- `README.md`

## verify

```sh
go test ./internal/proxy -list '^TestAnalysisDirect$' | rg '^TestAnalysisDirect$'
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./internal/proxy ./cmd/jev-routing
```

## depends_on

03, 04
