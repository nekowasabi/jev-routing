# Process 06: 上流JSONとSSEの使用量を逐次収集

## goal

ReverseProxy の応答本文を io.ReadCloser で包装し、読み取られたバイトを変更せず転送しながらJSON/SSEのusageを収集する。Chat、Responses、Anthropicの入力/出力/キャッシュ/推論を正規化し、終端の累積値を重複加算しない。
JSON解析・SSE未完イベント（複数data行の合計）の保持上限は各1MiB。超過・不正・非対応形式は計測不能として中継を続ける。全応答の読了待ちや非同期の無制限複製をしない。content-typeがないSSEも内容から識別し、複数data行・CRLF・分割チャンクを扱う。
EOF/Close/取消/通信失敗のいずれでも要求結果を一度だけ確定。HTTP状態、終了理由、経過時間を記録し、途中で受け取ったusageは部分値と明記する。zeroと欠測、キャッシュ/推論の内数を区別する。
TestGatewayUsage と TestGatewayStreaming を追加。JSON/SSE、改行境界、同じusage反復、欠測、途中切断、上限前後、早期フラッシュ、EOF後Closeでの一回記録を検証する。

## files

- `internal/proxy/usage.go（新規）`
- `internal/proxy/usage_test.go（新規）`
- `internal/proxy/proxy.go`
- `internal/proxy/proxy_test.go`
- `internal/proxy/events.go`

## verify

リポジトリ直下で実行する。以下の名前の回帰テストをこの工程で実装し、実APIへの接続なしで検証する。

```sh
go test ./internal/proxy -list 'TestGateway(Usage|Streaming)' | rg '^TestGateway(Usage|Streaming)'
go test -race -count=1 ./internal/proxy -run 'TestGateway(Usage|Streaming)'
go test ./internal/proxy
```

## depends_on

05
