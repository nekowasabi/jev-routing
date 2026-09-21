# jev-routing

[English version here](README.md)

## これは何か

jev-routing は、コーディングエージェント CLI（Claude Code / Codex / Grok Build / Devin CLI）と上流 LLM API の間に挟むローカルプロキシです。エージェントを置き換えるのではなく、エージェントが送るリクエストをそのまま受け取り、送信直前に整えてから上流へ渡します。

エージェントの CLI は、ターンが進むほど「全ツールのスキーマ」「肥大した tool 実行履歴」「前ステップの thinking」を毎回そのまま送り続けます。この積み上がりが、遅さ・トークン消費・ツール誤選択の主因です。jev-routing はそこに次の 4 つを入れます。

1. **履歴圧縮（compaction）** — `tool_use` / `tool_result` だけを採点し、drop / truncate する。ユーザー文とアシスタント文には触らず、要約もしない
2. **tool 選択の 1 スキーマ化** — Jev に「次のツール」と「完了したか」を同時に問い、そのステップの `tools[]` を 1 スキーマ（応答のみなら 0 個）に絞る
3. **thinking / reasoning の除去** — 次の判断に不要な推論ブロックを落とす
4. **モデル／effort のルーティング** — `route --json` で、難易度とコストから候補ペアのうち足りる範囲で最も安い組を選ぶ

導入して得られるもの:

- **送信バイトとトークンの削減** — 毎リクエストのツールスキーマと肥大した tool 履歴が消える
- **ツール誤選択の低減** — その場で意味のあるツールだけがモデルに見える
- **高い案件に高いモデルを使わずに済む** — 難易度に見合ったモデルと effort が自動で選ばれる
- **可観測性** — ループバック限定の読み取り専用ダッシュボードで、何が書き換えられ、何が適用されなかったのかを確認できる
- **導入コストの低さ** — 単一の Go バイナリ。Node 不要、エージェント側の設定は環境変数 1 つ。既存のログイン情報はそのまま使える
- **安全側の設計** — 不確実なら絞り込まない。未対応の履歴形式は書き換えず通過させる。実ツールの実行と承認はホスト側に残る

---

Claude Code / Codex / **Grok Build** / **Devin CLI** 向けの Jev ハーネス。単一の Go バイナリです。

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
jev-routing run devin             # DEVIN_API_URL をプロキシへ
jev-routing route --json < request.json   # ateam / 診断。モデル選定を JSON で返す
```

`run` はまず `127.0.0.1:8787` を使い、使用中なら空きポートを自動割当します。`JEV_LISTEN` を指定すると、そのアドレスを優先します。

`route --json` は ateam と診断用の選定入口です。`model_mode` / `effort_mode` と候補 `pairs` を渡すと、応答の `model` に適用した組を返します。キーは `model` / `effort` / `source` / `reason_code` / `asked` です。Jev 未接続で候補が複数なら `reason_code` は `no_match` で、`legacy_model` と `effort` の従来値へ戻します。ateam は `ateam auto review` のときだけこの自動選定を使い、`auto` が無いときは名簿の固定値です。選定は `~/.local/state/jev-routing/model-routes.jsonl` に1行追記します。記録先は `JEV_MODEL_LOG` で変えられます。

`ateam auto` のモデル選定は capability 用の `next_tool` 分類器ではなく、専用の `model_pair` 質問です。候補ペアは `difficulty` と `cost` を持ち、Jev は足りる範囲で最も安い／小さい組を選びます。確信度が低く採用できないときは適用は従来値のまま、却下した組を `rejected_id` に残します。

### tmux で起動する

```bash
jev-routing run --tmux codex
```

接続中の tmux 内なら、現在の pane でホストを起動するため、tmux をネストせず pane border は一重のままです。tmux 外、または古い `TMUX` 環境変数だけが残った状態からは、実行ごとに独立した tmux セッションを作成します。

既存の `grok login` / `claude login` / `codex login` / `devin auth` はそのままです。

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


Devin CLI は `DEVIN_API_URL`（既定 `https://api.devin.ai`）の `/messages`・`/sessions` および `prompt`/`message` + `tools[]` JSON を想定しています。Codex ChatGPT ログインは Responses Lite の `input` 内にある `additional_tools` から `functions` 名前空間を展開し、元の位置を保ってローカルツールを絞ります。外部名前空間と提供側の実行ツールは残します。

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

## 対応ツール

プロキシはリクエストに含まれる実行時カタログを正本にし、未知のツールを生成しません。下表は選択ロジックが役割を対応付ける組み込み名です。MCP・Skills・Pluginsが追加するツールは、実行時カタログの名前をそのまま扱います。

