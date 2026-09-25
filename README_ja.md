# jev-routing

[English version here](README.md)

## これは何か

jev-routing は、コーディングエージェント CLI（Claude Code / Codex / Grok Build / Devin CLI）と上流 LLM API の間に挟むローカルプロキシです。エージェントを置き換えるのではなく、エージェントが送るリクエストをそのまま受け取り、送信直前に整えてから上流へ渡します。

エージェントの CLI は、ターンが進むほど「全ツールのスキーマ」「肥大した tool 実行履歴」「前ステップの thinking」を毎回そのまま送り続けます。この積み上がりが、遅さ・トークン消費・ツール誤選択の主因です。jev-routing はそこに次の 4 つを入れます。

1. **履歴圧縮（compaction）** — `tool_use` / `tool_result` だけを採点し、drop / truncate する。ユーザー文とアシスタント文には触らず、要約もしない
2. **tool 選択の 1 スキーマ化** — Jev に「次のツール」と「完了したか」を同時に問い、そのステップの `tools[]` を 1 スキーマ（応答のみなら 0 個）に絞る
3. **thinking / reasoning の除去** — 次の判断に不要な推論ブロックを落とす

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

キーは任意です。ただし `JEV_SELECTION_MODE=jev` のときは必須で、無いと起動しません。キーが無いとき、`hybrid` と `local` はオンデバイスの分類器を使います。

```bash
export TYPESAFE_API_KEY=ts_...    # https://console.typesafe.ai/settings/keys
```

## 起動

```bash
jev-routing run grok              # GROK_CLI_CHAT_PROXY_BASE_URL をプロキシへ
jev-routing run claude            # ANTHROPIC_BASE_URL をプロキシへ
jev-routing run codex             # OpenAI ログインでプロキシへ接続
jev-routing run devin             # DEVIN_API_URL をプロキシへ
```

`run` はまず `127.0.0.1:8787` を使い、使用中なら空きポートを自動割当します。`JEV_LISTEN` を指定すると、そのアドレスを優先します。

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

## ベンチマーク

