# Process 05: サブエージェントの判断・起動・結果回収を接続する

## goal

委譲可否をローカルのユーザー指示・実行中の子・許可された名簿で制約し、意味判断が必要な場合だけJevへ委譲要否と役割の選択を渡す。委譲不要を必ず表現する。明示されたチーム起動では要否を再審査せず、固定の人数・役割・ホストを守る。役割が先に確定している場合はModel質問を同時化できるが、役割選定結果に依存するモデル候補を同じ問い合わせ内で参照しない。返すのは既存分担へのID参照であり、Jevにプロンプトや無制限の実行計画を生成させない。

ホストの実起動ツールへ分担本文・選定モデル・effortを渡すところまで接続する。分担本文が未確定なら親の引数生成で確定し、Jevにタスクを自由生成させない。ホスト内起動または接続済みateamのうち候補に登録された経路を使用し、同じ判断で両方を起動しない。子の起動IDと最終結果を保持して親の継続入力へ戻す。進捗返信や起動受付だけでは完了にしない。ateam実装は工程06で接続する。

## files

- `internal/plan/subagent.go`、`internal/plan/subagent_test.go`（新規）
- `internal/plan/routing.go`、`internal/plan/plan.go`
- `cmd/jev-routing/route.go`、`cmd/jev-routing/route_test.go`
- `internal/proxy/application.go`、`internal/proxy/application_test.go`
- `internal/plan/testdata/capabilities.json`
- `README.md`

## verify

```sh
go test ./internal/plan -list '^TestSubagentRouting$' | rg '^TestSubagentRouting$'
go test ./internal/plan ./internal/proxy ./cmd/jev-routing -count=1
```

委譲禁止・明示チーム・実行中の同一役割・追加委譲・委譲不要・低確信を検証する。偽の子実行器で起動引数→子の最終結果→親への配達まで確認する。進捗だけ・子の失敗・結果未回収・二重応答で成功判定しない。既存の連続Agent起動対策と返信用ツールの保持を回帰確認する。実子起動は工程08の無害な実環境試験で確認する。

## depends_on

03、04。
