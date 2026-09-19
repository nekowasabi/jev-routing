# Process 07: 読み取り専用ダッシュボードの移植

## goal

gatewayの単体HTML/CSSをGo embedで配信し、GET /dashboard と GET /dashboard/events?since= を追加する。画面は2秒ポーリング、現在プロセスだけ、外部依存なし。取得元は固定同一オリジンとし、画面からの書換APIや他ポート探索は実装しない。
APIは {router:{instanceId,startedAt,now,recorded,oldestSeq},events,historyTruncated} を返す。since省略は0、負数/非整数は400。保持以前のsinceならhistoryTruncated=true。再起動で画面履歴を初期化する。取得失敗を成功/ゼロ件として表示しない。
画面は判定元・反映方式・変更フラグ・通過理由・Jev通信・上流usage/時間・欠測・保持範囲を表示。入力・出力の内数を二重加算しない。料金は表示しない。ブラウザー保持は1000件、表は直近200件、API応答はCache-Control: no-storeとする。テキストはtextContentで描画し、表見出し・ラベル・キーボード操作を備える。
公開待受ではdashboard系を404にする。ループバック待受でもRemoteAddr、Hostのループバック名/IPと待受ポート、Originがある場合の同一オリジンを検証する。CORS許可を付けず、OPTIONSやPOSTは許可しない。中継の既存挙動は変えない。
上流のHTML/LICENSEは記録済みcommitのクローンを参照し、ない場合は同commitを隔離取得して使用する。MIT表示を assets/jev-gateway-LICENSE.txt に保持する。
TestGatewayDashboard を追加し、API値・XSS用文字列・秘密除外・再起動・履歴欠落・不正Origin/Host/公開待受を検証。画面の集計/表示用純粋関数は標準Nodeテストでも確認し、ブラウザーなしでも検証可能にする。

## files

- `internal/proxy/dashboard.go（新規）`
- `internal/proxy/dashboard_test.go（新規）`
- `internal/proxy/assets/dashboard.html（新規）`
- `internal/proxy/assets/dashboard.js（新規）`
- `internal/proxy/assets/dashboard.test.mjs（新規）`
- `internal/proxy/assets/jev-gateway-LICENSE.txt（新規）`
- `internal/proxy/proxy.go`
- `cmd/jev-routing/main.go`
- `README.md`

## verify

リポジトリ直下で実行する。以下の名前の回帰テストをこの工程で実装し、実APIへの接続なしで検証する。

```sh
go test ./internal/proxy ./cmd/jev-routing -list 'TestGatewayDashboard' | rg '^TestGatewayDashboard'
go test -race -count=1 ./internal/proxy ./cmd/jev-routing -run 'TestGatewayDashboard'
go test ./internal/proxy ./cmd/jev-routing
node --test internal/proxy/assets/dashboard.test.mjs
```

## depends_on

06