`jev-routing bench` は既存のチェス課題に加え、`x-cell` の読取、２ツール、スキル、子セッションの課題を共通ランナーで扱います。チェス課題は [jev-gateway-bench](https://github.com/vinilana/jev-gateway-bench)（MIT、Copyright (c) 2026 Vinicius Lana）から移植しました。通常の選択比較では on は `JEV_ROUTING_MODE=filter`（`--on-mode forced` も可）、off は `baseline` です。Codex の圧縮閾値比較は両方 `baseline` で、ホストの閾値だけを変えます。`direct` はプロキシを通さない接続確認で、総使用量を保証できないため削減量は比較不能です。実行ごとに新しいワークスペースと採点器を使います。

```bash
jev-routing bench --list
jev-routing bench selftest
jev-routing bench --agent fake --tasks chess-bugfix --reps 1
jev-routing bench --agent codex --tasks chess-bugfix --reps 1
JEV_SELECTION_MODE=jev jev-routing bench --agent claude --model claude-sonnet-5 --effort medium --tasks dual-facts --catalog 2 --reps 2
JEV_SELECTION_MODE=jev jev-routing bench --agent claude --model claude-sonnet-5 --effort medium --tasks skill-proof --reps 1
JEV_SELECTION_MODE=jev jev-routing bench --agent claude --model claude-sonnet-5 --effort medium --tasks child-facts --reps 1
JEV_SELECTION_MODE=jev jev-routing bench --agent claude --model claude-sonnet-5 --effort medium --tasks xcell-module --modes direct,off,on
jev-routing bench --agent codex --model gpt-5.6-terra --effort medium --tasks compact-facts --modes off,on --codex-compact-baseline-limit 900000 --codex-compact-limit 55000 --reps 6
jev-routing bench report results/<dir> --prices 1.25,0.125,10
```

エージェントは `codex`、`claude`、`grok`、`devin`、`fake` です。本物のエージェントはクォータを消費します。まずは課題を一つ、`--reps 1` から始めてください。`--agent fake` はプロキシに数回リクエストを送り、参照実装を書き込むので、モデルなしで一連の流れを確認できます。実 Jev の介入条件には TypeSafe の鍵が必要です。端末上の分類器を測るときは `JEV_SELECTION_MODE=local` にしてください。チェス課題の採点には Node.js が必要です。プロキシ自体は Node を必要としません。

`--prices` は 100 万トークンあたりの USD を `in,cached,out[,cachewrite]` で受け取ります。cache write を省くと input の 1.25 倍とみなし、`claude` にだけ適用します。入力トークン数はキャッシュ込みです。`claude` では Anthropic が input と別に報告する cache read と cache write を足し、`codex` と `grok` では cached が input に含まれています。

成果の合格と `comparison.json` の比較可能なペアの総トークン差を主に確認し、`summary.md` の中央値、非キャッシュ入力・費用・経過時間は補助情報とします。`compact-facts` は20ファイルの全文読取と回答を外部検証し、両条件の Jev 選択・圧縮置換を止めて Codex 自身の文脈上限設定を測ります。圧縮要求が起きないランも設定比較に含め、件数を別に記録します。入力本文のバイト差やキャッシュを除いた入力だけでは、タスク全体の削減を判定しません。

結果は `results/<timestamp>/` に出ます（`runs.jsonl`、`comparison.json`、`summary.md`、実行ごとのディレクトリ）。`comparison.json` は品質・Jev 適用・必要ツール結果・使用量が揃うペアだけに、対照と介入の総トークンおよび差分を記録します。総トークン（`baselineTokens`／`selectionTokens`／`savedTokens`、および `summary.md` の「Total tokens (upstream), median」）は、親・子エージェント CLI の上流入力（キャッシュ込み）＋上流出力だけを数えます。Jev 自身の入出力（`jevInput`／`jevOutput`）は別項目として記録し、この総量および効果判定には含めません。Jev 自身の使用量が欠測しても、そのペアを比較不能にはしません。欠測や不合格は理由付きの比較不能とし、削減量を空欄にします。Claude Code／Codex は CLI の主モデル使用量とプロキシのモデル別使用量も照合します。子セッション課題では親の CLI 使用量と要求ごとの使用量が一意に対応するときだけ子の総量を出します。Codex の自動承認審査など、CLI のターン使用量に入らない追加モデル要求もプロキシの総量に含めます。Grok Build のキャンセル応答は完全な CLI 集計で照合できる場合のみセッション総量を使い、要求別の欠測をゼロで埋めません。Devin CLI の Connect 応答に使用量がない場合は、検証済みの ATIF 手順合計をセッション総量として使えます。子の使用量・モデルを帰属できないランは比較不能です。実行ごとの `proxy-events.json` は原因調査用のローカル記録です。`bench audit` はエージェントログを読み直します。

## Compaction

履歴圧縮の判定は [tamaratran/fast-jev-compaction](https://github.com/tamaratran/fast-jev-compaction)（[MIT License](https://github.com/tamaratran/fast-jev-compaction/blob/main/LICENSE)、Copyright (c) 2025）を参考に Go へ移植した。要約はせず、`tool_use` / `tool_result` の drop / truncate 契約を踏襲する。Jev に見せる state は現行ライブラリと同じ段階で収める。ツール入力を 1000、200、60 文字へ縮め、長い本文を頭と末尾だけにし、古い本文を畳み、古い呼び出しを1行にし、呼び出しを持たない古い項目を落とす。

- ユーザー文とアシスタント文は触らない
- tool_use と tool_result だけを noul で採点
- `keepResult` → 両方残す
- `keepCall` のみ → 結果を先頭 300 字に truncate
- どちらも閾値未満 → 両方 drop
- 先頭と直近はピン留め

Codex と Grok Build は、Claude Code の `session.compact` のように `PreCompact` から置換トランスクリプトを返せない。

- Codex のローカル圧縮（`codex-rs/core/src/compact.rs`）は、provider が OpenAI / Azure でないとき（`RemoteCompactionSupport::Unsupported`）に動く。`CONTEXT CHECKPOINT COMPACTION` の要約を求め、直近のユーザーメッセージとその要約だけを残す。ツール結果は置換後の履歴項目にならない。
- Grok Build の full-replace（`xai-grok-compaction` の `code_compaction`）は `[system, user prefix, AGENTS.md, 最後のクエリ, 直近の尾, summary]` を組み直す。summary は番号付き節の `<summary>` で、掃除後 500 文字未満は退化する。それより古いツール呼び出しは summary の中にしか残らない。

Codex の圧縮要求は既定でホスト自身の要約処理へ転送する。実験用の `JEV_CODEX_NATIVE_COMPACTION=on` を指定すると、残した履歴による置換を有効にする。返却文が元の履歴より 25% 以上短くならない場合はホストの要約処理へ転送する。Grok の圧縮要求には残した履歴を `<summary>` ブロックで返す。Claude は Claude Code 自身が圧縮要求を送ったときだけ残した履歴を `<summary>` で返し、削減が 25% 未満ならホストの要約処理へ転送する。通常ターンの履歴は圧縮しない。

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

- [Claude Code](https://code.claude.com/docs/en/tools-reference): `tool_use` / `tool_result` の履歴形式を受理します。組み込み名は実行環境・機能フラグで変化するため、固定の許可リストにはしません。ツール定義は Anthropic のプロンプトキャッシュの先頭に位置するため、Claude ではツールカタログを絞らず、判定を最後のツール結果メッセージへリマインダーとして追記します。
- Codex: `functions.*`、`custom_tool_call`、Responsesの組み込みツールおよびMCP呼び出しの履歴形式を受理します。
- [Grok Build](https://docs.x.ai/build/features/permissions): `read_file`、`search_replace`、`grep_search`、`list_dir`、`run_terminal_cmd`、`web_search`、`web_fetch`、`todo_write`、`task`、`kill_task`、`get_task_output`、`memory_search`、`memory_get`、`search_tool`、`use_tool`、`lsp`、条件付きの`write`を実行時カタログから扱います。
- [Devin CLI](https://docs.devin.ai/cli/reference/permissions#tool-based-permissions): `read`、`write`、`edit`、`apply_patch`、ノートブック、検索、シェル、`webfetch`、タスク、Skills、サブエージェント、権限、MCP管理ツールを実行時カタログから扱います。ATIFエクスポート形式は公開スキーマが確認できるまで履歴判定へ推測追加しません。

履歴形式は、Claudeの`tool_use` / `tool_result`、Codex・Responsesの`*_call`、MCPの`mcp_call`を明示的に受理します。画像を含む履歴は安全側で通過します。

## Dashboard

ループバックで待受しているときだけ、読み取り専用の `http://127.0.0.1:<port>/dashboard` を開けます。公開待受では 404 です。画面から設定は変えられません。料金は表示しません。CORS は付けず、GET 以外は受けません。

```bash
jev-routing run --dashboard grok
JEV_SELECTION_MODE=local jev-routing serve --host codex --listen 127.0.0.1:8787
```

`run --dashboard` は起動後にブラウザーでダッシュボードを開きます。`serve` のときは同じ URL を手で開きます。先頭のベンチマーク欄で `results/<timestamp>/comparison.json` を選ぶと、削減量・品質・比較可能件数と課題別の理由を表示します。ファイルは送信されません。下段のライブ表示は現在のプロセスだけを 2 秒間隔で更新します。

- ルーティング概要（判定元・適用の件数）
- 六分類の状態（モデルとeffort、子エージェント、スキル、MCP、CLI、プラグイン、圧縮）。未観測は未観測のまま残す
- 適用一覧。プロキシの書き換え（子エージェント、スキル、MCP、CLI、プラグイン）を対象とする
- 上流レスポンスから集計したトークン消費（入力・出力・キャッシュ・推論）
- 直近のリクエスト（連番、ホスト、判定元、適用、採用ツール、理由、変更、ツール置換、jev、トークン、時間）
- ホスト／判定元／適用の絞込みと行の詳細（判断ID・操作ID）。j/k で行移動、Enter で詳細、r で再接続
- 保存済み `comparison.json` の読み込み（ローカル表示のみ。送信しません）

ブラウザー側は最大 1000 件を保持し、表は直近 200 件です。通信が切れたときは最終更新時刻と「接続切れ」を出し、再接続で履歴を取り直します。`?sample=1` は表示確認用の模擬値で、画面にサンプルと出します。

## 比較実験（既定では無効）

起動時に一度だけ読みます。不正値は起動失敗です。

| 変数 | 値 | 既定 |
|---|---|---|
| `JEV_ROUTING_MODE` | `baseline` / `filter` / `forced` | `filter` |
| `JEV_COMPACTION` | `off` / `on` | `on` |
| `JEV_CODEX_NATIVE_COMPACTION` | `off` / `on` | `off`（実験用。既定では Codex 自身が要約する） |
| `JEV_CODEX_TOOL_OUTPUT_TRUNCATE` | `off` / `on` | `on`（Codex のツール結果のうち20000バイトを超えるものを、先頭・末尾を残して中央を省略する。毎要求、再送される履歴全件に同じ規則で適用し、プロンプトキャッシュの接頭辞を保つ） |
| `JEV_REASONING` | `preserve` / `legacy` | `legacy` |
| `JEV_SELECTION_MODE` | `local` / `jev` / `hybrid` | `hybrid` |
| `JEV_SHADOW` | `on` / `off` | `off` |
| `JEV_CLAUDE_ADVISE` | `on` / `off` | `off`（Claude の advise 経路は Jev を呼ばず要求を無変更で通す） |
| `JEV_CLAUDE_CLEAR_TOOL_USES` | `on` / `off` | `off`（Claude の要求に `clear_tool_uses_20250919`（[context editing](https://platform.claude.com/docs/en/build-with-claude/context-editing)）を追記する） |
| `JEV_CLAUDE_CLEAR_TRIGGER` | `input_tokens` の整数 | `100000` |
| `JEV_CLAUDE_CLEAR_AT_LEAST` | `input_tokens` の整数 | `40000` |
| `JEV_CLAUDE_CLEAR_KEEP` | `tool_uses` の整数 | `3` |
| `JEV_CLAUDE_CLEAR_EXCLUDE` | カンマ区切りのツール名 | 空（`exclude_tools` を出力しない） |
| `JEV_CLAUDE_CLEAR_GATE` | `off` / `jev` | `off`（`jev`: 会話の直近の文脈量が trigger 以上かつ要求中の消去可能なツール結果の推定トークンが `clear_at_least` 以上になった時点で、過去のツール出力を再び必要とするかを Jev に一度だけ問い、不要な場合のみ edit を追記する。Jev の失敗やキー未設定時は消去しない） |
| `JEV_TRANSFORMS` | `compact=on/off,filter=on/off,criteria=on/off` | `compact=on,filter=on,criteria=off` |
| `JEV_COST_GATE_MAX` | 0 以上の整数 | `3` |
| `JEV_ARGS_MODEL` + `JEV_ARGS_TOOLS` | モデル識別子とカンマ区切りの完全一致名 | 空（無効） |
| `JEV_DIRECT_TOOLS` | 無引数/定数引数 Chat function の許可名 | 空（無効） |
| `JEV_RUN_ID` | 比較用 ID | 自動生成 |
| `JEV_AUTO_APPLY` | `on` / `off` | `off`（導入例の `examples/*.sh` は `on`） |
| `JEV_APPLICATION_POLICY` | `required` / `fallback` | 自動適用を新規に有効にしたときだけ `required` |
| `JEV_KIND_MODES` | `skill=apply,mcp_tool=observe` など | 新種類は `observe`、ateam は `fixed` |

`forced` は、検証済みの実 Jev 回答がある要求だけ `tool_choice` を固定します。ローカル採点だけでは強制しません。`JEV_ARGS_MODEL` は `forced` 専用で、許可ツールの送信モデルだけを透過的に差し替えます。価格や互換性は推測しません。`JEV_DIRECT_TOOLS` は `forced` と同時だけ有効で、ARGS_MODEL とは併用できません。対象外・不正スキーマは上流へ戻します。実ツール実行と承認はホストに残します。上流拒否の自動再送はありません。

`JEV_SELECTION_MODE=local` はローカル規則だけを使い、Jev へ選定を問い合わせません。`jev` は適格な選定を Jev に委譲し、Jev が未設定・不正・不確実・失敗なら候補を絞りません。`hybrid` は確定したローカル規則だけを使い、語一致などの保留は Jev に渡します。Jev 未接続なら候補を絞りません。

`JEV_SHADOW=on` は候補集合を採点しますが、リクエストは書き換えません。`JEV_TRANSFORMS` は compaction、ツールカタログの絞り込み、対比 criteria を個別に on/off します。criteria は混乱ペアが登録されるまで off のままです。`JEV_COST_GATE_MAX` は候補数がこの値以下のとき分類器を飛ばします。

通常のプロキシ要求では同じ判断関数が自動で呼ばれ、選定したスキル本文の供給・MCP/CLI 呼出し・結果照合まで進みます。`JEV_AUTO_APPLY=on` のとき種類別モードが `apply` の対象だけを起動し、`required` では未配達・未対応・選定不消費を成功終了にしません。`fallback` は明示指定時だけ従来設定へ戻します。選定ログや候補絞込みだけでは適用完了にしません。不明な実行は自動再送しません。ダッシュボードは可動個所・未適用理由・比較効果を日本語で示します。欠測と比較なしは欠測／比較なしのまま残し、模擬値はサンプルと表示します。

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

結果は `artifacts/x-cell/<日時>/<host>/comparison.json` に出ます。各ホストは `~/.local/state/jev-routing/x-cell.jsonl` にも1行追記します（記録先は `JEV_XCELL_LOG` で変えられます）。行にはコミット、各モードの時間とトークン数、比較できるときは削減値を残すので、繰り返し実行した結果を時系列で比べられます。比較不能な実行も残します。`comparable: true`（`valid: true`）の結果だけを比較に使ってください。プロキシ未到達、`rewritten=0`（passthrough のみ）、または完了条件不一致は `comparable: false` で、削減値は出しません。請求トークンは独立セッション間のキャッシュ状態で大きく変わるため、単発結果では比較しません。代わりに `routing_request_chars`（実際にプロキシが受け取り上流へ送った JSON 本文の削減バイト数）と、出力トークン・実行時間の差分を記録します。ChatGPT ログインの Codex は `-m gpt-5.6-terra`（`CODEX_MODEL` で上書き）を使います。短名 `terra` は 400 になります。

`make test-selection-benchmark` は、全プロキシ条件で `JEV_COMPACTION=off` と `JEV_REASONING=preserve` を固定します。履歴に未対応の内容型（例: Claude の `tool_addition`）があると `unknown_history` となり、書換えずに通過します。この結果は正常な安全停止であり、外部品質が合格しても選定比較の採点対象にはなりません。`comparison.json` の `invalid_reason` を確認し、対応済みの履歴形式だけで再実行してください。

保存済み観測の再集計（外部 CLI / ネットワークなし）:

```bash
bash scripts/test-x-cell.sh --summarize scripts/testdata/x-cell
python3 scripts/summarize_selection_benchmark.py scripts/testdata/selection-benchmark
```

`CHECK: PASS` の自己申告だけでは成功にしません。費用は単価と出典が揃うときだけ出し、欠測は 0 や削減率に変換しません。比較条件（圧縮・推論・課題）が揃わない群は比較不能です。基準リビジョンが無い選択比較は改善率を出しません。

1 回の差分はモデルの揺れ、プロンプトキャッシュ、サービス混雑の影響を受けます。効果を主張する用途では複数回実行し、各条件の中央値を比較してください。模擬フィクスチャの合格を実測の効率改善とは呼びません。

## Claude Code

[起動](#起動) のとおり `jev-routing run claude` で起動します。既定では通常の Claude 要求をそのまま転送し、Jev は呼びません。`JEV_CLAUDE_ADVISE=on` で以前のツール選択助言を再度有効にできますが、トークンは減りません（後述）。

Claude Code で残した削減手段は Anthropic ネイティブの [context editing](https://platform.claude.com/docs/en/build-with-claude/context-editing) だけです。`JEV_CLAUDE_CLEAR_TOOL_USES=on` にすると、プロキシが `clear_tool_uses_20250919` edit を追記し、古いツール結果がサーバー側で消去されます。Claude Code が送る `clear_thinking_20251015` edit はそのまま残します。サブスクリプション（claude.ai）ログインで動作します。既定では無効です。変数の一覧は [比較実験](#比較実験既定では無効) にあります。各実験の詳細は [docs/MEMO.md](docs/MEMO.md) を参照してください。

推奨設定:

| 変数 | 値 | 備考 |
|---|---|---|
| `JEV_CLAUDE_CLEAR_TOOL_USES` | `on` | edit を有効にする |
| `JEV_CLAUDE_CLEAR_GATE` | `jev` | 消去するかを会話ごとに一度だけ Jev が判断する。Jev キーが必要で、無ければ消去しない（fail closed） |
| `JEV_CLAUDE_CLEAR_TRIGGER` | `100000` | 既定値 |
| `JEV_CLAUDE_CLEAR_AT_LEAST` | `40000` | 既定値 |
| `JEV_CLAUDE_CLEAR_KEEP` | `3` | 既定値 |

```bash
unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787
JEV_CLAUDE_CLEAR_TOOL_USES=on JEV_CLAUDE_CLEAR_GATE=jev jev-routing serve --host claude &
claude
```

ベンチマークで測る場合（ベンチの文脈は 100k に届かないため閾値を下げます）:

```bash
jev-routing bench --agent claude --tasks chess-bugfix --modes off,on --reps 6 \
  --claude-clear --claude-clear-gate jev --claude-clear-trigger 30000 --claude-clear-at-least 10000
```

- `--user-tools` は利用者の実際のツール構成で実行します。利用者の hook を動かさないよう `--no-hooks` を併用してください。
- 課題 `child-survey` はサブエージェントが 8 個のソースファイルを読み、最後に報告します。サブエージェント内の消去を試すための課題です。
- `CLAUDE_CODE_SUBAGENT_MODEL` は環境から引き継がれ、記録されます。比較可能な実行にするには固定してください（例: `claude-sonnet-5`）。
- Linux で `bwrap` が使える場合、`bench` はエージェントごとに専用の `/tmp` を用意します。

### Claude Code でツール呼び出しの置き換えを断念した理由

Claude Code は extended thinking を有効にして動き、Anthropic API は thinking 中の `tool_choice` 強制を拒否します。ツール一覧や `tool_choice` を変えるとプロンプトキャッシュも壊れます。残るのは次のツールを誘導ではなく助言することだけで、助言は上流の要求や履歴の大きさを減らせません。増えるのはターンごとに積み上がる Jev の判定コストだけです。

| 調査 | 方法 | 結果 |
|---|---|---|
| 助言の構造的コスト | `dual-facts`（経路固定の課題）、claude-sonnet-5/medium、6 ペア | 総トークン中央値 −27.25%（増加）。増加分の 99% が Jev の判定コスト |
| Jev コストの削減 | 助言を付けられない要求で判定を省略、候補説明を短縮 | −6.20% まで改善したが依然として純増。最良でも基準と同等 |
| 実セッションでの置き換え余地 | 実際の Claude Code 200 ターンに同じ判定呼び出しを再現 | 実セッションの Jev 入力は中央値 7,406 トークン。どの閾値でも純減はマイナス |
| Jev を使わない決定的な合成 | 実セッションに対するルールベース合成 | 正解率 17.9%、削減上限 0.28% |
| 他製品ベンチとの照合 | [jev-gateway](https://github.com/vinilana/jev-gateway#benchmark) の `hint` 方式 | 作者自身が Claude Code の結果を「コストは下がらない」と記載 |

### context editing で試したこと

指標は同一経路の純削減です。要求の入力から消去したトークンから、消去で増えたキャッシュ書き込みトークンと手戻りを差し引きます。手戻りは、実際に消去された呼び出しを再取得し、同一内容が返ったものだけを数えます（以前の数値はテストの再実行も数えていたため、保守的な値です）。実行間の総トークン比較はどの系列でも A/A 相当のノイズだったため、根拠に使っていません。

1. **サーバーの挙動を実測。** `clear_tool_uses` は要求ごとに再計算され、対象となる最も古い結果から `clear_at_least` に達するまでだけ消去します。そのため 1 要求あたりの削減はおよそ `clear_at_least` が上限です。
2. **ベンチ v1**（`chess-bugfix`、trigger 30000 / at-least 10000 / keep 3）: 同一経路の純削減の中央値は 12.0%（12 ペア）と 8.5%（事前登録した確認系列）。品質は全実行で合格。
3. **実作業は条件が違う。** 実際のメインセッション 569 件（30 日）では、最初の要求の時点で中央値 81,574 トークンの固定プレフィックス（システムプロンプト、ツール定義、エージェントとスキルの一覧）があります。ツール結果は最終文脈の中央値 3.8% です。メインセッションのオフライン再生による上限は約 3% です。利用者の実際のツール構成（`--user-tools --no-hooks`、chess-engine）での試行では一度も消去されませんでした。
4. **プレフィックスの縮小は不採用。** 使っていないエージェントやプラグインを無効にするとプレフィックスは 21% 減りましたが、利用者が使うかもしれない機能を外すのは jev-routing の役割ではありません。
5. **サブエージェントはメインと同程度に消費する。** 30 日でサブエージェント 1,173M、メイン 1,223M トークンで、文脈の大半はツール結果です（最初の要求の中央値 35k、文脈の中央値 80k）。オフライン上限はサブエージェントのトークンの約 10% です。
6. **ゲートなしのサブエージェントは失敗。** `child-survey`、6 ペア、claude-sonnet-5/medium、60000/40000: 効果なし。消去により再読み込みが起き、1 回の実行で手戻りが最大 699,727 トークンに達しました。事前登録した判定は不合格でした。
7. **Jev ゲート**（`JEV_CLAUDE_CLEAR_GATE=jev`）。会話ごとに一度だけ（親と各サブエージェントは別々に）、会話の文脈が trigger 以上かつ要求中の消去可能なツール結果の推定が `clear_at_least` 以上になった時点で、課題文とツール呼び出し履歴（ツール結果は含めない）から Jev が `clear_old_results` か `keep_all_results` を選びます。edit を途中で外すとキャッシュが壊れるため、「clear」はその会話の最後まで維持します。エラー時は消去しません。事前登録、30000/10000 で 2 × 6 ペア:

| 課題 | ゲートの判断 | 同一経路の純削減 | 手戻り | 品質 |
|---|---|---|---|---|
| `child-survey` | keep 6/6 | 消去なし | なし | 16/16 |
| `chess-bugfix` | clear 6/6 | 消去が起きた 5 回で中央値 11.1%（2.9%–14.2%） | 6 回中 1 回で 34,344、他は 0 | 36/36 |

ゲートのコストは会話ごとに入力約 800–1,400 + 出力 38 トークン（実行全体の 0.1–0.2%）です。

### 推奨設定での期待効果

実セッション 30 日分に対するオフライン推定です。ゲートが常に消去を選び、手戻りが無いと仮定した上限値です。

| | 全体の削減 | 消去が発生するセッション | そのうちの最小 / 中央値 / 最大 |
|---|---|---|---|
| メイン（569） | 2.8% | 6% | 2.0% / 10.6% / 20.0% |
| サブエージェント（583） | 9.9% | 22% | 1.4% / 15.8% / 30.2% |
| 合計 | ≈6.3% | — | セッション単位の中央値は 0%（大半のセッションは消去されない） |

価格で重み付けすると（キャッシュ読み込み 0.1、1 時間キャッシュ書き込み 2.0）、推奨設定は約 +0.5% で、費用はほぼ中立です。

### 制約

- ベンチ課題は 2 つ、モデルは 1 つ（claude-sonnet-5、medium）だけです。
- ゲートが実作業の課題に一般化するかは未検証です。
- 既定の trigger 100000 での「clear」経路は実環境で未検証です。ベンチの文脈はそこまで届きません。
- 同一経路の指標は、消去しなくても経路が変わらないことを前提にしています。
- 手戻りは同一内容の再取得だけを数えるため、下限値です。

## Codex

`jev-routing run codex` で起動します。Codex で既定で有効な削減は、大きなツール結果の切り詰めだけです。`JEV_CODEX_TOOL_OUTPUT_TRUNCATE=off` で無効にできます。ツール選択と Jev による圧縮置換は、下記の経緯から既定ではトークン削減に寄与しません。各実験の詳細は [docs/MEMO.md](docs/MEMO.md) を参照してください。

### ツール結果の切り詰め（既定で有効）

Codex の要求に含まれるツール結果（`function_call_output` など）のうち 20,000 バイトを超えるものを、先頭 10,000 バイト・末尾 10,000 バイトと省略注記に置き換えます。注記は、省略部分が必要なら `sed -n` や `rg` で絞って再取得するようモデルに促します。Codex は毎要求で履歴全体を再送するため、最新の結果だけでなく履歴の全件に毎回同じ規則を適用し、プロンプトキャッシュの接頭辞を保ちます。Jev は呼びません。

- **根拠**：直近の実セッションで送信済みのツール結果 2,301 件のうち、20,000 バイト超は 13% ですが、バイト数の 54% を占めていました。`gpt-5.6-terra` の Codex はツール結果をトークン数 10,000 で打ち切り、モデルが呼出しごとに `max_output_tokens` を選びます。大きな結果は、モデルが大きな上限を選んだときに生じます。
- **ベンチ**：課題 `large-facts`（約 32KB のログ 6 個の先頭・中央・末尾の事実を回収）、`gpt-5.6-terra`／`medium`、事前登録した 6 ペア。品質は全 12 実行で満点、総トークン削減率の中央値 +8.19%（範囲 −39.25%〜+27.69%、改善 4・悪化 2）。キャッシュ外の入力は 6 ペアすべてで減りました（中央値 109,494→78,900）。一方で、省略部分の再取得が増え、要求数は 5 ペアで増え、経過時間の中央値は 48.0 秒→61.1 秒に延びました。区間がゼロをまたぐため、ベンチの効果判定は `hold` です。
- この課題は、モデルが大きな出力を求めた場合の負荷試験です（課題文で `max_output_tokens` を 12,000 以上にするよう指示）。実作業全体の削減率は未検証です。

```bash
jev-routing bench --agent codex --model gpt-5.6-terra --effort medium \
  --tasks large-facts --modes off,on --reps 6 --codex-tool-output-truncate
```

### 採用しなかった方法

| 調査 | 方法 | 結果 |
|---|---|---|
| ツール選択（既定） | 標準 Codex・`dual-facts --catalog 4` | ローカル候補が 3 件で費用ゲート（`JEV_COST_GATE_MAX=3`）に止まり、Jev は呼ばれない。追加した MCP ツールは外部名前空間として絞り込み対象外。効果は算出不可 |
| ツール選択（Jev を強制） | `JEV_COST_GATE_MAX=0`、`dual-facts`、3 ペア | 1 実行あたり Jev 約 1.3 万トークン。候補一覧が変わり、最初の要求のキャッシュ読取が 24,320→0。総差は +81,082／−118,050／−17,826 と符号が揺れ、原因診断にとどめた |
| Jev による圧縮置換 | Codex の圧縮要求を Jev の要約で置き換え | 品質 0/3 の回、要求本文の肥大、CLI と上流の使用量の不一致が起きた。既定で無効 |
| 圧縮閾値を下げる | Codex 標準圧縮、閾値 900000 対 55000、`compact-facts`、事前登録 6 ペア | 削減率の中央値 −0.75%（範囲 −11.70%〜+16.77%）。採用しない |

### 制約

- ベンチ課題は切り詰め 1 つ、モデルは 1 つ（`gpt-5.6-terra`、`medium`）だけです。
- 切り詰めの効果は負荷試験での値で、実作業での削減率と、再取得による待ち時間の悪化の程度は未検証です。
- 実セッションの分析はローカルの Codex 記録（`~/.codex/sessions`）によります。
