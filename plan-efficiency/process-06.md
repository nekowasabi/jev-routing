# Process 06: 強制ツール指定と推論設定の契約保持

## goal

`RewriteWithClient` の入口で対応可能性を判断し、強制指定・未対応設定・未対応形式は圧縮や外部判定より前に原要求のまま返す。
`tool_choice` の無条件削除を除く。既知の自動選択以外の指定（ツール名、必須、無効、未知形式）は要求全体を素通しとし、指定と候補限定の矛盾を作らない。
明示された推論設定を保持し、仕様が確認できないホスト・モデルに推論設定を追加・置換しない。既存の推論削減を当然の受入条件とせず、削減設定の比較は後段③に残す。
カタログ省略時は原要求を維持し、`tools: []` や推論設定を補わない。原形保持は書式を含む要求本文の一致で検証する。
既存の `ReverseProxy` を使い、認証、`stream`、SSE本文、HTTP状態、本文長、キャンセルを維持する。
文字列／オブジェクトの強制指定、`none`、未対応の推論設定、カタログなし、対応可能な通常要求をテストする。
模擬上流・Jevサーバーで素通し時の本文一致と外部判定ゼロ、通常経路の変換と通信契約を確認する。

## files

- `internal/proxy/rewrite.go`
- `internal/proxy/rewrite_test.go`
- `internal/proxy/proxy_test.go`

## verify

```sh
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./internal/proxy
```

## depends_on

01、02、05
