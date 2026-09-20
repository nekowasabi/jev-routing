# jev-routing

Claude Code / Codex / **Grok Build** / **Cursor Agent CLI** / **Devin CLI** 向けの Jev ハーネス。単一の Go バイナリです。

このバイナリはリクエスト前に:

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


Cursor Agent CLI は既定で `https://api2.cursor.sh` の Connect RPC（`/aiserver` / `/agent.v1`）に送ります。JSON の `tools[]` または `mcpTools` を含む POST を書き換え、protobuf 本体はそのまま上流へ渡します。Devin CLI は `DEVIN_API_URL`（既定 `https://api.devin.ai`）の `/messages`・`/sessions` および `prompt`/`message` + `tools[]` JSON を想定しています。Codex ChatGPT ログインは Responses Lite の `input` 内にある `additional_tools` から `functions` 名前空間を展開し、元の位置を保ってローカルツールを絞ります。外部名前空間と提供側の実行ツールは残します。

## Compaction

履歴圧縮の判定は [tamaratran/fast-jev-compaction](https://github.com/tamaratran/fast-jev-compaction)（[MIT License](https://github.com/tamaratran/fast-jev-compaction/blob/main/LICENSE)、Copyright (c) 2025）を参考に Go へ移植した。要約はせず、`tool_use` / `tool_result` の drop / truncate 契約を踏襲する。

- ユーザー文とアシスタント文は触らない
- tool_use と tool_result だけを noul で採点
- `keepResult` → 両方残す
- `keepCall` のみ → 結果を先頭 300 字に truncate
- どちらも閾値未満 → 両方 drop
- 先頭と直近はピン留め

ツール選択が不確実でも、安全に適用できる履歴圧縮は実行します。Claude の `system` 境界、署名付き思考、ツール参照、呼び出しと結果の対応は維持します。

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

外部ツール名（`github_get_pr` など）は共通です。Grok の旧名 `run_terminal_cmd` / `task` は別名として残し、実カタログに無い名前は作りません。

## Dashboard

ループバックで待受しているときだけ、読み取り専用の `http://127.0.0.1:<port>/dashboard` を開けます。公開待受では 404 です。画面から設定は変えられません。料金は表示しません。CORS は付けず、GET 以外は受けません。

```bash
jev-routing run --dashboard grok
```

`run --dashboard` は起動後にブラウザーでダッシュボードを開きます。`serve` のときは同じ URL を手で開きます。画面は現在のプロセスだけを 2 秒間隔で更新します。

- ルーティング概要（判定元・適用の件数）
- 上流レスポンスから集計したトークン消費（入力・出力・キャッシュ・推論）
- 直近のリクエスト（連番、判定元、適用、採用ツール、理由、変更、ツール置換、jev、トークン、時間）
- Comparison JSON の貼り付け（ローカル表示のみ。送信しません）

ブラウザー側は最大 1000 件を保持し、表は直近 200 件です。

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

結果は `artifacts/x-cell/<日時>/<host>/comparison.json` に出ます。`comparable: true`（`valid: true`）の結果だけを比較に使ってください。プロキシ未到達、`rewritten=0`（passthrough のみ）、または完了条件不一致は `comparable: false` で、削減値は出しません。請求トークンは独立セッション間のキャッシュ状態で大きく変わるため、単発結果では比較しません。代わりに `routing_request_chars`（実際にプロキシが受け取り上流へ送った JSON 本文の削減バイト数）と、出力トークン・実行時間の差分を記録します。ChatGPT ログインの Codex は `-m gpt-5.6-terra`（`CODEX_MODEL` で上書き）を使います。短名 `terra` は 400 になります。

保存済み観測の再集計（外部 CLI / ネットワークなし）:

```bash
bash scripts/test-x-cell.sh --summarize scripts/testdata/x-cell
python3 scripts/summarize_selection_comparison.py scripts/testdata/selection-comparison
```

`CHECK: PASS` の自己申告だけでは成功にしません。費用は単価と出典が揃うときだけ出し、欠測は 0 や削減率に変換しません。比較条件（圧縮・推論・課題）が揃わない群は比較不能です。基準リビジョンが無い選択比較は改善率を出しません。

1 回の差分はモデルの揺れ、プロンプトキャッシュ、サービス混雑の影響を受けます。効果を主張する用途では複数回実行し、各条件の中央値を比較してください。模擬フィクスチャの合格を実測の効率改善とは呼びません。
