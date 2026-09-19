# Process 07: 要求単位の観測と欠測の明示

## goal

既存の `RewriteStats`、`Server` の `/stats`、終了時JSON保存を拡張し、次の観測を分離する。既存の統計項目は互換性を保ち、新項目を追加する。

- プロキシ到達、変換対象、実変換、素通し理由。バイト差・JSON整形差だけで適用を判定しない。
- ローカル判定、Jev HTTP試行・成功・失敗、キャッシュ再利用、代替処理。`engine=live` は接続設定であり実通信の証拠にしない。
- 要求ごとのJev待機時間と各通信時間の合計。並列通信の合計を壁時計の待機時間と呼ばない。
- 上流ヘッダー受信までの時間と本文読取り終了までの時間。失敗・キャンセル・未完了も区別する。
- 既存の圧縮 `Decision` にある結果IDと保持・切詰め・削除。IDの有効範囲も記録し、本文・認証情報は保存しない。

通信試行は共通の `Ask` で数え、要求に紐づく結果で集計する。並行要求のグローバルカウンター前後差分から要求別通信数を推測しない。
上流JSON/SSEの使用量は確認済みの保存サンプル・固定入力で意味を検証できる項目だけ取得し、元フィールド名と範囲を残す。SSEは全体を貯めず透過転送し、観測が失敗しても本文・順序・認証・状態を変えない。
Jev使用量・費用、ツール時間、親子関係、再取得の因果など未観測値は `null` と理由を持たせ、ゼロと区別する。
`/stats` と終了時保存は同じ整合したスナップショットを使い、保存失敗を報告する。
模擬サーバーで到達のみ、同じ長さの変更、非JSON、カタログなし、ローカル判定、キャッシュ再利用、並列バッチ、通信失敗、使用量欠損、SSEの分割・キャンセル、保存失敗を検証する。

## files

- `internal/proxy/proxy.go`
- `internal/proxy/rewrite.go`
- `internal/proxy/proxy_test.go`
- `internal/proxy/rewrite_test.go`
- `internal/jev/jev.go`
- `internal/jev/jev_test.go`
- `cmd/jev-routing/main.go`
- `cmd/jev-routing/main_test.go`

## verify

```sh
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./internal/proxy ./internal/jev ./cmd/jev-routing
```

## depends_on

01〜06
