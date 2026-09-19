# Process 09: 適合する要求への強制選択を追加

## goal

forced設定時に、Jevの実回答が工程04の条件を満たす選択だけを強制する。ローカル採点のみで強制しない。ローカルで十分なら基準filter処理のまま、反映方式を正確に記録する。
対象はChatのfunction、全履歴を持つResponsesの非名前空間function/custom、思考無効かつcache_controlのないAnthropicのfunction。明示tool_choice、previous_response_id、提供元実行、名前空間、画像/未知履歴など選択材料が不足する要求は元要求へ戻す。非JSON経路には適用しない。
同一モデル比較ではfilterと同じ候補限定・圧縮・推論条件を使い、tool_choiceだけをプロトコル別に変更する。disable_parallel_tool_use等の制約を維持し、ツールは実行しない。
上流が拒否した場合はエラーを記録してそのまま返す。自動再送を追加せず、重複副作用の推測に依存しない。
TestGatewayForced を追加。適合/対象外、設定off、実Jev/ローカル、並列制約、上流拒否1要求のみ、モデル不変を模擬上流で検証する。

## files

- `internal/proxy/rewrite.go`
- `internal/proxy/rewrite_test.go`
- `internal/proxy/proxy_test.go`
- `internal/proxy/events.go`
- `README.md`

## verify

リポジトリ直下で実行する。以下の名前の回帰テストをこの工程で実装し、実APIへの接続なしで検証する。

```sh
go test ./internal/proxy -list 'TestGatewayForced' | rg '^TestGatewayForced'
go test -race -count=1 ./internal/proxy -run 'TestGatewayForced'
go test ./internal/proxy
```

## depends_on

08