| 役割 | Claude Code | Codex | Grok Build | Devin CLI |
|---|---|---|---|---|
| 読み取り | Read | read_file | read_file | read |
| 編集 | Edit | apply_patch | search_replace | edit |
| 書き込み | Write | add_file | write | write |
| シェル | Bash | exec_command | run_terminal_cmd | exec |
| 検索 | Grep / Glob | grep_files / list_dir | grep_search / list_dir | grep / glob |
| Web | WebSearch / WebFetch | web_search / web_fetch | web_search / web_fetch | web_search / webfetch |
| サブエージェント | Agent | spawn_agent | task | run_subagent / read_subagent |
| タスク管理 | TodoWrite | update_plan | todo_write / get_task_output / kill_task | todo_write |
| MCP | ToolSearch / MCPツール | `mcp__<server>__<tool>` | search_tool / use_tool | mcp_list_tools / mcp_call_tool / mcp_read_resource |

### 製品別の範囲

- [Claude Code](https://code.claude.com/docs/en/tools-reference): `tool_use` / `tool_result` の履歴形式を受理します。組み込み名は実行環境・機能フラグで変化するため、固定の許可リストにはしません。
- Codex: `functions.*`、`custom_tool_call`、Responsesの組み込みツールおよびMCP呼び出しの履歴形式を受理します。
- [Grok Build](https://docs.x.ai/build/features/permissions): `read_file`、`search_replace`、`grep_search`、`list_dir`、`run_terminal_cmd`、`web_search`、`web_fetch`、`todo_write`、`task`、`kill_task`、`get_task_output`、`memory_search`、`memory_get`、`search_tool`、`use_tool`、`lsp`、条件付きの`write`を実行時カタログから扱います。
- [Devin CLI](https://docs.devin.ai/cli/reference/permissions#tool-based-permissions): `read`、`write`、`edit`、`apply_patch`、ノートブック、検索、シェル、`webfetch`、タスク、Skills、サブエージェント、権限、MCP管理ツールを実行時カタログから扱います。ATIFエクスポート形式は公開スキーマが確認できるまで履歴判定へ推測追加しません。

履歴形式は、Claudeの`tool_use` / `tool_result`、Codex・Responsesの`*_call`、MCPの`mcp_call`を明示的に受理します。画像を含む履歴は安全側で通過します。

## Dashboard

ループバックで待受しているときだけ、読み取り専用の `http://127.0.0.1:<port>/dashboard` を開けます。公開待受では 404 です。画面から設定は変えられません。料金は表示しません。CORS は付けず、GET 以外は受けません。

```bash
jev-routing run --dashboard grok
```

`run --dashboard` は起動後にブラウザーでダッシュボードを開きます。`serve` のときは同じ URL を手で開きます。画面は現在のプロセスだけを 2 秒間隔で更新します。

- ルーティング概要（判定元・適用の件数）
- 六分類の状態（モデルとeffort、子エージェント、スキル、MCP、CLI、プラグイン、圧縮）。未観測は未観測のまま残す
- 適用一覧。プロキシの書き換えに加え、`route --json` が選んだモデルとeffortを `kind=model` として出す。capability が適用モデル、callId が `jev` / `no_match` などの理由
- 上流レスポンスから集計したトークン消費（入力・出力・キャッシュ・推論）
- 直近のリクエスト（連番、ホスト、判定元、適用、採用ツール、理由、変更、ツール置換、jev、トークン、時間）
- ホスト／判定元／適用の絞込みと行の詳細（判断ID・操作ID）。j/k で行移動、Enter で詳細、r で再接続
- Comparison JSON の貼り付け（ローカル表示のみ。送信しません）

ブラウザー側は最大 1000 件を保持し、表は直近 200 件です。通信が切れたときは最終更新時刻と「接続切れ」を出し、再接続で履歴を取り直します。`?sample=1` は表示確認用の模擬値で、画面にサンプルと出します。

## 比較実験（既定では無効）

起動時に一度だけ読みます。不正値は起動失敗です。

| 変数 | 値 | 既定 |
|---|---|---|
| `JEV_ROUTING_MODE` | `baseline` / `filter` / `forced` | `filter` |
| `JEV_COMPACTION` | `off` / `on` | `on` |
| `JEV_REASONING` | `preserve` / `legacy` | `legacy` |
| `JEV_SELECTION_MODE` | `local` / `jev` / `hybrid` | `hybrid` |
| `JEV_ARGS_MODEL` + `JEV_ARGS_TOOLS` | モデル識別子とカンマ区切りの完全一致名 | 空（無効） |
| `JEV_DIRECT_TOOLS` | 無引数/定数引数 Chat function の許可名 | 空（無効） |
| `JEV_RUN_ID` | 比較用 ID | 自動生成 |
| `JEV_AUTO_APPLY` | `on` / `off` | `off`（導入例の `examples/*.sh` は `on`） |
| `JEV_APPLICATION_POLICY` | `required` / `fallback` | 自動適用を新規に有効にしたときだけ `required` |
| `JEV_KIND_MODES` | `skill=apply,mcp_tool=observe` など | 新種類は `observe`、ateam は `fixed` |

`forced` は、検証済みの実 Jev 回答がある要求だけ `tool_choice` を固定します。ローカル採点だけでは強制しません。`JEV_ARGS_MODEL` は `forced` 専用で、許可ツールの送信モデルだけを透過的に差し替えます。価格や互換性は推測しません。`JEV_DIRECT_TOOLS` は `forced` と同時だけ有効で、ARGS_MODEL とは併用できません。対象外・不正スキーマは上流へ戻します。実ツール実行と承認はホストに残します。上流拒否の自動再送はありません。

`JEV_SELECTION_MODE=local` はローカル規則だけを使い、Jev へ選定を問い合わせません。`jev` は適格な選定を Jev に委譲し、Jev が未設定・不正・不確実・失敗なら候補を絞りません。`hybrid` は確定したローカル規則だけを使い、語一致などの保留は Jev に渡します。Jev 未接続なら候補を絞りません。

通常のプロキシ要求では同じ判断関数が自動で呼ばれ、選定したスキル本文の供給・MCP/CLI 呼出し・結果照合まで進みます。`JEV_AUTO_APPLY=on` のとき種類別モードが `apply` の対象だけを起動し、`required` では未配達・未対応・選定不消費を成功終了にしません。`fallback` は明示指定時だけ従来設定へ戻します。`jev-routing route --json` は ateam と診断用の同じ入口であり、LLM が自発的に呼ぶことは前提にしません。モデル選定の JSON は `model` / `effort` / `reason_code` です。選定ログや候補絞込みだけでは適用完了にしません。不明な実行は自動再送しません。ダッシュボードは可動個所・未適用理由・比較効果を日本語で示します。欠測と比較なしは欠測／比較なしのまま残し、模擬値はサンプルと表示します。

模擬試験は実ホストの承認互換や実サービスの高速化・費用改善の証拠ではありません。読取/検索と自由記述のコマンド・差分は別課題で評価してください。

## 検証

```bash
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./...
go vet ./...
node --test internal/proxy/assets/dashboard.test.mjs
python3 -m unittest discover -s scripts -p 'test_summarize_x_cell.py'
python3 -m unittest discover -s scripts -p 'test_summarize_selection_benchmark.py'
bash scripts/test-x-cell.sh --summarize scripts/testdata/x-cell
```

## 実測比較

通常テストには含めません。各対象を同じコミットから作る別 worktree で 1 回ずつ実行し、素の CLI と `jev-routing` 経由のトークン使用量・経過時間を JSON で保存します。

```bash
make test-x-cell           # Claude Code → Codex → Grok Build → Devin
make test-x-cell claude    # 1 製品だけ
make test-selection-benchmark claude # baseline/local/jev/hybrid を1製品で比較
```

結果は `artifacts/x-cell/<日時>/<host>/comparison.json` に出ます。`comparable: true`（`valid: true`）の結果だけを比較に使ってください。プロキシ未到達、`rewritten=0`（passthrough のみ）、または完了条件不一致は `comparable: false` で、削減値は出しません。請求トークンは独立セッション間のキャッシュ状態で大きく変わるため、単発結果では比較しません。代わりに `routing_request_chars`（実際にプロキシが受け取り上流へ送った JSON 本文の削減バイト数）と、出力トークン・実行時間の差分を記録します。ChatGPT ログインの Codex は `-m gpt-5.6-terra`（`CODEX_MODEL` で上書き）を使います。短名 `terra` は 400 になります。

`make test-selection-benchmark` は、全プロキシ条件で `JEV_COMPACTION=off` と `JEV_REASONING=preserve` を固定します。履歴に未対応の内容型（例: Claude の `tool_addition`）があると `unknown_history` となり、書換えずに通過します。この結果は正常な安全停止であり、外部品質が合格しても選定比較の採点対象にはなりません。`comparison.json` の `invalid_reason` を確認し、対応済みの履歴形式だけで再実行してください。

保存済み観測の再集計（外部 CLI / ネットワークなし）:

```bash
bash scripts/test-x-cell.sh --summarize scripts/testdata/x-cell
python3 scripts/summarize_selection_benchmark.py scripts/testdata/selection-benchmark
```

`CHECK: PASS` の自己申告だけでは成功にしません。費用は単価と出典が揃うときだけ出し、欠測は 0 や削減率に変換しません。比較条件（圧縮・推論・課題）が揃わない群は比較不能です。基準リビジョンが無い選択比較は改善率を出しません。

1 回の差分はモデルの揺れ、プロンプトキャッシュ、サービス混雑の影響を受けます。効果を主張する用途では複数回実行し、各条件の中央値を比較してください。模擬フィクスチャの合格を実測の効率改善とは呼びません。
