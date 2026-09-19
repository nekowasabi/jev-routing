# Process 01: 呼び出しIDとGrokツール名の対応を修正

## goal

既存の compact.Item を使い、actionsFromItems が PairID で結果を結合するよう修正する。並列・順不同でも正しい本文を保持し、未知IDは既知行動へ代入しない。重複結果は同一IDについて最後の結果を採用し、未完了の呼び出しは残す。
Grokの実カタログ名 run_terminal_command / spawn_subagent を正規名として対応し、既存別名も維持する。工程一致の採点を成功確率と表現しない。
既存の隔離診断テストは参考資料に留め、回帰入力を本リポジトリへ組み込む。隔離ディレクトリや外部CLIをテストの依存にしない。
TestGatewayHistory と TestGatewayGrokNames を追加し、単一・並列・逆順・未知ID・未完了・重複・正規名/別名を検証する。

## files

- `internal/proxy/rewrite.go`
- `internal/proxy/rewrite_test.go`
- `internal/plan/plan.go`
- `internal/plan/plan_test.go`

## verify

リポジトリ直下で実行する。以下の名前の回帰テストをこの工程で実装し、実APIへの接続なしで検証する。

```sh
go test ./internal/proxy ./internal/plan -list 'TestGateway(History|GrokNames)' | rg '^TestGateway(History|GrokNames)'
go test -race -count=1 ./internal/proxy ./internal/plan -run 'TestGateway(History|GrokNames)'
go test ./internal/proxy ./internal/plan
```

## depends_on

なし

