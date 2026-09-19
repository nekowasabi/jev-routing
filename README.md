# jev-routing

Claude Code / Codex / **Grok Build** / **Cursor Agent CLI** / **Devin CLI** 向けの Jev ハーネス。単一の Go バイナリです。**npx は使いません。MCP サーバーでもありません。** [nekowasabi/jev-routing-mcp](https://github.com/nekowasabi/jev-routing-mcp) の置き換えです。既存リポジトリへは Contents 権限の都合でブランチを押せなかったため、このリポジトリに置きました。

`claude mcp add` / `codex mcp add` / `grok mcp add` / Cursor・Devin の MCP 追加で足すと、ホストの組み込みツールも他の MCP も残ったまま往復が増え、トークンは悪化します。このバイナリはリクエスト前に:

1. 会話の tool 結果を [fast-jev-compaction](https://github.com/tamaratran/fast-jev-compaction) と同じ判定で drop / truncate する（本文は要約しない）
2. Jev に次ツール（Choice）と done（Noul）を同時に聞く
3. そのステップの `tools[]` を **1 スキーマ**（respond ならゼロ）にする
4. thinking / reasoning を落とす

## 入れ方

Node は不要です。Go 1.22+。

```bash
go install github.com/nekowasabi/jev-routing/cmd/jev-routing@latest
```

ソースから:

```bash
git clone https://github.com/nekowasabi/jev-routing.git
cd jev-routing
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
jev-routing run codex             # OpenAI ログインでプロキシへ接続
jev-routing run cursor            # CURSOR_API_ENDPOINT と --endpoint をプロキシへ
jev-routing run devin             # DEVIN_API_URL をプロキシへ
```

`run` はまず `127.0.0.1:8787` を使い、使用中なら空きポートを自動割当します。`JEV_LISTEN` を指定すると、そのアドレスを優先します。

既存の `grok login` / `claude login` / `codex login` / `cursor-agent login` / `devin auth` はそのままです。

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

# Cursor Agent CLI
export CURSOR_API_ENDPOINT=http://127.0.0.1:8787
jev-routing serve --host cursor &
cursor-agent

# Devin CLI
export DEVIN_API_URL=http://127.0.0.1:8787
jev-routing serve --host devin &
devin
```

Codex を手動で起動する場合だけ、`~/.codex/config.toml` に設定します。

```toml
model_provider = "jev"

[model_providers.jev]
name = "jev-routing"
base_url = "http://127.0.0.1:8787/v1"
wire_api = "responses"
requires_openai_auth = true
```


Cursor Agent CLI は既定で `https://api2.cursor.sh` の Connect RPC（`/aiserver`）に送ります。JSON の `tools[]` を含む POST だけを書き換え、protobuf 本体はそのまま上流へ渡します。Devin CLI は `DEVIN_API_URL`（既定 `https://api.devin.ai`）経由の `/messages` を想定しています。

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

| 役割 | Claude Code | Codex | Grok Build | Cursor Agent | Devin CLI |
|---|---|---|---|---|---|
| 読む | Read | read_file | read_file | Read | read |
| 直す | Edit | apply_patch | search_replace | Write | edit |
| 書く | Write | add_file | search_replace | Write | write |
| シェル | Bash | exec_command | run_terminal_command | Shell | exec |
| 検索 | Grep | grep_files | grep | Grep | grep |
| 一覧 | Glob | list_dir | list_dir | Glob | glob |
| サブエージェント | Agent | spawn_agent | spawn_subagent | Task | run_subagent |

MCP プラグイン名（`github_get_pr` など）は共通です。Grok の旧名 `run_terminal_cmd` / `task` は別名として残し、実カタログに無い名前は作りません。

ループバックで待受しているときだけ、読み取り専用の `http://127.0.0.1:<port>/dashboard` を開けます。公開待受では 404 です。画面から設定は変えられません。料金は表示しません。

## 比較実験（既定では無効）

起動時に一度だけ読みます。不正値は起動失敗です。

| 変数 | 値 | 既定 |
|---|---|---|
| `JEV_ROUTING_MODE` | `baseline` / `filter` / `forced` | `filter` |
| `JEV_COMPACTION` | `off` / `on` | `on` |
| `JEV_REASONING` | `preserve` / `legacy` | `legacy` |
| `JEV_ARGS_MODEL` + `JEV_ARGS_TOOLS` | モデル識別子とカンマ区切りの完全一致名 | 空（無効） |
| `JEV_DIRECT_TOOLS` | 無引数/定数引数 Chat function の許可名 | 空（無効） |
| `JEV_RUN_ID` | 比較用 ID | 自動生成 |

`forced` は、検証済みの実 Jev 回答がある要求だけ `tool_choice` を固定します。ローカル採点だけでは強制しません。`JEV_ARGS_MODEL` は `forced` 専用で、許可ツールの送信モデルだけを透過的に差し替えます。価格や互換性は推測しません。`JEV_DIRECT_TOOLS` は `forced` と同時だけ有効で、ARGS_MODEL とは併用できません。対象外・不正スキーマは上流へ戻します。実ツール実行と承認はホストに残します。上流拒否の自動再送はありません。

模擬試験は実ホストの承認互換や実サービスの高速化・費用改善の証拠ではありません。読取/検索と自由記述のコマンド・差分は別課題で評価してください。

## 検証

```bash
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./...
go vet ./...
node --test internal/proxy/assets/dashboard.test.mjs
python3 -m unittest discover -s scripts -p 'test_summarize_x_cell.py'
python3 -m unittest discover -s scripts -p 'test_summarize_selection_comparison.py'
bash scripts/test-x-cell.sh --summarize scripts/testdata/x-cell
```

## 実測比較

通常テストには含めません。各対象を同じコミットから作る別 worktree で 1 回ずつ実行し、素の CLI と `jev-routing` 経由のトークン使用量・経過時間を JSON で保存します。

```bash
make test-x-cell           # Claude Code → Codex → Grok Build → Cursor → Devin
make test-x-cell claude    # 1 製品だけ
```

結果は `artifacts/x-cell/<日時>/<host>/comparison.json` に出ます。`valid: true` の結果だけを比較に使ってください。`jev` 側でプロキシの書換えリクエストが 1 件も観測されなければ `valid: false` となり、削減値は出力しません。請求トークンは独立セッション間のキャッシュ状態で大きく変わるため、単発結果では比較しません。代わりに `routing_request_chars`（実際にプロキシが受け取り上流へ送った JSON 本文の削減バイト数）と、出力トークン・実行時間の差分を記録します。特に ChatGPT ログインで WebSocket を使う Codex は HTTP プロキシを通らない場合があり、その計測値は無効です。

保存済み観測の再集計（外部 CLI / ネットワークなし）:

```bash
bash scripts/test-x-cell.sh --summarize scripts/testdata/x-cell
python3 scripts/summarize_selection_comparison.py scripts/testdata/selection-comparison
```

`CHECK: PASS` の自己申告だけでは成功にしません。費用は単価と出典が揃うときだけ出し、欠測は 0 や削減率に変換しません。比較条件（圧縮・推論・課題）が揃わない群は比較不能です。基準リビジョンが無い選択比較は改善率を出しません。

1 回の差分はモデルの揺れ、プロンプトキャッシュ、サービス混雑の影響を受けます。効果を主張する用途では複数回実行し、各条件の中央値を比較してください。模擬フィクスチャの合格を実測の効率改善とは呼びません。
