# Process 03: Jev入力予算と圧縮キャッシュの分離

## goal

FitInput を入れ子の map / slice / 構造体由来の結果にも適用し、元オブジェクトを変更せずUTF-8を保持する。ツール名・呼び出しID・選択肢の同一性は切り詰めない。JointTokens は既存の見積りであり実課金値ではない。
圧縮後も予算超過なら Ask は外部要求を出さず明示エラーを返す。上限直前・一致・超過、巨大なactions_taken.Result、縮められない選択肢を検証する。
判定キャッシュのキーを実際の判定状態（目的・履歴を含む）、候補ID/内容、質問、モデル、接続先、判定仕様から生成し、異なる要求・同じIDの別本文へ漏らさない。同条件の再利用は維持し、不正回答は保存しない。並列要求で競合しないことを確認する。
TestGatewayInputBudget と TestGatewayVerdictCache を追加し、模擬Jevの実受信回数で再利用/失効を検証する。

## files

- `internal/jev/jev.go`
- `internal/jev/jev_test.go`
- `internal/compact/state.go`
- `internal/compact/state_test.go`

## verify

リポジトリ直下で実行する。以下の名前の回帰テストをこの工程で実装し、実APIへの接続なしで検証する。

```sh
go test ./internal/jev ./internal/compact -list 'TestGateway(InputBudget|VerdictCache)' | rg '^TestGateway(InputBudget|VerdictCache)'
go test -race -count=1 ./internal/jev ./internal/compact -run 'TestGateway(InputBudget|VerdictCache)'
go test ./internal/jev ./internal/compact
```

## depends_on

01, 02
