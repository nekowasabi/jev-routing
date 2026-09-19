# Process 08: 比較条件を独立指定する設定と集計出力

## goal

serve/run共通の設定を起動時に一度解決し、不正値は起動失敗にする。JEV_ROUTING_MODE=baseline|filter|forced（既定filter）、JEV_COMPACTION=off|on（既定on）、JEV_REASONING=preserve|legacy（既定legacy）を導入する。forcedは工程09まで不介入理由forced_unavailableで処理し、候補限定へ暗黙変換しない。
baseline は全書換とJev通信を止め、圧縮/推論設定より優先する。filterは工程04の安全な基準方式。推論preserveは値と関連設定を変更しない。設定は要求中に変更しない。
JEV_RUN_STATS の既存キーを維持し、実通信・usage欠測・中断/失敗を含む集計、適用設定、起動識別子を追加する。JEV_RUN_ID は任意の比較IDとし文字種/長さを制限、未指定は自動生成。収集範囲は単一プロセスと明示する。
TestGatewayOptions と TestGatewayRunStats を追加し、環境を隔離して優先順位/不正値/両起動経路/基準無変更/秘密非出力を検証する。READMEに比較条件と未測定の効果を併記する。

## files

- `internal/proxy/options.go（新規）`
- `internal/proxy/options_test.go（新規）`
- `internal/proxy/rewrite.go`
- `internal/proxy/proxy.go`
- `cmd/jev-routing/main.go`
- `cmd/jev-routing/main_test.go`
- `README.md`

## verify

リポジトリ直下で実行する。以下の名前の回帰テストをこの工程で実装し、実APIへの接続なしで検証する。

```sh
go test ./internal/proxy ./cmd/jev-routing -list 'TestGateway(Options|RunStats)' | rg '^TestGateway(Options|RunStats)'
go test -race -count=1 ./internal/proxy ./cmd/jev-routing -run 'TestGateway(Options|RunStats)'
go test ./internal/proxy ./cmd/jev-routing
```

## depends_on

07

