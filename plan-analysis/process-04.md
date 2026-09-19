# Process 04: 許可ツールの引数生成モデル分離

## goal

引数生成用モデル名とツール名の完全一致許可リストを設定できるようにする。既定は空で無効。片方だけの指定、空の名前、重複名、`forced` 以外との組み合わせは設定エラーにする。
工程03で実際に強制選択を適用できた許可ツールだけ、送信モデルを切り替える。ローカル候補限定・Jev失敗・対象外の通信形式には適用しない。認証、URL、履歴、引数スキーマ、推論設定をモデル切り替えに伴って変更しない。
元モデル・送信モデル・適用件数・拒否を工程02の計測へ追加する。上流拒否時の自動高価モデル再送は行わず、ホストが修復した要求もタスク比較へ含める。
READMEに、モデル識別子の明示指定が必要であり、読取・検索と自由記述のコマンド・差分を別課題で評価することを記載する。模擬試験を実モデルの互換性・品質認定と扱わない。
`TestAnalysisArgsModel` で許可/非許可、既定無効、不正設定、モデル以外の不変、上流拒否、無関係な要求への漏れを検証する。

## files

- `internal/proxy/options.go`
- `internal/proxy/rewrite.go`
- `internal/proxy/rewrite_test.go`
- `internal/proxy/events.go`
- `cmd/jev-routing/main.go`
- `cmd/jev-routing/main_test.go`
- `README.md`

## verify

```sh
go test ./internal/proxy -list '^TestAnalysisArgsModel$' | rg '^TestAnalysisArgsModel$'
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./internal/proxy ./cmd/jev-routing
```

## depends_on

03
