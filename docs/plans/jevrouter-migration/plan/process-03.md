# Process 03: 能力の自動選定を適用と結果照合へ接続する

## goal

既存プロキシの要求受信とツール結果受信から共通判断関数を自動的に呼ぶ。Skills／MCP／CLIs／Pluginsを共通Choiceで扱い、明示指定・不適格候補除外・no_match・応答検証を行う。`route --json`は同じ関数をateamと診断から呼ぶ入口として追加するが、LLMによる自発的な呼出しには依存しない。

選定したスキル本文を出典付きの追加コンテキストとして供給し、プラグインを配下能力へ解決する。MCP／CLIは対応JSONプロトコルで次の実呼出しへ接続し、生成された引数を検証してホスト実行器へ渡す。CLIはシェル名だけでなくコマンドまで照合する。ストリームは呼出しを検証する前に副作用を実行させない。次要求の結果を呼出しIDで照合し、選定・配達・結果・検証を別状態で保存する。既存承認経路を維持し、不明な実行を自動再送しない。Cursor／Devinの独自経路は工程08で同契約へ接続する。

## files

- `cmd/jev-routing/main.go`、`cmd/jev-routing/route.go`、`cmd/jev-routing/route_test.go`（後二者は新規）
- `internal/plan/routing.go`、`internal/plan/routing_test.go`（新規）
- `internal/jev/jev.go`、`internal/jev/response_shape_test.go`
- `internal/proxy/rewrite.go`、`internal/proxy/gateway_decision_test.go`
- `internal/proxy/proxy.go`、`internal/proxy/direct.go`
- `internal/proxy/application.go`、`internal/proxy/application_test.go`（新規）
- `README.md`（自動呼出し・適用・結果照合の契約）

## verify

```sh
go test ./cmd/jev-routing -list '^TestRouteCommand$' | rg '^TestRouteCommand$'
go test ./internal/proxy -list '^TestAutomaticApplicationLifecycle$' | rg '^TestAutomaticApplicationLifecycle$'
go test ./internal/plan ./internal/jev ./internal/proxy ./cmd/jev-routing -count=1
```

偽Jev・偽上流・偽ホスト実行器で、通常要求だけを渡して本文配達、ツール呼出し、対応結果まで検証する。選定のみ・別コマンド・結果欠落・ストリーム中断を成功にしない。no_match・無効ID・欠落回答・能力版変更、CLIのJSON出力、明示スキル保持も検査する。同じ要求の再送と意図的な新規同文要求を区別し、呼出し後の不明状態で二重実行しない。

## depends_on

02。
