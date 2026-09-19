# Process 07: 比較判断用の読み取り専用画面

## goal

既存プロキシへ読み取り専用 `/dashboard` を追加し、工程02の統計をそのまま表示する。標準の `html/template` と埋め込みHTMLを使い、手動更新で十分な画面にする。SPA、外部依存、独立DB、設定変更APIは追加しない。
ローカル/Jev/候補限定/forced/引数モデル/direct/不適合の件数、Jev実通信と目的・入力量、上流モデル・使用量・エラーを表示する。欠落をゼロと表示しない。タスクの再試行・修復・外部成否は工程06の比較結果で確認できるよう、比較JSONを読み取るローカルファイル表示欄を設ける。JSON読込はブラウザ内だけで行い、未知の構造はエラー表示し、本文をHTMLとして挿入しない。
画面はループバック接続限定で提供し、非ローカル接続を拒否する。既存の統計APIの互換性は維持し、新しい詳細データに秘密や本文を含めない。履歴はメモリ上限付きのまま。画面移植を効率改善の実績とは表示しない。
`TestAnalysisDashboard` でレスポンス・項目・欠落値・エスケープ・非ローカル拒否・既存転送への無影響を検証する。比較JSONの整形関数はNode標準テストで固定入力と不正入力を検証する。READMEに利用方法を追記する。

## files

- `internal/proxy/proxy.go`
- `internal/proxy/dashboard.go`（新規）
- `internal/proxy/dashboard_test.go`（新規）
- `internal/proxy/assets/dashboard.html`（新規）
- `internal/proxy/assets/dashboard.mjs`（新規）
- `internal/proxy/assets/dashboard.test.mjs`（新規）
- `README.md`

## verify

```sh
go test ./internal/proxy -list '^TestAnalysisDashboard$' | rg '^TestAnalysisDashboard$'
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./internal/proxy
node --test internal/proxy/assets/dashboard.test.mjs
```

## depends_on

02, 06
