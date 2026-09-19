# Process 04: Jev回答の検証と不確実時の無変更復帰

## goal

askNextTool の質問を next_tool と needs_tool に整理する。「今ツール不要」をタスク完了度へ変換しない。回答型・必須項目・有限な0〜1の値・選択肢内存在を検査し、固定 Confidence:0.8 を実確信度に置き換える。
Jev採用閾値は初期0.8、選択したのに needs_tool < 0.5 なら不介入。境界値は採用側とする。これは設定した基準であり成功率の保証ではない。空回答・未知・型不正・低確信度・矛盾・通信失敗では低確信度ローカル候補へ戻さず元要求を返す。
ローカルで検証済みの工程一致は基準方式に維持する。強制選択可能性とは別の判定元として保持する。圧縮結果は選択確定まで原本に反映せず、不介入時の変更を防ぐ。
TestGatewayDecision を追加し、閾値前後、Jev失敗、ローカルのみ、Jev採用、補助回答不足を模擬HTTPで検証する。既存liveテストの質問契約も更新するが実サービスは呼ばない。

## files

- `internal/proxy/rewrite.go`
- `internal/proxy/rewrite_test.go`
- `internal/proxy/jev_live_test.go`
- `internal/jev/jev.go`
- `internal/jev/jev_test.go`
- `internal/plan/plan.go`

## verify

リポジトリ直下で実行する。以下の名前の回帰テストをこの工程で実装し、実APIへの接続なしで検証する。

```sh
go test ./internal/proxy ./internal/jev ./internal/plan -list 'TestGatewayDecision' | rg '^TestGatewayDecision'
go test -race -count=1 ./internal/proxy ./internal/jev ./internal/plan -run 'TestGatewayDecision'
go test ./internal/proxy ./internal/jev ./internal/plan
```

## depends_on

02, 03

