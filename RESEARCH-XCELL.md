# test-x-cell 計測指標の検証と jev-routing ターン数増加の調査

## 背景

`scripts/test-x-cell.sh` は claude/codex/grok/cursor/devin の各CLIを baseline（jev-routingなし）と jev（jev-routing経由）で実行し、効果を比較する計測ツールである。当初 `wall_ms`（壁時計時間）を主指標としていたが、比較結果が実行のたびに大きくブレて安定した結論が出せなかった。本調査は指標の妥当性検証と、根本原因の特定を目的とする。

## 調査の経緯

### 1. 初期計測（1ファイル確認プロンプト、n=10）

内部確認用の軽量プロンプト（`internal/host/host_test.go` の1関数確認）で `wall_ms` を10回比較した。

| 指標 | 中央値 |
|---|---|
| baseline (ms) | 12253.71 |
| jev (ms) | 10065.09 |
| 差分（baseline − jev） | +527.66ms（jevが速い） |
| jevが速かった回数 | 6/10 |

分散が大きく（baseline 7.1〜14.8秒、jev 7.0〜15.1秒）、有意な差とは言えなかった。

### 2. プロンプト変更によるリクエスト数増加の試み

タスクが小さすぎてjev-routingの文字数削減効果が壁時計時間のノイズに埋もれている、という仮説のもとプロンプトを変更した。

変更後プロンプト:
> `internal/proxy/rewrite.go` に定義されている関数（func で始まる各定義）それぞれについて、リポジトリ全体から呼び出し箇所を検索し、関数名ごとにファイル:行番号の一覧を作成してください。ファイルは変更せず、最後に `CHECK: PASS` と一行だけ出力してください。

rewrite.go内の19関数それぞれについて呼び出し元を検索させることで、jev-routing経由のリクエスト数を3〜9回に増やした。

結果、3回の実行で `wall_ms` の逆転が観測された（1回はjevが baseline の3.4倍遅い: baseline 18967ms vs jev 65411ms）。

### 3. 原因調査：askNextToolの同期呼び出し（後に反証）

`internal/proxy/rewrite.go:149` の `askNextTool()` が各プロキシリクエストのたびに外部LLM（`jev.Client.Ask`）を同期呼び出ししてツールを1つに絞り込んでいることを発見。run.logの行間に数秒〜8分の開きがあり、これがwall_ms悪化の原因と当初推定した。

### 4. reviewチーム（review-claude/review-codex/review-local）による1回目の検証

3者合議で以下が判明:
- **askNextToolのHTTPタイムアウトは20秒、リトライなし**（`internal/jev/jev.go:51`）。8分の待機はAsk自体のレイテンシではあり得ない。run.logに出るのはRewrite完了後のFormatStats 1行のみで、Ask自体のレイテンシは記録されていない。行間の時間差は「上流API応答＋クライアント側のツール実行＋次リクエストまでの間隔」であり、Askへの誤帰属だった。
- **比較指標の欠陥**: `routing_chars_before/after`（文字数削減率）はプロキシ内部の書換え量の自己申告に過ぎず、baselineとの比較にならず常に「勝ち」を示すため、悪化を検出できない。主指標にすべきでない。
- **既存フィールドの見落とし**: `claude -p --output-format json` の応答には既に `total_cost_usd`、`duration_api_ms`、`num_turns` が含まれているのに、スクリプトはこれらを抽出せず捨てていた。

3者一致の推奨: 案1（文字数削減率を主指標）は却下、案3（Askレイテンシの差し引き）は却下、案2（usage比較）は「新規計装せず既存フィールドを使う」形で縮小採用。

### 5. test-x-cell.sh の指標改善（実装済み）

`scripts/test-x-cell.sh` の `run_one()` 内Pythonブロックに以下3フィールドを追加:
- `total_cost_usd`
- `duration_api_ms`
- `num_turns`

`summarize()` のjqフィルタに `cost_usd` と `duration_api_ms` の差分を追加し、比較出力に反映した（`routing_chars`・`wall_ms`は勝敗判定に使わず併記のみ）。

`bash -n scripts/test-x-cell.sh` で構文検証済み（エラーなし）。

### 6. 新指標での再計測（n=10）

