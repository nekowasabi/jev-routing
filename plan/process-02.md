# Process 02: Responsesの正規化と介入対象の判定

## goal

Responsesの function_call / function_call_output / custom_tool_call とその結果を型に基づき正規化し、工程01のID対応へ渡す。tools と additional_tools、名前空間、提供元実行ツールを識別する。既存のChat/Anthropic形式を保つ。
元要求を保持し、明示tool_choice、previous_response_id、重複名、認識できない形式/ツール、名前空間・提供元実行ツールを含み安全な限定を保証できないカタログは、圧縮・推論設定変更より前に介入対象外とする。理由を固定識別子で返し、生データを理由へ混ぜない。未知形式を空カタログとして書き換えない。
TestGatewayResponses と TestGatewayEligibility を追加。履歴を反映した次工程選択、custom結果、明示none/required/個別指定、隠れた履歴、重複名、空/不正JSONについて元要求が保たれることを検証する。

## files

- `internal/proxy/rewrite.go`
- `internal/proxy/rewrite_test.go`
- `internal/plan/plan.go`
- `internal/plan/plan_test.go`

## verify

リポジトリ直下で実行する。以下の名前の回帰テストをこの工程で実装し、実APIへの接続なしで検証する。

```sh
go test ./internal/proxy ./internal/plan -list 'TestGateway(Responses|Eligibility)' | rg '^TestGateway(Responses|Eligibility)'
go test -race -count=1 ./internal/proxy ./internal/plan -run 'TestGateway(Responses|Eligibility)'
go test ./internal/proxy ./internal/plan
```

## depends_on

01

