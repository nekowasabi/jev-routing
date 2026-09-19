# Process 02: Grokの実カタログ名と旧名互換

## goal

既存の `host.Native` と `plan.aliasIn`・`Remaining` を修正し、確認済みのGrokカタログ名 `run_terminal_command` / `spawn_subagent` に対応する。
旧名 `run_terminal_cmd` / `task` だけを提供するカタログも既存の別名処理で扱い、実カタログにない名前を生成しない。
実行済み行動の新旧名も同じ工程として認識し、完了済み工程の再選択を防ぐ。
`run the tests` と委譲課題で期待するローカル工程とカタログ内ツールを選び、既存の確信度によるローカル分岐が成立することを検証する。
新旧カタログ、新旧名の行動履歴、他ホストの既存対応をテストする。新しい名前正規化層は作らない。

## files

- `internal/host/host.go`
- `internal/host/host_test.go`
- `internal/plan/plan.go`
- `internal/plan/plan_test.go`
- `internal/proxy/rewrite_test.go`

## verify

```sh
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./internal/host ./internal/plan ./internal/proxy
```

## depends_on

01
