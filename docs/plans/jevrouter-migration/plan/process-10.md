# Process 10: Jevの稼働と節約効果をグラフで可視化する

## goal

既存ダッシュボードを仕様の画面構成へ更新する。総トークン比較差・完了時間比較差・Jev使用量・適用状況を要約し、ホスト×六分類＋圧縮の状態マップ、選定から成果確認までの件数、比較の積み上げ棒、Jev使用量推移、実行タイムライン、未適用理由の横棒を表示する。上流の状態・能力・選定元・判断時間の集計設計を取り入れる。選定ログだけを実適用成功にしない。

通常語の日本語ラベル、実測／推定／未計測の区別、集計範囲と最終更新を表示する。比較なし・条件不一致・部分欠測はグラフを空の0として描かない。絞込みと詳細表示から判断ID・実操作まで辿れるようにする。SVG／CSSと既存JavaScriptを再利用し、色以外の凡例・表・キーボード操作を備える。ローカルアクセス制限を維持する。

## files

- `internal/proxy/assets/dashboard.html`、`internal/proxy/assets/dashboard.js`
- `internal/proxy/assets/dashboard.mjs`、`internal/proxy/assets/dashboard.test.mjs`
- `internal/proxy/dashboard.go`、`internal/proxy/dashboard_metrics_test.go`
- `internal/proxy/assets/jevrouter-LICENSE.txt`（上流コードを実質転用する場合のみ新規）
- `README.md`（見方・指標の意味・欠測と比較条件）

## verify

```sh
node --test internal/proxy/assets/dashboard.test.mjs
go test ./internal/proxy -count=1
```

固定データで増加・削減・未適用・正常なローカル省略・欠測・比較なしを検証する。既存画面をブラウザーで開き、要約、各グラフ、絞込み、詳細、再接続、狭い画面、キーボード操作を実際に確認する。非専門家向けの五つの問いを画面だけで回答できるか受入確認する。静的テストだけで画面検証済みにしない。今回の工程で表示用の模擬値を使う場合は常にサンプルと表示する。

## depends_on

09。
