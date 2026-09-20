# Process 02: 共通能力カタログを導入する

## goal

`internal/plan`に種類・安定ID・説明・提供元・ホスト対応・入力仕様・利用可能性・権限・版を持つ能力型を追加する。現在の`Spec`から変換可能にし、既存ツール一覧、明示スキル一覧、登録CLI、導入済みプラグインのメタデータを取り込む。プラグイン内部の能力を重複登録せず提供元を保持する。MCPを追加起動せず、未知の副作用をreadへ変換しない。機密値は能力の説明へ取り込まない。完全な検証用仕様とJevへ渡す短い判断用記述を分ける。

各候補には適用先を必須にする。スキルは信頼済み本文参照、MCPは実接続と実ツール名、CLIは操作と引数契約、プラグインは配下能力、子は実起動器を持つ。適用先が存在しない候補を選定可能に登録しない。名前だけの候補追加では完了しない。

## files

- `internal/plan/capability.go`、`internal/plan/capability_test.go`（新規）
- `internal/proxy/rewrite.go`、`internal/proxy/catalog_shape_test.go`
- `internal/plan/testdata/capabilities.json`（新規）

## verify

```sh
go test ./internal/plan -list '^TestCapabilityCatalog$' | rg '^TestCapabilityCatalog$'
go test ./internal/plan ./internal/proxy -count=1
```

六分類の往復変換、同名で別提供元、ID衝突、未導入、未知権限、プラグイン内重複、スキル明示指定、能力版の変化をテストする。外部コマンドやMCPプロセスを起動しないことを偽の接続で確認する。

## depends_on

01。