19関数検索プロンプトのまま、新指標で10回実行した結果、**`num_turns` と成績に強い相関**を発見した。

| ターン数の状態 | 件数 | コスト差分の中央値（baseline − jev） |
|---|---|---|
| baseline = jev（ターン数一致） | 5/10 | +0.0212 USD（jevが安い） |
| jev > baseline（ターン数増加） | 5/10 | -0.0250 USD（jevが高い） |

baselineは常にnum_turns=3（1回だけ5）で安定していたのに対し、jev側はnum_turns=3〜9と大きくばらついた。ターン数が一致する実行ではjevが一貫してコスト・時間とも有利、ターン数が増える実行では一貫して不利という傾向が明確に出た。

全体の中央値（n=10）:

| 指標 | 中央値（baseline − jev） |
|---|---|
| `total_cost_usd`差分 | +0.00067 USD（ほぼ拮抗） |
| `duration_api_ms`差分 | -1885ms（jevがやや遅い） |
| `wall_ms`差分 | -767ms（jevがやや遅い） |

ターン数増加パターンと安定パターンが相殺し、全体では有意差が見えなくなっていた。

### 7. ターン数増加の原因調査（1次仮説、後に反証）

`internal/proxy/rewrite.go` の以下2メカニズムが原因と推定した:
1. `filterTools()`（109, 182-202行目）が全ツールカタログ（11種）から `askNextTool` が選んだ1つだけに絞り込み、`tool_choice` も削除する（119行目）ため、モデルはそのリクエストで1種類のツールしか呼べない
2. `disableThinking()`（120, 205-224行目）がツールがフィルタされる全リクエストで無条件にThinkingを無効化する

これにより、モデルが「19関数の検索をどうバッチ処理するか」を計画する余地が失われ、ターン数がラン毎にばらつくと推定した。

改善案として「同一ツールが連続選択される場合はaskNextToolの外部呼び出しをスキップし、Thinkingを無効化しない」を検討した。

### 8. reviewチームによる2回目の検証（改善案は反証・却下）

3者合議の結果、上記仮説は**実測ログにより反証された**。

**review-claudeの精査結果（決定的）**:
- run.logの全10ラン・全ターンで **`tools 11→1 chosen=Bash`、Thinking無効化も完全に同一挙動**。つまり「同一ツール連続」は本タスクで常時成立しており、改善案（同一ツール連続時にAskスキップ）を適用してもfilterToolsの出力は変わらない（Bash→Bashのまま）。ツール制限が原因なら、3ターンで終わる良いランも同じ制限下で達成されている事実と矛盾する。
- baseline/jevの `output_tokens` はターン数一致時でほぼ同等（reasoning_tokens=null）。baseline側にThinking由来の出力トークンが乗っている証拠がない。
- **ターン数と唯一連動していたのは compaction（会話圧縮）の切り詰め量**。`rewrite.go:56-65` で `PreserveRecent=2`、`len(items) > 4` で live AskCompact が発火し、直近1組の tool call/result しか保護されない。本タスクは「19関数の検索結果を最後に集約する」性質のため、古い検索結果がターン3以降で切り詰められ、モデルが失った情報を再検索する羽目になり5〜9ターンへ膨張する。baselineは圧縮がないため常に3ターン固定。
- コスト構造: `cached_input` はターン数に比例し、ターン数一致時にjevが約12%安いのはツールスキーマ削減（cache_creation減）による。Askのコストは`total_cost_usd`には乗らず`duration_api_ms`にのみ乗る。「Ask削減でコストが下がる」は成立しない。

**review-codexの判定**: 不採用（⭐4/5）。因果が未証明のまま複数レバー（Ask省略・Thinking維持・状態導入）を同時に変えると切り分けができない。既存の `TestRewriteTwoSequentialTurnsDoesNotLockSpawnSubagent`（`rewrite_test.go:806-847`）が守る「新しい要求で古いツール選択に固定しない」という保護と、セッション単位の再利用は同じ失敗を広げるリスクがあると指摘。

