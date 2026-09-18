# Proposed PR: nekowasabi/jev-routing-mcp

GitHub 連携の書き込み権限が無いため、こちらからブランチを押せませんでした。ローカルで:

```bash
cd jev-routing-mcp
git checkout -b go-harness
# copy the files from this workspace's harness/ directory
git add go.mod cmd internal examples README.md
git commit -m "Rewrite as a Go proxy: no npx, no MCP, Grok Build + compaction"
git push -u origin go-harness
gh pr create --title "Rewrite as a Go proxy (no npx, Grok Build + compaction)" --body-file PR.md
```

## Why

`npx` + `claude mcp add` / `grok mcp add` はカタログを増やし、`tools[]` は消えない。実効効率が落ちる原因そのもの。

この PR は Node MCP サーバーを **Go のリクエスト書き換えプロキシ** に置き換える。

- `go install github.com/nekowasabi/jev-routing/cmd/jev-routing@latest`
- `jev-routing run grok|claude|codex`
- compaction は tamaratran/fast-jev-compaction と同じ契約（本文は要約しない）
