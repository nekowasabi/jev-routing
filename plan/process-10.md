# Process 10: 許可対象だけ引数生成モデルを分離

## goal

JEV_ARGS_MODEL と JEV_ARGS_TOOLS（完全一致のカンマ区切り名）を導入する。既定は空で無効。片方だけ・重複/空のツール名・forced以外との組合せは設定エラー。モデルは利用者が明示する識別子を透過使用し、価格や提供元の対応を推測しない。
対象は工程09でJevによりforcedとなった許可リスト内ツールだけ。元モデル以外に、履歴・引数スキーマ・認証・上流URLを変更しない。原モデルと送信モデルを計測する。モデル互換性を保証するとは表示しない。
モデルの実引数品質や副作用の安全性は模擬試験だけで認定しない。既定の対象リストは空、READMEに読取/検索等から意味的な完了検査で評価する条件を記載する。
失敗時の自動高価モデル再送は追加しない。ホストが再試行した場合も別要求として失敗込みで集計する。
TestGatewayArgsModel を追加し、許可/非許可、Jev失敗、ローカル選択、既定off、無効な組合せ、上流拒否、モデル以外の不変を検証する。

## files

- `internal/proxy/options.go`
- `internal/proxy/options_test.go`
- `internal/proxy/rewrite.go`
- `internal/proxy/rewrite_test.go`
- `internal/proxy/events.go`
- `cmd/jev-routing/main.go`
- `README.md`

## verify

リポジトリ直下で実行する。以下の名前の回帰テストをこの工程で実装し、実APIへの接続なしで検証する。

```sh
go test ./internal/proxy ./cmd/jev-routing -list 'TestGatewayArgsModel' | rg '^TestGatewayArgsModel'
go test -race -count=1 ./internal/proxy ./cmd/jev-routing -run 'TestGatewayArgsModel'
go test ./internal/proxy ./cmd/jev-routing
```

## depends_on

09