**review-localの判定**: 不採用（代替案比較で最下位 ⭐1/5）。「Askは切替の神託であり、スキップ対象にすべきでない」。提案は実質「直前に実行したツールを次も使うと仮定する」ことになり、一般開発タスク（Read→Edit→Bashのような頻繁な切替）では直前ツールへの張り付きが切替そのものを破壊する副作用を持つと指摘。また会話ID単位の状態管理は、直前ツールが `actions`（`rewrite.go:43-44`）から既にステートレスに導出可能なため不要と判定。

**3者一致**: 会話ID単位の新規状態管理は不要（`plan.go` の `agentStreak` と同様の手法でステートレスに汎用化できる）。

## 結論

1. `wall_ms` 単独の比較は指標として不適切（分散が大きくノイズに埋もれる）。`total_cost_usd`・`duration_api_ms`・`num_turns` を併記する現行の改修版が妥当。
2. 「ツール1種絞り込み＋Thinking無効化」仮説は反証された。ターン数増加の真因は **compaction（`PreserveRecent=2`、`rewrite.go:56-65`）による古い検索結果の切り詰め** の可能性が高い（現時点では相関のみ、因果は未検証）。
3. 「askNextTool頻度削減＋Thinking維持＋会話ID状態管理」の改善案は3者一致で **不採用**。

## 次にやること（優先度順）

1. **compaction無効化のA/B実験**（review-claude提案、本命）
   `client.Live()` 分岐をスキップする、または `PreserveRecent` を引き上げる設定で10回実行し、`num_turns` が3に収束するかを検証する。相関ではなく因果を確認する。

2. **disableThinkingを外すだけの単純A/B**（review-local⭐5、review-claude②）
   `filterTools` によるツール1種絞り込みとAsk頻度は一切変えず、`rewrite.go:120` の `disableThinking` 呼び出しのみを外して10回実行し、`num_turns` の分散に影響するか単独で測定する。

3. **ツール1種固定をやめ、複数候補を残す案の検証**（review-local⭐4）
   `Decision.Top`（`plan.go:19-26`、現状代入のみで未使用）を活用し、直前ツール＋次点候補の2種程度をカタログに残す設計を検証する。同種反復（Grep連打）とツール切替（Read→Edit）の両方に効く可能性がある。

4. **`scoreCatalog` の再利用ペナルティ見直し**（review-local⭐3）
   検索系ツール（Grep/Glob）の再利用ペナルティを、既にReadで免除されているのと同様に免除する（`plan.go:309-310`）。検索反復でローカル確信度が下がり毎ターンAskが発生する経路そのものを塞ぐ案。

5. **計測の穴を塞ぐ**（review-claude提案）
   `test-x-cell.sh` は `--output-format json` のため tool_use 列が保存されず、ターン増加がどのツール呼び出しで起きたか追跡できない。`stream-json --verbose` でツール名列を保存し、`RewriteStats` に `askMs`（Ask実測レイテンシ）・`hasThinking`（Thinking設定の有無）・`compactTruncatedIDs`（圧縮で切り詰められた項目）を追加する。

### 検証時の注意（3者共通の指摘）

- **レバーは1つずつ変更すること**。Ask削減・Thinking維持・状態導入を同時に変えると、どれが効いたか切り分けられなくなる。
- 会話ID単位の新規状態管理は**導入しない**（直前ツールは既存の `actions` からステートレスに導出可能）。
- 検証には「同種反復タスク」（今回の19関数検索）と「ツール切替タスク」（Read→Edit→Bashのような典型的開発フロー）の両方を含め、一方への最適化が他方を壊していないか確認すること。

## 関連ファイル

- `scripts/test-x-cell.sh` — 計測スクリプト（`total_cost_usd`/`duration_api_ms`/`num_turns` 追加済み）
- `internal/proxy/rewrite.go` — `Rewrite`（28-124行目）、`askNextTool`（149-181行目）、`filterTools`（182-202行目）、`disableThinking`（205-224行目）
- `internal/plan/plan.go` — `Decision`構造体（19-26行目）、`DecideSpecs`、`agentStreak`、`scoreCatalog`（309-310行目付近）
- `internal/jev/jev.go` — `Client.Ask`（81-116行目）、HTTPタイムアウト設定（51行目）
- `internal/proxy/rewrite_test.go` — `TestRewriteTwoSequentialTurnsDoesNotLockSpawnSubagent`（806-847行目）
