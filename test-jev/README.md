# Jev ダッシュボード確認用

5 分間、3 秒ごとに Jev へリクエストします。決済障害、設定不整合、CI 失敗、外部 API 仕様変更という開発シナリオを循環させ、`search`、`read`、`grep`、`write`、`agent`、`mcp`、`terminal`、`browser` と、必要なら未知のツールも選ばせます。7 回ごとに接続不能なローカルアドレスへ送るため、成功と失敗の両方を確認できます。

```sh
TYPESAFE_API_KEY=... go run ./test-jev
```

調整例:

```sh
go run ./test-jev -duration=5m -interval=3s -fail-every=7
```
