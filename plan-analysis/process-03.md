# Process 03: 比較条件と適合要求への強制選択

## goal

選択方式を `filter`（既定）と `forced` から指定できる設定を追加し、実効設定を統計に含める。比較用に履歴圧縮と推論設定の変更を独立制御し、無指定は現行設定を維持する。未知値は起動時エラーにする。
`forced` でも安全なローカル判定でJevを省略した場合は候補限定のままにする。Jevの検証済み回答がある場合だけ、候補限定を再利用して通常のChat functionの `tool_choice` を固定する。同一モデルを維持し、履歴・推論設定は比較条件以外で変更しない。
介入は非ストリーミングChat JSON、完全な解釈可能テキスト履歴、通常functionの一意な名前、選択指定なしまたは `auto` に限定する。名前空間、custom、legacy、画像、未知項目の履歴、別プロトコル、明示選択、並列制約を満たせない要求は新方式を適用しない。明示選択の契約は候補限定でも破壊せず、選択前の要求を維持する。
上流のエラーをそのまま返し、自動再送しない。`TestAnalysisForced` に設定無効、適合、不適合、ローカル判定、Jev失敗、モデル不変、候補限定併用、拒否時1要求のみをまとめる。READMEに対象範囲と比較設定を記載する。

## files

- `internal/proxy/options.go`（新規）
- `internal/proxy/rewrite.go`
- `internal/proxy/rewrite_test.go`
- `internal/proxy/proxy.go`
- `internal/proxy/events.go`
- `cmd/jev-routing/main.go`
- `cmd/jev-routing/main_test.go`
- `README.md`

## verify

```sh
go test ./internal/proxy -list '^TestAnalysisForced$' | rg '^TestAnalysisForced$'
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./internal/proxy ./cmd/jev-routing ./internal/host
```

## depends_on

01, 02
