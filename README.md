# jev-routing

Claude Code / Codex / **Grok Build** 向けの Jev ハーネス。単一の Go バイナリです。**npx は使いません。MCP サーバーでもありません。** [nekowasabi/jev-routing-mcp](https://github.com/nekowasabi/jev-routing-mcp) の置き換えです。既存リポジトリへは Contents 権限の都合でブランチを押せなかったため、このリポジトリに置きました。

`claude mcp add` / `codex mcp add` / `grok mcp add` で足すと、ホストの組み込みツールも他の MCP も残ったまま往復が増え、トークンは悪化します。このバイナリはリクエスト前に:

1. 会話の tool 結果を [fast-jev-compaction](https://github.com/tamaratran/fast-jev-compaction) と同じ判定で drop / truncate する（本文は要約しない）
2. Jev に次ツール（Choice）と done（Noul）を同時に聞く
3. そのステップの `tools[]` を **1 スキーマ**（respond ならゼロ）にする
4. thinking / reasoning を落とす

## 入れ方

Node は不要です。Go 1.22+。

```bash
go install github.com/nekowasabi/jev-routing-go/cmd/jev-routing@latest
```

ソースから:

```bash
git clone https://github.com/nekowasabi/jev-routing-go.git
cd jev-routing-go
go install ./cmd/jev-routing
```

キーは任意。無いときはオンデバイスの分類器です。

```bash
export TYPESAFE_API_KEY=ts_...    # https://console.typesafe.ai/settings/keys
```

## 起動

```bash
jev-routing run grok              # GROK_CLI_CHAT_PROXY_BASE_URL をプロキシへ
jev-routing run claude            # ANTHROPIC_BASE_URL をプロキシへ
jev-routing run codex             # Responses プロバイダ
```

既存の `grok login` / `claude login` / `codex login` はそのままです。

手で環境を書く場合:

```bash
# Grok Build
unset XAI_API_KEY GROK_MODELS_BASE_URL
export GROK_CLI_CHAT_PROXY_BASE_URL=http://127.0.0.1:8787/v1
jev-routing serve --host grok &
grok

# Claude Code
unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787
jev-routing serve --host claude &
claude
```

Codex は `~/.codex/config.toml`:

```toml
model_provider = "jev"

[model_providers.jev]
name = "jev-routing"
base_url = "http://127.0.0.1:8787/v1"
wire_api = "responses"
requires_openai_auth = true
```

## やらないこと

```bash
# カタログが増えるだけ。tools[] は消えない
claude mcp add jev-routing -- npx -y jev-routing-mcp
codex mcp add jev-routing -- npx -y jev-routing-mcp
grok mcp add jev-routing -- npx -y jev-routing-mcp
```

Grok の `PreCompact` / Claude の `PreToolUse` は、モデルが全スキーマを見たあとです。拒否はできてもカタログは剥がせません。

## Compaction

`tamaratran/fast-jev-compaction` と同じ契約です。

- ユーザー文とアシスタント文は触らない
- tool_use と tool_result だけを noul で採点
- `keepResult` → 両方残す
- `keepCall` のみ → 結果を先頭 300 字に truncate
- どちらも閾値未満 → 両方 drop
- 先頭と直近はピン留め

```bash
jev-routing compact < transcript.json
```

## ホストの組み込み名

| 役割 | Claude Code | Codex | Grok Build |
|---|---|---|---|
| 読む | Read | read_file | read_file |
| 直す | Edit | apply_patch | search_replace |
| 書く | Write | add_file | search_replace |
| シェル | Bash | exec_command | run_terminal_cmd |
| 検索 | Grep | grep_files | grep |
| 一覧 | Glob | list_dir | list_dir |

MCP プラグイン名（`github_get_pr` など）は共通です。

## 検証

```bash
go test ./...
jev-routing bench --host grok
```
