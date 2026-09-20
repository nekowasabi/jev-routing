# Process 04: モデルとeffortを有効な組から選択する

## goal

判断CLIへモデル選択を追加する。入力はタスク・役割・確定したホスト・許可済みのモデルとeffortの組・項目別fixed/auto・従来値とする。Jevは候補IDだけを選び、設定はカタログから復元する。固定項目は候補制約にし、両方固定なら推論しない。固定項目が未知のCLI既定なら、モデル・effortのどちらでも自動選択を保留する。低確信・時間切れ・未報告回答は従来値へ戻し、理由を記録する。モデル価格や対応表をコードへ固定しない。`JEV_ARGS_MODEL`は従来用途のまま残す。

## files

- `internal/plan/model.go`、`internal/plan/model_test.go`（新規）
- `internal/plan/capability.go`、`internal/plan/routing.go`
- `cmd/jev-routing/route.go`、`cmd/jev-routing/route_test.go`
- `internal/plan/testdata/model-profiles.json`（偽モデルの検証データ、新規）
- `README.md`

## verify

```sh
go test ./internal/plan -list '^TestModelRouting$' | rg '^TestModelRouting$'
go test ./internal/plan ./cmd/jev-routing -count=1
go test ./internal/proxy -run 'TestGatewayArgsModel|TestAnalysisArgsModel' -count=1
```

モデル固定／effort固定／両方自動／未指定を表形式で検証する。低確信、他ホスト候補、存在しない組、制約矛盾、未知CLI既定モデルでも意図しない引数を返さないことを確認する。

## depends_on

03。
