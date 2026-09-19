# Process 05: 要求単位の判定履歴とJev実通信を計測

## goal

Server に起動識別子・単調な連番・直近1000件のメモリ履歴を追加する。累積集計と履歴範囲を分け、脱落を識別可能にする。固定長の理由コード、判定元、選択名、実確信度、変更フラグだけを記録する。
受信要求のcontextを Rewrite/Ask/AskCompact へ伝播し、圧縮バッチの実HTTP試行ごとに目的・所要時間・成功/失敗・取得できたusageを記録する。共有Clientに要求固有の可変コールバックを置かず、競合と他要求への混入を防ぐ。既存公開関数の利用箇所を更新または互換ラッパーで保つ。
Engine=live や圧縮バッチ数から通信を推定しない。本文・引数・認証・生エラー・秘密付きURLは保存しない。取得不能usageはnull相当。記録長は文字列200文字以下、イベントには本文由来の無制限配列を持たせない。
TestGatewayEvents と TestGatewayJevTrace を追加。容量境界、同時要求、秘密値、キャッシュhitで通信0、通信失敗、キャンセル後の外部要求中断を確認する。

## files

- `internal/proxy/events.go（新規）`
- `internal/proxy/events_test.go（新規）`
- `internal/proxy/proxy.go`
- `internal/proxy/rewrite.go`
- `internal/jev/jev.go`
- `internal/jev/jev_test.go`

## verify

リポジトリ直下で実行する。以下の名前の回帰テストをこの工程で実装し、実APIへの接続なしで検証する。

```sh
go test ./internal/proxy ./internal/jev -list 'TestGateway(Events|JevTrace)' | rg '^TestGateway(Events|JevTrace)'
go test -race -count=1 ./internal/proxy ./internal/jev -run 'TestGateway(Events|JevTrace)'
go test ./internal/proxy ./internal/jev
```

## depends_on

04

