# Process 11: 無引数・定数引数だけの直接生成を限定実装

## goal

JEV_DIRECT_TOOLS の完全一致許可リストを追加し、既定空で厳密に無効にする。forced設定・実Jevの有効な選択・非ストリーミング/ストリーミングChat functionに限定する。Responses/Anthropic/customは初期対象外とし工程09の経路へ戻す。ARGS_MODELとの同時有効化は比較を曖昧にするため設定エラー。
スキーマは object、properties/required/additionalProperties:false、注釈title/descriptionのみを許可し、各propertyはスカラーconstだけ、全propertyがrequiredと完全一致するものだけ対応。空propertiesも可。null/object/arrayのconst、enum、自由引数、$ref、allOf/oneOf/anyOf、未知制約、欠落/不正schemaは直接生成しない。定数値にも入力契約の検査を適用し、少なくともschema全体の許可語彙を再帰検査する。
安全なIDを生成し、Chat JSONまたはSSEのtool_calls応答を返す。実ツール実行と承認はクライアントに残す。上流呼出0、Jev使用量は別計上しLLM使用量へ混ぜない。無効/不適合時は上流経路へ戻す。
TestGatewayDirect を追加し、無効設定、許可集合、allOf再現、JSON/SSE組立て、上流0、模擬クライアントの承認前未実行・拒否・承認後結果を伴う次要求を検証する。実ホスト互換性は未保証とREADMEに明記し既定を有効化しない。

## files

- `internal/proxy/direct.go（新規）`
- `internal/proxy/direct_test.go（新規）`
- `internal/proxy/options.go`
- `internal/proxy/options_test.go`
- `internal/proxy/proxy.go`
- `internal/proxy/rewrite.go`
- `internal/proxy/events.go`
- `README.md`

## verify

リポジトリ直下で実行する。以下の名前の回帰テストをこの工程で実装し、実APIへの接続なしで検証する。

```sh
go test ./internal/proxy -list 'TestGatewayDirect' | rg '^TestGatewayDirect'
go test -race -count=1 ./internal/proxy -run 'TestGatewayDirect'
go test ./internal/proxy
```

## depends_on

10

