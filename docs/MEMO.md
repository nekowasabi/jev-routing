# jev-routing：トークン増加と機能分離の調査メモ

記録開始日：2026-09-24。コード・文書の調査、固定通信試験、実ホスト試験の経過を時系列で残す。初期の「未実施」「未修正」は記録時点の状態を指し、現在の状態は次節を正本とする。

## 現状の骨子と当面の作業（2026-09-24 更新）

### 判断の前提

- 目的は**外部検証で成果品質を保ち、親・子ホストと Jev の実測総トークンを減らすこと**。時間短縮は副指標。単発の正答、候補の削減、通信成功を削減効果と呼ばない。
- **ツール・スキル選択とコンパクションは別機能**。当面は選択経路を `JEV_COMPACTION=off`・`JEV_REASONING=preserve` で検証し、圧縮と併用する比較はそれぞれが成立した後に行う。
- 「判定成功 → ホスト要求への適用 → 必要なツール／スキルの使用 → 結果の対応 → 外部成果の正答」を別の段階として扱う。総トークンには Jev のスキル選定要求も含め、どの段階か不明なランは効果判定に使わない。
- Claude Code と Codex はサブスクリプション認証の実 CLI で疎通を確認済み。模擬上流は通信契約、模擬 Jev は分岐の固定試験に限る。完了まで GitHub へ公開しない。

### 直近の実証結果

| ホスト／機能 | 確認できた段階 | 残る障害 |
|---|---|---|
| Claude Code のツール助言 | 末尾 `system` の実要求を固定試験で再現して修正。実 Jev の助言適用、２回の `Read`、正答を確認 | 助言がトークンを減らすかは未判定。２ツール課題の単発比較では増加 |
| Claude Code のスキル | frontmatter 説明、配信先、要求間の共有状態を修正。実 Jev が暗黙に選び、実 Claude Code が合成マーカーを採用して正答 | スキル課題の対照は品質不合格で、削減量は比較不能。再配信と Jev 判定のトークン費用に注意 |
| Codex のツール選択 | 実 Jev 適用、２つの MCP 結果、成果３項目を確認。CLI の主モデル使用量とプロキシのモデル別使用量が一致 | 自動承認審査の追加推論を含めると、単発比較ではトークン増加。反復効果は未判定 |
| Grok Build のツール選択 | 実 Jev の `read_file` 適用、ツール結果、`xcell-module` 品質3/3。CLI の主モデル使用量とプロキシの完了要求が一致 | 終了時のキャンセル済み推論要求に使用量がなく、総トークン差は比較不能 |
| Devin CLI のツール選択 | 実 Jev の read/write 適用、Connect 実通信の呼出 ID／結果、`xcell-module` 品質3/3。ホスト会話記録に補助使用量 | Connect 推論応答の使用量が全件欠測。ホスト補助集計と要求別使用量は照合できず、総トークン差は比較不能 |

Claude Code／スキルの修正前追試は Claude Opus 5.5・high の単独レビューで実施した。実 Claude Code は `claude-sonnet-5`・`medium`、実 Jev は TypeSafe のサービスを使用。スキル選定の Jev 使用量が計上漏れした例（当時の統計7,986／228、通信実測9,258／511）は修正前の証拠である。修正後は実ホストと固定試験で判定・配信・成果を再確認した。単発の成果成功を削減効果とはしない。[選定経路](../internal/proxy/proxy.go#L870)・[統計加算](../internal/proxy/proxy.go#L580)

### 当面の作業リスト（計測を先に固定する）

**現在は主比較の検証基盤と Claude Code／スキルの修正を実装済み。** 以下のチェックは当初の順序と残作業を示す。`direct` 条件、全ホストの計測契約、反復効果判定は未完了。判明した不具合は固定試験で再現してから修正した。

**第１段階：ベンチマークと計測の契約**

- [ ] **V1：ラン記録の項目と判定規則を固定する。** [検証仕様](requirements/unified-benchmark-validation.md)を基に、ホスト・実モデル・推論設定・ソース版・開始状態・条件、上流推論／制御／Jev／子セッションの要求数、親子と Jev の入出力・キャッシュ使用量、選定→適用→ツール呼出 ID→結果、外部成果、欠測理由を定義する。`direct`・`passthrough`・`selection` を別条件として記録し、未取得値を０にしない。
- [ ] **V2：固定入力で計測器を較正する。** 既知の使用量・複数ツール・子セッション・失敗／中断・同時要求・イベント切捨てを与え、ラン記録と集計が期待値と一致することを確認する。既知の漏れであるスキル `capability` 判定の Jev 使用量をここで修正する。CLI とプロキシの使用量も同じ実行で突き合わせ、差を記録する。
- [x] **V3：成果を採点できる課題を先に固定する。** 短い P1／R1 は疎通用に限る。ツール選択には独立複数ツール R2 と複数工程 R3、スキルには指示を課題本文から推測できない S1 を用意し、必要操作と最終成果を独立に採点する。各課題は対照条件でも同じ採点器を使う。失敗復旧 R4 と子エージェント A1 は基本課題の計測が成立してから追加する。
- [x] **V4：主指標の確認方法を固定する。** 保存済みランから品質・適用・計測完全性・総トークン差を `comparison.json` に再計算し、ダッシュボードはその数値と比較不能理由を表示する。要求別の原因分析はローカルの `proxy-events.json` とモデル別の `runs.jsonl` を使い、表示側に別の集計式を持たせない。
- [x] **V5：現行動作を診断として記録する。** 上記の契約で Claude Code・Codex を優先し、実 Jev・実 CLI の `passthrough` と `selection` を試す。既知の Claude 助言０件とスキル不採用も失敗として残す。この段階の反復は原因・ばらつきの把握用で、削減効果の採否には使わない。

**第２段階：介入動作の修正と再検証**

- [x] **F1：Claude Code の助言適用を修正する。** 末尾に `system` メッセージが続く実要求を回帰試験にし、助言の挿入、モデルによる使用、ツール結果、外部成果を V1 の同じ項目で再確認する。
- [x] **F2：スキルの選定・配信・採用を修正する。** frontmatter の説明、ホストの `Skill` ツールとの区別、要求間の `LastDelivered` 競合を扱い、Jev 選定と明示名選定を分けて検証する。配信方式を決め、実 Claude Code が S1 の成果を満たすまで成功としない。
- [ ] **F3：他ホストの未完了の証拠を埋める。** Codex の実 Jev 適用例と Devin CLI の呼出 ID／結果の対応を確認し、Grok Build の成立した経路を回帰試験に残す。

**第３段階：効果判定**

- [ ] **E1：条件を事前登録して反復比較する。** 品質・適用・計測が成立したホスト／課題に限り、同一条件の `passthrough` と `selection` を順序交替で比較する。全開始ランの品質、総トークン、比較可能数、悪化例、経過時間を示す。コンパクションは別条件として検証し、各単独条件の後に併用を試す。

**第２段階へ進む条件**：V1～V4 の記録・採点・欠測判定を固定し、V5 で現行の失敗も隠さず再現できること。**効果判定へ進む条件**：対象ホスト・対象機能で判定の適用、必要操作、外部成果、該当する親子セッションと Jev の使用量が揃うこと。スキルを含まないツール選択の比較をスキルや製品全体の改善に読み替えない。

## ユーザーの方針

- 主目的は、タスクの品質を保ちながら総トークン使用量を減らすこと。動作時間の短縮は副次的な目標とする。
- コンパクションと、LLM が担う処理の一部を Jev に任せる機能は別々に扱う。まず重要なのは後者であり、上流 LLM の要求数が極端に増える原因を調べる。
- ホストごとに通信形式、ツール・スキルの呼び出し、実行主体を一次情報から整理する。ベンチの「正しく動作した」の定義も先に固める。
- 議論と調査結果をこのファイルに継続して残す。修正方針の合意前にはコードを変更しない。

## 用語と機能の境界

混同を避けるため、上流 LLM への推論要求、Jev への判定要求、ホストが実行したツール呼び出し、制御用 HTTP 要求、子エージェントの推論要求を別々に数える。実ツール呼び出し数が同じでも、並列呼び出しが逐次化されれば上流 LLM の要求数は増え得る。この説明は原因仮説であり、現行ランでの実証ではない。[Anthropic の並列ツール呼び出し](https://platform.claude.com/docs/en/agents-and-tools/tool-use/parallel-tool-use)

**コンパクション**：現行コードは通常要求ごとの履歴圧縮を停止し、ホストが圧縮を要求したときに別分岐で処理する。[通常要求](../internal/proxy/rewrite.go#L228)・[圧縮要求](../internal/proxy/proxy.go#L558) コード上の変換を独立にしても、圧縮された文脈が後続のツール判断へ間接的に影響しないとは保証できないため、両機能を同時に有効化した検証も必要。

**Jev による代替**：通常経路で Jev が代替するのは主に「次の候補を選ぶ判断」である。Jev の `Choice` は固定候補から一つを返す。[TypeSafe の仕様](https://docs.typesafe.ai/primitives/choice) 現行プロキシは、その結果を Claude Code には助言として渡し、Codex と Grok Build では主にツール候補を絞る。任意の引数生成と実ツール実行はモデルとホストに残る。[選択処理](../internal/proxy/rewrite.go#L1242)・[候補の書き換え](../internal/proxy/rewrite.go#L473) `direct` は固定引数を持つ特定の Chat 形式など追加条件が必要で、既定では対象ツールが空である。[適用条件](../internal/proxy/rewrite.go#L492)・[既定値](../internal/proxy/options.go#L56)

## 確認済みの実装とホスト別の整理

| ホスト | 一次資料で確認したホスト側の責務 | 現行プロキシの介入 | 確認が残る点 |
|---|---|---|---|
| Claude Code | モデルが `tool_use` を返し、Claude Code がクライアント側ツールを実行して `tool_result` を次の要求へ渡す。[公式資料](https://platform.claude.com/docs/en/agents-and-tools/tool-use/how-tool-use-works) | ツール一覧を維持し、直近のツール結果へ助言を追加する。[実装](../internal/proxy/rewrite.go#L259) | 初回ターンには助言の追記先がない。Jev 判定だけが発生した実件数。[既存テスト](../internal/proxy/claude_advise_test.go#L92) |
| Codex | モデルが関数呼び出しを選び、クライアント側が実行する。[公式資料](https://developers.openai.com/api/docs/guides/function-calling) | Responses Lite の `input.additional_tools` も抽出して絞り、条件によって `tool_choice` と推論設定を変える。[実装](../internal/proxy/rewrite.go#L452) | 候補変更によるキャッシュ・並列性・再計画への影響。 |
| Grok Build | 公式ソースにツール付き要求とホスト側の実行処理がある。[要求形式](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-sampling-types/src/types.rs#L52-L69)・[実行処理](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-shell/src/session/acp_session_impl/tool_dispatch.rs#L11-L60) | 通常 JSON 要求の候補を絞る。[実装](../internal/proxy/rewrite.go#L473) | 増加した要求を通常ターン、再計画、ホスト内の通信再試行に分類する。[再試行処理](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-sampler/src/actor/request_task.rs#L93-L145) |
| Devin CLI | スキル本文は会話へ注入され、ツールの権限と実行は Devin CLI が管理する。[スキル](https://docs.devin.ai/cli/extensibility/skills/overview)・[コマンド](https://docs.devin.ai/cli/essential-commands) | `GetChatMessage`／`GetDevstralStream` の Connect 通信に専用経路がある。[実装](../internal/proxy/proxy.go#L479) | 公式文書でこの通信形式の契約は確認できていない。実通信で候補の抽出、書き戻し、上流受理を確認する。現行試験は手製のフレームを使う。[試験](../internal/proxy/connect_devin_test.go#L18) |

スキル、MCP、CLI の自動適用は既定で観測モードであり、`JEV_AUTO_APPLY` も既定では無効である。[設定](../internal/proxy/options.go#L56) 有効時のスキル本文供給には後続要求への反復注入が起こり得る経路があるが、発生頻度とトークン増加量は未測定。[選定と供給](../internal/proxy/proxy.go#L828)・[配達済み結果](../internal/proxy/application.go#L123) MCP・CLI は現行の通常自動適用経路では実行処理まで進まず、[README の記述](../README_ja.md#L227)と一致しない。[実装](../internal/proxy/proxy.go#L909)

## 過去の観測と、現行ランで未確定のこと

- コミット `d7a96b4` は、Codex／Grok の毎要求の履歴圧縮がプロンプトキャッシュを崩し、過去の `gpt-5.6-terra` 比較で非キャッシュ入力を増やしたとして、その経路を停止した。これは**過去の増加原因**であり、現行 `HEAD` で報告された要求数増加の原因を示す証拠ではない。[現行コード](../internal/proxy/rewrite.go#L228)
- 保存された[旧チャート](../charts/202609231550-terra/comparison-dark.svg)には上流要求数の増加例があるが、対策コミットより前の各条件一回の記録で、生の `runs.jsonl` は作業環境にない。現行 `HEAD` の結果、設定、要求別イベントも作業環境に見つかっていない。
- 現行の要求数増加については、候補制限による逐次化、誤選択後の再計画、スキル本文の反復、ホスト内の再試行・子セッション、計測上の過大計数を区別する必要がある。どれが主因かは未確定。

## 現行ベンチの判定上の問題

1. `make test-selection-benchmark` の説明は `JEV_COMPACTION=off`・`JEV_REASONING=preserve` だが、[実行スクリプト](../scripts/test-x-cell.sh#L93)はプロキシ側の全条件に `on`・`legacy` を固定する。[説明](../README_ja.md#L254) 現状の結果をツール選択単独の効果として読めない。
2. [合格判定](../scripts/summarize_x_cell.py#L248)は `filter`／`forced` の完了を要求し、Claude Code の現行 `advise` を含めない。圧縮 `on` の条件では圧縮適用も要求するが、通常要求の圧縮は停止済み。
3. [ベンチの `Requests`](../internal/bench/gateway.go#L201)は全プロキシイベントを数え、非推論の制御要求も混ざり得る。[制御要求の記録](../internal/proxy/proxy.go#L703) 上流 LLM 要求、Jev 要求、実ツール呼び出しの別集計が必要。
4. [TypeSafe の公式 API](https://docs.typesafe.ai/api)は使用量を `input_tokens`／`output_tokens` で返すが、[Jev 応答の受信構造体](../internal/jev/jev.go#L90)は `inputTokens`／`outputTokens` を期待する。公式形式の応答では使用量が欠測し得る。現行[ベンチ記録](../internal/bench/record.go#L19)には `JevInput` はあるが Jev 出力はなく、総トークンを完全には計算できない。
5. [集計](../internal/bench/report.go#L113)は汚染ランだけを除外する。失敗や使用量欠測を含む中央値を、そのまま節約の証拠にできない。`bench-all.sh` は選択のみ／圧縮のみの二条件を `--modes on` で別出力にするため、単体では基準との差を出せない。[スクリプト](../bench-all.sh#L3)

## 提案する検証契約（未合意・未実施）

まず「プロキシなし」「プロキシ通過のみ」を比較し、接続自体の影響を確かめる。そのうえで同一ホスト・同一モデル・同一開始状態の課題を、**両機能無効／コンパクションのみ／選択のみ／両機能有効**で比べる。選択のみの条件では推論設定を維持し、スキル本文の自動供給は別条件とする。実行順を交替して反復し、各ランの設定とソース版を記録する。

| 機能 | 正しさの判定 | 効果の判定 |
|---|---|---|
| ツール選択 | Jev の判断が実際に適用され、上流に受理される。必要な候補を除外せず、呼び出しと結果が対応し、外部検証で課題が成功する。単一ツール、独立した複数ツール、順次工程、スキル、MCP／CLI を別課題で見る。 | 応答ごとの実ツール呼び出し数、並列性、追加ターン、再試行、キャッシュ、上流要求数を原因別に示す。 |
| コンパクション | 通常推論要求のツール定義・選択設定に直接介入せず、圧縮要求への応答が受理される。必要な制約と呼び出し／結果の対応を保持し、その後の課題が成功する。 | 圧縮前後の実入力トークンと、後続ターンの再読・再試行を含めて比較する。 |

主 KPI は、親・子エージェントの**上流入力（キャッシュ分を含む）＋上流出力＋Jev 入力＋Jev 出力**の実測合計とする。推論トークンが出力の内数なら二重加算しない。Claude の入力は `input_tokens + cache_read_input_tokens + cache_creation_input_tokens`、OpenAI 形式の `cached_tokens` は `input_tokens` の内数として扱う。[Anthropic の使用量仕様](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)・[OpenAI のキャッシュ仕様](https://developers.openai.com/api/docs/guides/prompt-caching) 欠測ランはゼロ補完せず、比較不能と表示する。品質の不合格を安価な成功として扱わない。所要時間は副指標とする。

## 次の議論で必要な入力

- 要求数増加が起きた**現行版**の `runs.jsonl`、`comparison.json`、プロキシの要求別イベント、または保存先。生の認証情報や要求本文は不要。
- その「リクエスト数」が上流 LLM、Jev、実ツール、制御要求のどれを指すか。該当するホスト、モデル、課題、実行時の設定。
- Jev に代替させたい工程の範囲。候補選択、スキル本文供給、固定引数の呼び出し合成は現行の能力が異なるため、成功条件を個別に決める。

## 検証項目と結果確認の見直し（2026-09-24 追記）

以下は検証設計の調査結果と提案であり、テストの実行・実装変更・施策の採否決定はまだ行っていない。

### 現行の二つの試験系とダッシュボード

| 系統 | 既に測れること | 主目的を判定できない理由 |
|---|---|---|
| `test-x-cell.sh` | 同じコミットの別作業ツリーで、終了値・最終回答の正答・非変更を外部照合する。[実行](../scripts/test-x-cell.sh#L74)・[照合](../scripts/summarize_x_cell.py#L234) | 既定１回。`locate` は５関数の逐次検索・読取を指示するが実ツール呼び出しを採点せず、`module` は `go.mod` のみ。選択比較でも圧縮と推論設定の変更が混在し、Claude の `advise` は現行の適用合格条件から漏れる。[課題](../scripts/summarize_x_cell.py#L16)・[条件](../scripts/test-x-cell.sh#L93)・[合格判定](../scripts/summarize_x_cell.py#L248) |
| `jev-routing bench` | チェス３課題を別の隠し検証器で採点し、上流使用量・キャッシュ・時間・Jev 呼出しを記録する。[課題](../internal/bench/tasks.go#L115)・[検証](../internal/bench/verify.go#L46) | 既定１回、追加カタログ０件。`--catalog` の追加ツールは Claude/Codex のみ、実行するとエラーになるため、必要ツールの実行ではなく不要候補の回避を主に測る。成果判定は実ツール数・並列性・選択の消費を見ない。[既定値](../internal/bench/run.go#L90)・[追加ツール](../internal/bench/mcpstub.go#L160) |
| ダッシュボード | プロキシ１プロセスの要求別イベント、上流使用量、選定と適用状態を表示する。[供給データ](../internal/proxy/dashboard.go#L49) | [使用量棒グラフ](../internal/proxy/assets/dashboard.js#L79)は上流使用量だけを合計し、Jev 使用量、ホスト別の総入力、比較元との差、成果品質を算出しない。「比較効果」は実測差ではなく未適用・欠測などの状態表示で、貼り付けた比較 JSON は整形表示のみ。[表示](../internal/proxy/assets/dashboard.js#L608)・[貼り付け](../internal/proxy/assets/dashboard.js#L796) |

追加の計測上の制限：`bench` の `Requests` は推論以外も含む全イベント数で、Jev 使用量は入力だけを記録し、欠測値は加算時に０になる。[集計](../internal/bench/gateway.go#L201)・[記録](../internal/bench/record.go#L19) `ObservedTools` はツール名を重複排除して保存するため、同じツールの複数回使用、呼出し ID、並列性を復元できない。[抽出](../internal/proxy/decision_event.go#L250)・[保存](../internal/proxy/events.go#L70) イベントは直近 1000 件までで、ダッシュボード経由のベンチ集計は長いランで全件を得られる保証がない。[上限](../internal/proxy/events.go#L10)・[読み出し](../internal/bench/gateway.go#L177)

### 指標の契約：値、出典、欠測を一緒に保存する

| 順位 | 指標 | 定義と判定用途 |
|---|---|---|
| 主指標 | タスク全体の実測総トークン | 親・子の上流入力（キャッシュ込み）＋上流出力＋Jev 入力＋Jev 出力。各値に出典と取得可否を付け、一つでも必要な値が欠ければ総量は「欠測」とする。バイト差・推定削減量を代用しない。 |
| 品質条件 | 外部成果、実行の完了、設定一致 | 隠し検証または事前計算した正解で成否を確認する。ホスト・モデル・推論設定・課題・コミット・候補カタログ・実行モードを記録し、一致しない組は比較しない。失敗ランと消費量も別枠で残す。 |
| 介入条件 | 選定→適用→上流受理→実ツール実行 | `advise`、`filter`、`forced`、`direct`、スキル本文供給を区別し、選択が実際に使われたかを追う。必要ツールの保持、呼出し ID と結果、実ツール回数と並列群を確認する。 |
| 原因分析 | 要求と使用量の内訳 | 上流推論、Jev、制御要求、ホスト内再試行、子セッションを別々に数える。上流入力のキャッシュ読取・書込・非キャッシュ、出力中の推論、Jev 待ち、総経過時間を表示する。 |

`bench` の[報告](../internal/bench/report.go#L251)は課題名で on/off を分けて**各群の中央値同士**を比較し、モデル・候補・設定の一致や反復ペアを検査しない。`x-cell` の[中央値報告](../scripts/summarize_x_cell.py#L123)には比較不可でも品質が成功したランが入り、`improved` は入力・出力・時間すべての中央値改善を要求する。どちらも主指標の効果判定とは分ける。ペアごとの総トークン差と比率をまず出し、その分布、比較可能数／全件数、成功率、欠測数をホスト・課題ごとに示す。全ホストや異なる課題の中央値をさらに一つの中央値へまとめて、個別の悪化を隠さない。

### 検証の順序と採用ゲート（提案）

1. **計測契約の確認**：ホスト別の使用量の内数関係と Jev 応答形式を固定データで照合し、要求数、実ツール数、欠測、イベント切捨てを正しく判定できる状態にする。ここが満たせない実走行は節約率を算出しない。
2. **ホスト別の通信契約**：推論要求の到達、候補抽出、変換適用、上流受理、ツール呼出しと結果の対応を、実際の通信形式ごとに確認する。対応していない経路を性能比較へ混ぜない。
3. **選択だけの成果課題**：圧縮を無効にして推論設定を保ち、短い単一ツール課題は計測器の確認用に残す。独立した複数ツール、段階的な調査・修正、失敗後の復旧、スキル、MCP／CLI の課題を加える。必要操作と最終成果を独立に採点する。候補制限による逐次化・誤除外・再計画を要求別に調べる。
4. **圧縮だけの長い課題**：ホストの圧縮要求を実際に発生させ、通常要求のツール定義に直接触れないこと、残すべき制約・証拠・呼出し対応、その後の成果を確認する。圧縮前後の文字数だけで成功としない。
5. **両機能の併用**：単独の合格後に相互作用を確認する。同じホスト・モデル・開始状態を使い、比較条件の順序を交替して反復する。反復数は小規模な予備計測のばらつきから決め、単発の中央値や中央値の再集計で採否を決めない。

品質が悪化していないことを節約主張の前提とする。主指標の改善は、比較可能な反復ペアの総トークン差で判定する。全ランの成功率、失敗ランの消費量、最も悪化した課題、要求数・時間の変化も同時に残す。これにより成功ランだけを選ぶ偏りを避ける。

### QA 観点と最小の課題群（提案）

| 技法 | この製品で確認するケース |
|---|---|
| 同値分割 | ホスト別通信形式、ツールなし／単一／独立複数／順次／スキル／MCP、圧縮要求と通常要求、適用方式別。 |
| 境界値 | 候補０・１・費用ゲート境界の３／４件、結果なし／一件／複数件、イベント保持上限の直前・上限・超過。 |
| デシジョンテーブル | 圧縮 on/off × 選択 on/off、品質合格／失敗 × 使用量完全／欠測、選定成立／未成立 × 上流受理／拒否。 |
| 状態遷移 | 候補選定→要求変更→上流受理→実ツール呼出し→結果→成果確認。圧縮要求→代替応答→再開。途中失敗や再送での重複も見る。 |
| エラー推測 | Jev 失敗・タイムアウト、不正回答、未知の履歴、部分的な使用量、キャンセル、子セッション、同一スキルの再注入。 |
| チェックリスト | 実際のホスト通信への適合、独立した成果検証、設定一致、全件計測、キャッシュ内数の非二重計上、ダッシュボードと保存済み報告の一致。 |

### ダッシュボードの表示順（提案）

ダッシュボードは新たな集計の正本を作らず、保存済みランと同じ指標定義を表示する。先頭に**比較元、介入条件、成果成功率、比較可能ペア数／全ラン数、総トークンとペア差、欠測**を置く。比較元がなければ「比較なし」とする。次に上流入力のキャッシュ内訳・上流出力・Jev 入出力、推論要求／制御要求／Jev 要求／実ツール呼出し／再試行の内訳、最後に経過時間を置く。個々の要求へ掘り下げて、選定候補から実行結果まで追えるようにする。プロセス単独のダッシュボード表示から、タスク全体の削減を推論しない。

## `x-cell` と `bench` の統合方針（2026-09-24 追記）

検証スイートの入口・反復・計測・結果形式・比較を `jev-routing bench` 側へ統合し、読み取り課題とチェス課題は別の課題・外部採点器として残す案を採用候補とする。両者は同じルーティング本体を使うが、`x-cell` はプロキシなしの対照と本番の `run` 経路、`bench` はプロキシ通過の対照と直接起動の経路を使う。この差は統合後も条件として記録する。[x-cell](../scripts/test-x-cell.sh#L90)・[bench](../internal/bench/gateway.go#L28)

別環境でも結果を再計算・表示できるよう、実行ごとの版付き・許可リスト式の測定記録を Git 管理対象へ保存する。生ログ、要求本文、ツール引数、スキル本文、絶対パスはそのまま公開しない。現行の `results/` と `artifacts/` は Git 管理対象外である。[gitignore](../.gitignore) ダッシュボードは同じ集計定義の保存済み記録を読んで表示し、単独のライブプロセス表示から節約効果を推定しない。

具体的な条件、検証項目 P1／R1–R4／S1／M1／C1／A1／I1／W1、採否の順序、ダッシュボード、保存形式の案は [統合ベンチマークと結果表示の最終改善案](requirements/unified-benchmark-validation.md) にまとめた。実装・実測・GitHub への公開はまだ行っていない。

## 敵対的レビューと反映（2026-09-24 追記）

`ateam single claude-opus-5-5 high` による読み取り専用レビュー（run ID `06adcabda4f9`）を受け、[統合ベンチマークと結果表示の最終改善案](requirements/unified-benchmark-validation.md) を更新した。レビュー対象は本メモ、検証仕様案、現行 `x-cell`・`bench`・ダッシュボード。レビュアによるファイル変更と実テストはない。以下は指摘を現行コードへ照合した結果である。

| 論点 | 判断と反映 |
|---|---|
| 選択以外の推論設定変更 | 確認。`bench` の既定 `legacy` と選択適用時の変更を区別できるよう、`preserve` の明示と `reasoningChanged` 違反を比較不能条件へ追加。[既定値](../internal/proxy/options.go#L56)・[適用](../internal/proxy/rewrite.go#L480) |
| 使用量欠測が０になる | 確認。メーター失敗・固定待機・イベント上限を計測失敗として扱い、旧形式は必要項目がなければ比較不能とした。[現行処理](../internal/bench/run.go#L329) |
| 成功ペアだけの比較 | 確認。失敗ランを含む全開始ランの総トークンを主比較にし、成果一件あたりの総トークンも示す。成功ペア限定は補助へ下げた。[旧集計](../scripts/summarize_x_cell.py#L123) |
| 親子合計の検証 | 確認。現行の一部起動指定は子エージェントを無効にするため、子を使う A1 課題と親子計測の完全性判定を追加。[bench](../internal/bench/agents.go#L61)・[x-cell](../scripts/test-x-cell.sh#L92) |
| direct／passthrough の計測源差 | 確認。主比較を同じプロキシ計測源を持つ passthrough 対介入とし、direct は CLI とプロキシの較正ができた場合のみ総量比較に用いる。 |
| キャッシュ悪化 | 総トークン削減という主 KPI は維持。キャッシュ破壊は同じ総トークンでも費用・速度を悪化させるため、独立した注意と未決の採用制約へ追加。自動的な不合格とはまだ定めない。 |
| 欠測判定の単純化 | `metered < requests` は制御要求・上流を呼ばない経路も欠測にするため採らない。使用量が必要な推論応答だけを母数にする。 |
| 採点器の出力混入 | ワークスペースのコードを読み込む Node プロセスの標準出力から JSON 行を受ける経路は確認した。[読取側](../internal/bench/verify.go#L64)・[実行側](../internal/bench/assets/chess/verify.mjs#L192) 偽装の実発生は未確認。採点の真正性と採点器失敗の分類を通信契約へ追加。 |
| ダッシュボードの一目での判読 | 改善／悪化／保留／判定不能の固定語彙、品質と欠測の先行表示、ホスト×課題の一覧、推定値の隔離、要求単位への掘り下げを仕様へ追加。 |

実用上の最小削減幅、品質差の許容値、反復数、製品全体の課題重みは未実測の値を作らず、予備計測と利用者の目的を踏まえて本比較より前に登録する。これらが未決の間は全体に「改善」の判定を出さない。

## 固定応答による計測契約の確認（2026-09-24 追記）

利用者の指定：実 LLM を用いる検証は `gpt-5.6-terra`、推論量 `medium` を基準とする。今回の確認では外部 LLM を呼ばず、固定応答と既存のローカル試験だけを使った。ホストがこのモデルを受け付けるかは実走行前に確認し、異なるモデルの結果を同一ペアに混ぜない。

`go test ./internal/jev ./internal/proxy ./internal/bench` は３パッケージとも成功。`TestFakePipeline` など既存の固定応答試験も成功し、正常系の集計が動くことは確認した。一方、Go の `-overlay` で作業ツリーを変更せずに次の契約試験を実行した。

| 実行した契約 | 期待 | 実際 |
|---|---|---|
| TypeSafe 公式形式の Jev 使用量 `input_tokens=296`／`output_tokens=20` | 入出力を取得 | 両方 `nil` で失敗。`go test -overlay=/tmp/jev_routing_official_usage_overlay.json ./internal/jev -run '^TestOfficialUsageContract$' -count=1` |
| 介入側の使用量欠測、対照側入力100 | 比較不能 | 報告が入力０・100%削減として失敗。`go test -overlay=/tmp/jev_routing_missing_usage_overlay.json ./internal/bench -run '^TestMissingUsageCannotClaimSavings$' -count=1` |
| 制御 POST 一件＋推論 POST 一件 | 推論要求１件 | `Requests=2` で失敗。`go test -overlay=/tmp/jev_routing_meter_contract_overlay.json ./internal/bench -run '^TestMeter(CountsInferenceOnly|RejectsTruncatedEvents)$' -count=1` |
| `historyTruncated=true` のイベント | 計測不完全として拒否 | 受理して失敗。同じオーバーレイ試験 |

オーバーレイ用の一時ファイルは `/tmp` にあり、リポジトリのコード・テストは変更していない。固定応答で確認できた失敗を[最終改善案](requirements/unified-benchmark-validation.md)の計測契約に反映した。部分使用量、子セッション、ホスト再試行、1000件超過の実行確認と、実サービスでのトークン削減判定は未実施。

## 計測不備の修正後再確認（2026-09-24 追記）

利用者の「再確認を実施」に基づき、上記の固定応答で再現した計測不備を修正した。Jev 使用量の JSON タグを公式の snake_case に合わせ、ベンチでは制御 POST と上流を呼ばない要求を LLM 要求数から分離した。Jev 出力を記録し、上流・Jev 使用量の欠測や部分報告、イベントの切捨てをメーターエラーとして保存する。報告は不完全なランの使用量差を `n/a` とし、総トークンに Jev 出力を加える。比較図は不完全な使用量と対照なしの入力を拒否する。

前節の Go オーバーレイによる四つの再現試験は、修正後に同じ期待値で全て成功した。新規の固定応答回帰テストも追加した。`TYPESAFE_API_KEY= JEV_API_KEY= go test ./... -count=1` は全パッケージ成功、`git diff --check` と対象 Go ファイルの `gofmt -d` に差分なし。実サービスの LLM は呼んでいない。使用する際の基準 `gpt-5.6-terra`／推論量 `medium` は維持する。

この再確認は**計測契約の一部**である。子セッション全体の捕捉、ホスト内再試行、選択／圧縮だけの実サービス比較、ダッシュボードへの結果表示、GitHub に保存する正規化結果は未実装・未検証。

## 計測修正への外部コードレビュー（2026-09-24 追記）

`ateam single claude-opus-5-5 high`（run ID `bc17022b0998`）で、上記のコード・回帰テスト・文書を読み取り専用でレビューした。レビュアはファイル変更、テスト実行、実 LLM 呼出しをしていない。指摘を親セッションでコードに照合した結果を以下に残す。**この節の指摘は未修正である。**

| 判断 | 指摘と根拠 | 次に確認・修正すること |
|---|---|---|
| 高・確認済み | Devin Connect 用の [Jev 試行記録](../internal/proxy/proxy.go#L778)は使用量を格納しない。一方、[ベンチ計測](../internal/bench/gateway.go#L269)は Jev 入出力の欠測をエラーにする。Connect 経路で Jev を実際に呼んだオン条件は比較不能になる。[JSON 経路](../internal/proxy/proxy.go#L539)には使用量が入る。 | Connect 側にも入出力を記録し、固定応答の回帰テストを追加する。 |
| 中・確認済み | [報告](../internal/bench/report.go#L151)の LLM 要求数・Jev 呼出し数は `MeterError` を確認しない。メーター取得や履歴切捨てで `RunRecord` が空でも、対照と比較した要求数の削減が表示され得る。 | 不完全ランの要求数・Jev 呼出し数・誘導率の比較値を `n/a` にする。 |
| 中・条件付き | [要求経路判定](../internal/proxy/proxy.go#L818)は `/messages/count_tokens` も推論要求とみなす。Claude Code が実際にその経路を使用し、返答に `usage` がなければ比較不能になる。実走行の発生は未確認。 | 実通信または固定応答で到達を確認してから、制御要求として分類する。 |
| 中・判断を保留 | 429・529・502 など、使用量のない失敗応答を現行メーターは不完全とする。レビュアはゼロ計上案も示したが、課金・使用量がゼロという一次根拠がない。 | ゼロと推定せず、失敗と使用量不明を分けて記録する。採否への影響は検証仕様で扱う。 |
| 低・表現の問題 | 直接応答は上流 LLM を呼ばないため、上流要求数の分母から除外するのは妥当。「Requests Jev steered」という既存の名称はホスト要求との違いが伝わりにくい。 | 指標名・説明を明確にする。 |
| 低・確認済み | [検証仕様](requirements/unified-benchmark-validation.md)と本メモには、修正前の実装を「現行」と記す箇所が残る。 | 調査時点を「修正前」に統一する。 |

反証で棄却した懸念：キャッシュ済みの Jev 判定は `JevCalls` の HTTP 件数へ入らないため、キャッシュ外試行数との一致確認は妥当。[JSON 経路](../internal/proxy/proxy.go#L544)・[Connect 経路](../internal/proxy/proxy.go#L798) イベントの `oldestSeq > 1` も、実行ごとに新規プロキシを起動する現行ベンチでは切捨て検出として妥当。実サービスでの頻度やトークン削減効果は今回のレビューでは評価していない。

## Devin と計測失敗表示の修正・再確認（2026-09-24 追記）

利用者の依頼に基づき、上記レビューの「高・確認済み」と「中・確認済み」を修正した。Devin Connect の Jev 試行に、通常 JSON 経路と同じ `usageInputTokens`／`usageOutputTokens` を設定した。[実装](../internal/proxy/proxy.go#L778) 既存の Connect 統合試験に固定値31／7を追加し、修正前の `nil` を再現してから修正後の記録を確認した。[試験](../internal/proxy/connect_devin_test.go#L668)

報告の LLM 要求数、Jev 呼出し数、誘導率、圧縮・失敗要求などメーター由来の指標は、同じ条件のランに `MeterError` または使用量件数の不一致があれば `n/a` とする。[実装](../internal/bench/report.go#L174) 介入側で計測失敗、対照側で要求10件・Jev呼出し１件の固定データは、修正前に「LLM要求−90%・Jev呼出し−100%」と表示され、修正後は比較値が `n/a` になった。[試験](../internal/bench/bench_test.go#L30)

`TYPESAFE_API_KEY= JEV_API_KEY= go test ./... -count=1` は全 Go パッケージで終了値０。`git diff --check` と対象ファイルの `gofmt -d` に差分なし。実サービスの LLM は呼んでいない。レビュー中の Claude `count_tokens` 到達、失敗した上流応答の実使用量、ホスト内再試行は未確認のまま。

## Claude Code・Codex の固定通信試験（2026-09-24 追記）

利用者の指定により、主に使用する Claude Code と Codex を先に確認した。Claude の固定要求は `claude-sonnet-5` と `output_config.effort=medium`、Codex は `gpt-5.6-terra` と推論量 `medium` とした。実サービスは呼ばず、模擬上流で検証した。Claude の努力量フィールドと [`POST /v1/messages/count_tokens`](https://platform.claude.com/docs/en/api/typescript/messages/count_tokens) は[公式資料](https://platform.claude.com/docs/en/build-with-claude/effort)で照合した。

- Claude：`/v1/messages/count_tokens` と `/v1/messages` を同じモデル条件で通し、本文の無改変転送、制御要求と推論要求の分類、応答の使用量を確認。修正前は計数用要求も推論要求として２件に数え、使用量欠測とした。`looksLikeLLM` で公式の計数用経路を除外した後は、推論１件・制御１件、入力10・キャッシュ読取20・書込30・出力５を取得した。[実装](../internal/proxy/proxy.go#L818)・[試験](../internal/proxy/gateway_observe_test.go#L113)
- Codex：Responses Lite の `input.additional_tools` からローカル候補を絞り、提供側の `web_search` を保持し、モデル・推論量 `medium` を維持。模擬上流の SSE から入力36・出力８・キャッシュ22・推論３を取得し、`/v1/telemetry` は無改変の制御要求と確認した。[試験](../internal/proxy/codex_fixed_contract_test.go)
- Codex の固定試験では既定 `JEV_REASONING=legacy` が `medium` を書き換える事実も確認されたため、選択単独比較の方針どおり `ReasoningPreserve` を明示した。既定設定での効果をこの固定試験の合格と混同しない。

`go test ./internal/proxy -run '^Test(ClaudeFixedMessagesAndCountTokens|CodexFixedWireContract)$' -count=1` と同じ条件の `-race` は成功。`TYPESAFE_API_KEY= JEV_API_KEY= go test ./... -count=1` も全 Go パッケージ成功。これらは固定通信契約であり、Claude Code／Codex の実 CLI・実モデルでの到達や総トークン削減はまだ示していない。

## サブスクリプション認証での実 CLI 疎通（2026-09-24 追記）

利用者の質問を受け、模擬上流は固定通信契約の試験として維持し、サブスクリプション認証を使う実サービス疎通を別に確認した。認証情報の値は表示・保存していない。実行前後の `codex login status` は `Logged in using ChatGPT`、`claude auth status --json` は `authMethod=claude.ai`・`subscriptionType=max`・`apiProvider=firstParty`。`ANTHROPIC_API_KEY`、`ANTHROPIC_AUTH_TOKEN`、`OPENAI_API_KEY`、`CODEX_ACCESS_TOKEN` は未設定で、Claude の確認した設定ファイルにも API キー再設定はなかった。[OpenAI の認証資料](https://learn.chatgpt.com/docs/auth)・[Claude Code の環境変数資料](https://code.claude.com/docs/en/env-vars)

現行ソースから `/tmp/jev-routing-subscription-smoke` をビルドし、両 CLI に `go.mod` の module と Go 版を読み取らせた。Claude Code は `claude-sonnet-5`／`--effort medium`、Codex は `gpt-5.6-terra`／`model_reasoning_effort=medium`。それぞれ `baseline` と `filter`＋`JEV_SELECTION_MODE=local` を実行し、`JEV_REASONING=preserve`、`JEV_COMPACTION=off`、`JEV_AUTO_APPLY=off` を固定した。Jev のキーは未設定で、外部 Jev 呼出しは０件。API キー方式へ切り替えずにプロキシから既定の実サービスへ転送した。[起動環境](../internal/host/host.go#L123)・[既定上流](../internal/proxy/proxy.go#L241)

| ホスト | 条件 | 上流要求 | 選択適用 | 入力 | キャッシュ読取 | キャッシュ書込 | 出力 | 総トークン |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| Claude Code | baseline | 2 | 0 | 4 | 105060 | 105641 | 289 | 210994 |
| Claude Code | filter・local | 2 | 0 | 4 | 188846 | 22100 | 534 | 211484 |
| Codex | baseline | 2 | 0 | 55779 | 31232（入力の内数） | 0 | 196 | 55975 |
| Codex | filter・local | 2 | 1 | 55299 | 3840（入力の内数） | 0 | 164 | 55463 |

４実行とも CLI 終了値０、上流の推論応答は全て HTTP 200・完了で、最終回答は `go.mod` の module と Go 版に一致した。要求別の上流入力・出力・キャッシュをホストごとの式で合算すると、Claude Code／Codex CLI が報告した使用量と各実行で一致した。作業ツリーには実行による新しい変更がない。ローカルの生出力・要求別記録は `/tmp/jev-<host>-subscription-{smoke,filter}.{out,err,json}` に保持し、本表は本文や認証情報を含まない派生値である。

**これらの差は削減効果ではない。** 各条件１回の独立セッションで、実行順とキャッシュ状態が異なる。Claude の filter 実行ではローカル選定があっても適用０件であり、Jev は両ホストとも未使用。サブスクリプションでのプロキシ疎通と使用量取得は確認したが、Jev の選択効果、反復した品質・総トークン、請求額は確認していない。なお公式資料によると Claude Code は非 first-party の `ANTHROPIC_BASE_URL` で MCP ツール検索の既定が変わるため、後続の比較ではこの条件も記録する。[Claude Code の環境変数資料](https://code.claude.com/docs/en/env-vars)

## 最終効果判定と公開の時期（2026-09-24 追記）

利用者の指摘により順序を明確化した。現時点の４実行はサブスクリプション認証・通信・使用量の疎通確認であり、Jev による代替を修正・検証していない。Jev キーも未設定で、Claude ではローカル選択の適用が０件だった。この状態で反復しても**現行動作の診断**にしかならず、最終的な総トークン削減の採否には使わない。

先に Jev 判定からホストへの適用までを実際に成立させ、必要なツール・スキルの消費と外部品質を検証する。その後で、事前登録した条件の反復比較により最終効果を判定する。診断のために現行版を測る場合も、修正後の結果と区別する。

GitHub への測定記録・文書の公開は、全作業が終わるまで行わない。途中の生ログはローカルに保持し、公開する場合は最終的な記録形式と内容の監査を先に行う。

## Jev 判定から実ホストの使用・成果までの確認（2026-09-24）

`JEV_SELECTION_MODE=jev`、`JEV_ROUTING_MODE=filter`、`JEV_REASONING=preserve`、`JEV_COMPACTION=off`、`JEV_AUTO_APPLY=off` で、４つの実 CLI に読み取り専用の `go.mod` 抽出を実行させた。Claude Code は `claude-sonnet-5`／`medium`、Codex は `gpt-5.6-terra`／`medium`、Devin CLI は `gpt-5-6-terra-medium`、Grok Build は既定の `grok-4.6-build`。実 Jev の鍵は環境に設定されていたが値は表示・保存していない。ホストの API キーは未設定で、Claude Code／Codex は先の確認どおりサブスクリプション認証を使用した。最終回答は全ホストで `go.mod` の `module github.com/nekowasabi/jev-routing` と `go 1.25.0` に一致した。

| ホスト | Jev 成功／呼出 | 判定の要求への適用 | ツール実行の証拠 | 今回の判定側トークン |
|---|---:|---:|---|---:|
| Claude Code | 3／3 | 0 | `Read` ２回の CLI 記録と結果あり。Jev 助言の挿入は０件 | 入力57,242・出力6,410 |
| Codex | 1／1 | 0 | `go.mod` 読取コマンドと正答あり。Jev の候補被覆不足で無変更 | 入力8,543・出力155 |
| Grok Build | 1／1 | 1 | `read_file` の選定、候補25→2、同名ツールの結果確認済み | 入力17,692・出力334 |
| Devin CLI | 2／2 | 1 | `read` の選定、候補25→1、正答あり。ツール呼出 ID と結果は記録なし | 入力33,572・出力692 |

Codex は別途、同一設定の模擬 Jev 判定で `exec` を選び、候補３→１の書き換え、コマンド実行、正答まで確認した。模擬判定の成功を実 Jev の成功に読み替えない。Claude Code は模擬・実 Jev とも判定自体は成功したが、`selectionApplied=0`。実装上、Claude Code はツール候補を削らず、直近のユーザーメッセージが `tool_result` を含む場合のみ助言を追記する。[実装](../internal/proxy/rewrite.go#L259)・[固定試験](../internal/proxy/claude_advise_test.go#L50)。今回の逐次読取でもその条件を満たした適用は観測されなかった。実ホストの要求形とこの条件の不一致を調べる必要がある。

スキルについては、４ホストを模擬上流で通す `TestRunAppliesSkillOnNormalPath` は成功した。ただしこの試験はスキル名を明示し、`plan.Route` の明示指定が Jev より先に選ばれるため、Jev 判定でスキルを選んだ証拠ではない。[経路](../internal/plan/routing.go#L73)・[試験](../cmd/jev-routing/run_apply_test.go#L18)。実 CLI に模擬 Jev と単独スキルを与えた試行では、Claude Code／Codex とも `lastDelivered` は空であり、最終回答はスキル固有の `proof` 値を含まなかった。Claude Code の回答には、当該値を指示注入と判断した旨が記されたが、プロキシからの配信は確認できないため、これをスキル採用の成否と解釈しない。現在の `autoApply` は同じ候補集合にホストのツールも入れるため、ツールの局所選定が先行するとスキルの Jev 選定へ進まない。スキル配信とモデルによる採用を分けて検証すべきである。[実装](../internal/proxy/proxy.go#L833)・[選定順](../internal/plan/routing.go#L87)

**現時点の判定は部分的な成立に留まる。** Grok は判定→要求変更→同名ツールの結果→正答を確認。Codex は模擬判定で同じ連鎖を確認したが実 Jev 回では候補被覆不足。Devin CLI は判定→要求変更→正答まででツール結果の対応が欠ける。Claude Code とスキルは実適用を確認できない。単発の正答は、Jev が品質を維持し総トークンを削減した証拠ではない。特に判定側のトークン消費を合算して反復比較する必要がある。生出力・統計は `/tmp/jev-<host>-real-application.{out,err,json}` と `/tmp/jev-<host>-mock-{application,skill}*` にローカル保存した。GitHub には公開していない。

## ベンチ・計測・介入の実装と単発診断（2026-09-24）

上節までの未適用は**修正前**の記録。`jev-routing bench` に２つの実証用 MCP ツールを別々に呼ぶ `dual-facts` と、暗黙の Jev スキル選定を成果の合成値で採点する `skill-proof` を追加した。既存のチェス課題は複数工程の隠し採点に使う。ベンチは `JEV_COMPACTION=off`・`JEV_REASONING=preserve` を固定し、Claude Code は `claude-sonnet-5`、Codex は `gpt-5.6-terra`、両者の推論量は `medium` を明示する。サブスクリプション認証のままで実行し、ホスト API キーは未設定。

各ランは `runs.jsonl` と要求別 `proxy-events.json`、対照・介入のペアは版付き許可項目の `comparison.json` に保存する。総トークンはホストのキャッシュ内数を正規化し、上流入力・出力と Jev 入力・出力を合計する。Jev 未適用、外部成果不合格、必要な２ツールの呼出 ID／結果欠落、使用量欠測、設定・実送信モデル不一致、CLI 主モデル使用量との不一致では削減値を `null` にする。Codex の `codex-auto-review` は CLI のターン使用量に含まれなかったが、プロキシで別モデルの推論要求として記録し、総量に加えた。実要求２件の入出力が CLI とプロキシの差に一致した。ダッシュボードは保存済み `comparison.json` をブラウザー内で読み、同じ差分、品質、比較可能数、理由を表示する。模擬ランと実ランのファイル入力を画面操作で確認した。

| 実 CLI の１ペア | 対照品質 | 介入品質 | 対照総トークン | 介入総トークン | 対照−介入 |
|---|---:|---:|---:|---:|---:|
| Claude Code・`dual-facts` | 3/3 | 3/3 | 52,765 | 67,123 | **−14,358** |
| Codex・`dual-facts` | 3/3 | 3/3 | 140,962 | 156,555 | **−15,593** |
| Claude Code・`skill-proof` | 2/3 | 3/3 | — | — | **比較不能：品質不一致** |

`dual-facts` の左右の MCP 結果と CLI の主モデル使用量は両ホストで照合済み。Codex のツールはサンドボックス付き自動承認審査で実行した。`skill-proof` は Jev による選定、スキル配信、実 Claude Code による合成値の採用を確認した。表は各条件１回の**現行動作の診断**であり、反復した削減効果の採否ではない。特に２ツール課題では総トークンが増えている。保存先は `/tmp/jev-bench-dual-claude-verified/`、`/tmp/jev-bench-dual-codex-final/`、`/tmp/jev-bench-skill-live/`。認証値と生ログは GitHub に公開していない。

この節の記録後に `direct` 条件・`x-cell` の共通ランナー・Claude Code 親子計測・Devin の呼出 ID 対応を追加した。現行ダッシュボードのベンチ欄は比較結果ファイルを手動で選ぶ方式で、ライブ要求の下段表示とは別の保存済み結果を示す。反復数と判定閾値を事前固定した効果判定は未実施。

## Grok Build・Devin CLI の実ベンチ診断（2026-09-24）

共通 `xcell-module` 課題を実 Jev と実 CLI で実行した。両ホストとも対照・介入の品質は 3/3 合格。ただし使用量が欠測しているため、トークン削減量は比較不能とした。単発の実行時間差は採否に使用しない。ログは `/tmp/jev-grok-xcell-20260924/`、`/tmp/jev-devin-xcell-20260924/`、修正後の Devin は `/tmp/jev-devin-wire-fixed/` にローカル保存した。

| ホスト | 実 Jev と要求への適用 | 呼出 ID・結果 | 使用量の状態 |
|---|---|---|---|
| Grok Build・`grok-4.7` | 介入で `read_file` を選定して要求を変更。対照・介入とも成果 3/3 | 対照 9/9、介入 8/8 件のアプリ結果を確認 | 終了時の `/v1/responses` １件ずつがキャンセルされ使用量なし。制御要求 `/sessions/.../signals` と `/turn-deltas` は推論件数から除外する修正を加えた。再計測が必要 |
| Devin CLI・`gpt-5-6-terra-medium` | 介入で候補25件から `read`／`write` 等へ絞り込み。対照・介入とも成果 3/3 | 実 Connect の field6 に呼出 ID・名称・引数、field7 と field3 に結果 ID・本文があると確認。修正後の介入で４件すべて同一 ID の結果を確認 | Connect 推論応答は使用量を提供せず、プロキシ記録は入力・出力とも欠測。CLI 標準出力にも使用量なし |

Devin の旧観測は UUID・`call_` 識別子・ツールカタログの `Shell` を実ツール名と誤認し、実際の呼出・結果を記録できなかった。実要求の匿名化した protobuf 構造を調べ、対応するフィールドだけを抽出する固定試験を追加した。実ホスト再実行で `read`・`write`・`find_file_by_name` の呼出４件と結果４件を照合した。Devin の `--export` が生成する会話記録には `final_metrics.total_prompt_tokens` 等が含まれる可能性があるが、このベンチでの出力生成とプロキシとの照合は未検証である。したがって現時点では Devin のトークン削減 KPI は表示しない。

その後、native 履歴の本文に含まれる UUID をツール呼出と取り違えない修正と、ID のない protobuf 葉を呼出開始として数えない修正を加えた。最終コードでの `/tmp/jev-devin-final-wire/` は品質 3/3、実 Jev ４回、`read`・`write`・`find_file_by_name` の実呼出 ID と結果３組を照合した。推論応答５件の使用量は引き続き全件欠測している。ID を持たない旧形式の `exec` 結果は、既存の互換処理が未完了呼出１件に関連付けるため、厳密な ID 対応の証拠には数えない。

Devin の `--export` をベンチへ組み込み、会話記録の `session_id`・空でない `steps`・非負の `final_metrics` を検証して数値だけを `HostTranscript` に保存した。実走行 `/tmp/jev-devin-export-live/` では `promptTokens=60,277`、`completionTokens=384`、`cachedTokens=0`、`steps=12` を取得し、成果は 3/3 合格。生の会話記録は採取後に削除した。このホスト側集計は推論要求ごとのプロキシ使用量と照合できないため、欠測を埋めず、トークン削減 KPI は引き続き比較不能にする。

## `direct`・`x-cell` 共通化と親子計測（2026-09-24）

`jev-routing bench` は `--modes direct,off,on` を同じ課題・採点器で実行する。`direct` はプロキシを通さず、ホスト・子・Jev の総使用量を確証できないため `comparison.json` に対照種別 `direct` の別行を残し、差分を `null` とする。主比較の `off→on` は別行で維持する。旧 `scripts/test-x-cell.sh` は同じ `bench` を呼ぶ薄い入口にし、`xcell-module` と `xcell-locate` を共通ランナーへ登録した。回答とソース無変更を外部採点する。`xcell-locate` の指示にある逐次10回のツール呼出順は、現在の採点対象ではない。

実 Claude Code（`claude-sonnet-5`／`medium`）の `xcell-module` 一回では `direct`・`off`・`on` の品質はすべて3/3。`off` 総69,610、`on` 総113,807で、`off−on` は **−44,197トークン**。`direct−on` は品質が同じでも比較不能として表示した。ローカル記録は `/tmp/jev-bench-direct-claude/`。この１ペアの増加は反復効果の判定ではない。

Claude Code の実要求では `metadata.user_id` を同一ラン内の匿名キーに変換した。ただし実 `Agent` 子セッションは親と同じキー・同じモデル名を使ったため、キーだけでは分離できなかった。主セッションの CLI 使用量と、プロキシの要求別入力・キャッシュ読取・書込・出力の**一意の組合せ**を照合し、残りを子として分類する。複数解、使用量欠測、検証できる要求数の上限超過、Agent の結果欠落では比較不能にする。生の識別値は記録しない。この手法は短い子課題で確認済みであり、任意の長い親子セッションを識別できるとは主張しない。

| 実 Claude Code・`child-facts` １ペア | 対照 | Jev 介入 |
|---|---:|---:|
| 外部成果 | 3/3 | 3/3 |
| 親セッション／子セッション | 1／1 | 1／1 |
| 子トークン（Jev 分込み） | 17,818 | 37,945 |
| 親子・Jev 合計トークン | 81,627 | 122,579 |

両条件とも Agent の呼出 ID と結果、親の CLI 使用量、子に帰属する要求２件を照合し、`off−on` は **−40,952トークン**。再集計した `comparison.json` に子の件数と使用量も保存し、ダッシュボードに表示する。ローカル記録は `/tmp/jev-bench-child-claude-pair/`。Codex・Grok Build・Devin CLI の子セッションで同じ識別を確認したわけではない。子を観測して帰属できないランは差分を表示しない。

Grok Build では[公式のヘッドレス出力仕様](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/14-headless-mode.md)にある CLI 主モデル使用量が、キャッシュ読取を入力に加えた後、プロキシの完了要求と一致した（`/tmp/jev-bench-grok-usage-verified/`）。補助モデル `grok-4.6` の要求も別に記録する。一方、終了時の `grok-4.7` キャンセル要求１件は使用量がなく、ホスト側の主モデル合計との一致だけでは消費ゼロを証明できない。したがって KPI は比較不能のままにする。Devin CLI の `--export` 集計も同様に参考値であり、Connect 応答使用量の欠測を埋めない。

## 使用量の代替出典、呼出順、反復判定（2026-09-24）

上節の使用量判断を再検討し、**要求別の欠測を補完せず、検証済みセッション総量を別出典として採用**した。Grok Build は CLI の `usage_is_incomplete`、モデル別呼出数・入出力・キャッシュ内訳とプロキシの完了要求を照合する。キャンセル要求の個別使用量は依然不明だが、CLI 集計が完了した主要求をすべて覆い、補助要求はプロキシ側で別に加算する。Devin CLI は ATIF-v1.7 の各 agent 手順の入出力・キャッシュ合計が `final_metrics` と一致し、モデル名が指定値と一致し、子軌跡がなく、Connect の全推論要求が使用量欠測の場合だけセッション総量を使う。どちらも出典を `grok_cli_reconciled`／`devin_atif_steps` として保存する。`/tmp/jev-grok-host-source-final/` の `xcell-module` は品質3/3ずつ、総量269,843→269,919（対照−介入 **−76**）。`/tmp/jev-devin-leaf-verify-2/` も品質3/3ずつ、ATIF モデル名 `gpt-5-6-terra-medium` を確認後に322,758→299,994（**+22,764**）の比較可能な１ペアとなった。どちらも１ペアの診断で、削減効果の採否ではない。

`xcell-locate` は回答の５項目に加え、各定義について異なる呼出 ID の検索と読取、結果、対象、順序を確認する。Claude Code の実走行では、読取パスの `/var`／`/private/var` が同じファイルを指すのに文字列比較で拒否されたため、同一ファイル判定に改めた。その後の試行は介入側で10操作と正答を確認したが、対照側が複数名を一度に検索したため `tool_sequence_unverified` とした。課題文を名前ごとの独立検索・読取に明確化した。Codex ではローカル定義検索がツールを全削除し、回答をファイルへ書かず品質0/5となる試行があった。明示された逐次ツール課題はローカル検索で代替しないようにした。その後は off/on とも10操作・正答を確認したが、候補数３件の節約判定で Jev 呼出が０となり、`jev_not_applied` で比較不能。候補２件を足した試行でも実送信候補は３件で、品質4/5かつ Jev 呼出０だった。正答や要求数減少を Jev の改善に読み替えない。

子課題では、Grok の `spawn_subagent` と `get_command_or_subagent_output`、Devin の `run_subagent` と `read_subagent` をそれぞれ起動１件・結果取得１件として分離し、子数を２件に誤計上しないようにした。Codex の実試行は子起動が失敗し成果0/3、独立した子要求もなかった。呼出 ID と結果受信だけでは子の推論を証明できないため、独立使用量を要求する。Grok は親 CLI の９要求とプロキシの使用量付き11要求を親９件・子２件へ一意に分割できたが、追加のキャンセル要求が使用量欠測で、親子総量は比較不能。Devin の実試行は成果3/3で子起動と結果取得を確認したが、保持した ATIF に子軌跡・子参照・子モデル・子使用量がなく、Connect の14推論要求も使用量欠測。親 ATIF を親子合計には流用せず比較不能にする。[Devin の子エージェント仕様](https://docs.devin.ai/cli/subagents)では `subagent_explore` のモデルは親の指定モデルとは独立に選ばれ得る。`--keep` で残す作業領域を次回ベンチの孤児掃除から除外する修正を加え、生の ATIF はローカルで解析後に削除した。

反復効果は事前に `--min-pairs 6`・`--min-savings-pct 0` を固定し、各ペアの総トークン削減率の中央値と正確な二項順序統計による95%以上の区間で判定する。外部品質が悪化すれば優先して不合格、比較不能ペアがあれば効果判定不能、区間が閾値をまたげば保留。実 `dual-facts` の各６ペアでは、Claude Code は中央値 **−14,377.5トークン**・削減率 **−27.24%** で増加判定、Codex は中央値 **−13,432.5トークン**・削減率 **−9.55%** だが区間がゼロをまたぎ保留だった。ローカル記録は `/tmp/jev-bench-dual-claude-reps6/` と `/tmp/jev-bench-dual-codex-reps6/`。単発の Grok・Devin ペアは反復不足の保留とする。GitHub には公開していない。

## 別マシンへの引き継ぎ（2026-09-24 作業停止時）

### 目的・現在地

共通 `jev-routing bench` で `direct`／`off`／`on`、`x-cell` 課題、４ホストの成果・実適用・総トークンを扱い、親子があるランも欠測をゼロ補完せず判定する。主指標は上流の親・子と Jev の**キャッシュ込み総トークン**。品質は前提、時間は副指標。コンパクションは選択と別条件とする。コードとこのメモは `bdd5251` の作業木上で**未コミット**。GitHub への公開・プッシュは、ユーザーの指示どおり全作業が終わるまで行っていない。

### 完了した変更と根拠

- `internal/bench/run.go`・`comparison.go`・`effect.go`・`report.go` とダッシュボードに、同一設定ペア、出典付き総トークン、品質・Jev 適用・必要操作・欠測のゲート、６ペア以上の効果判定を実装。`direct` は同一課題の品質を採点するが総量差は `null`。`scripts/test-x-cell.sh` は共通ランナーを呼び、Claude `claude-sonnet-5`／中、Codex `gpt-5.6-terra`／中、Devin `gpt-5-6-terra-medium` を既定にする。
- `internal/bench/xcell.go` は `xcell-locate` の独立した検索５回・読取５回を呼出 ID、成功結果、対象、順序で確認する。macOS の `/var`／`/private/var` 同一ファイルを受け入れる。`internal/proxy/lookup.go` は逐次操作を指定された課題をローカル検索だけで回答しない。
- `internal/bench/grok_usage.go` は Grok CLI の完全なモデル使用量と完了要求を照合する。同じプロキシモデル名で補助要求が混ざる場合、要求別使用量の**一意の部分集合**だけを認める。キャンセル要求の個別使用量は作らない。`internal/bench/devin_transcript.go` は ATIF 手順合計・最終集計・モデルを検証する。`internal/bench/usage_source.go` は検証できたセッション総量のみ採用する。`report.go` もこの出典の総量を表示する。
- `internal/bench/child_hosts.go`・`gateway.go` は子起動と結果取得を分け、Codex と Grok では独立した子要求の使用量帰属を必要とする。Devin の子使用量・モデルを確認できないランは比較不能。`--keep` 作業領域には `.bench-keep` を置き、後続ベンチの孤児掃除から除外する。生の ATIF は通常ランでは削除する。

### 実測結果と保存先

| 対象 | 記録 | 判定 |
|---|---|---|
| Claude Code `dual-facts` ６ペア | `/tmp/jev-bench-dual-claude-reps6/` | 全比較可能、削減率中央値 −27.24%、**増加** |
| Codex `dual-facts` ６ペア | `/tmp/jev-bench-dual-codex-reps6/` | 全比較可能、削減率中央値 −9.55%、区間がゼロをまたぎ**保留** |
| Grok Build `xcell-module` １ペア | `/tmp/jev-grok-same-model-fixed-20260924/` | 品質3/3ずつ、照合済み総量404,364→270,444、単発のため効果保留。`bench report` で要約の総量表示も確認 |
| Devin CLI `xcell-module` １ペア | `/tmp/jev-devin-leaf-verify-2/` | 品質3/3ずつ、ATIF 出典の総量322,758→299,994、単発のため効果保留 |
| Claude Code `child-facts` １ペア | `/tmp/jev-bench-child-claude-pair/` | 親子１件ずつを一意に帰属、81,627→122,579 |
| Grok Build 反復の中断分 | `/tmp/jev-grok-xcell-reps6-fixed-20260924/` | ２ペアは品質・適用・使用量が揃い、差分 −10,889／−64,401。３ペア目の対照実行中にユーザーの停止指示を受け、終了信号で停止。３ペア目は不完全、**反復効果を判定しない** |

`xcell-locate` は Claude Code の介入側と Codex の両条件で10操作の証拠を確認した試行がある。ただし Claude の対照が検索をまとめた試行、Codex が Jev を節約ゲートで呼ばない試行、Codex の品質4/5の試行があり、これらの削減値は `comparison.json` で `null`。Grok／Devin の現在の最終ログでは対象・順序を証明できず、この課題は比較不能。子課題も Grok のキャンセル使用量と Devin の子軌跡・モデル・使用量が欠けるため、両者の親子総量は比較不能。Devin の ATIF 原本は分析後に削除した。

### 検証済みの範囲

作業停止前に `go test ./... -count=1` は全 Go パッケージ成功。`python3 scripts/test_summarize_x_cell.py` は17件成功、`node --test internal/proxy/assets/dashboard.test.mjs` は17件成功、`bash -n scripts/test-x-cell.sh` と `git diff --check` も成功。後二つの言語の試験後に変更したのは Go、文書、シェルのホストモデル引数であり、シェル構文は再確認済み。長時間の Grok 反復はユーザーの停止指示により途中終了し、未完了を成功に数えない。

### 次に行うこと

1. **作業木を移す。** `docs/MEMO.md` と `docs/requirements/unified-benchmark-validation.md` を含む多数の未追跡・未コミットファイルがある。別マシンに Git のコミットだけを取得してもこの変更は現れない。作業木を安全に転送してから `git status --short` で一致を確認する。`/tmp/jev-*` の生ログはローカル診断用で、転送・公開時は内容を精査する。GitHub にプッシュしない。
2. **反復を完成させる。** Grok Build の中断済みシリーズは効果判定に使わず、環境を確認して `JEV_SELECTION_MODE=jev go run ./cmd/jev-routing bench --agent grok --tasks xcell-module --modes off,on --reps 6 --out <新しいローカル結果先>` で新規６ペアを取る。続いて Devin CLI は `--agent devin --model gpt-5-6-terra-medium` で同じ課題・６ペアを取る。実サービスとクォータを使う。各ペアで `comparison.json` の品質・Jev 適用・使用量出典を見てから効果欄を読む。途中で比較不能が出たら原因を修正し、古いデータへ後付けで成功値を入れない。
3. **未充足の測定契約を扱う。** Grok 子のキャンセル要求、Devin 子の別モデル・使用量は現在取得できない。提供側に完全な子使用量の出典がない限り `child_session_unverified`／`usage_incomplete` を維持する。`xcell-locate` の Grok／Devin 操作順も原ログから証明できない。`docs/requirements/unified-benchmark-validation.md` に記載した「失敗も含む成果一件あたり総トークン」の群間推定は未実装で、現行の効果判定は全ペア品質合格時に限る。
4. **最後に成果を整理する。** ４ホストの結果をダッシュボードへ読み込み、総量と出典・比較不能理由が `comparison.json` と一致するか確認する。必要な修正が終わるまで公開しない。

### 制約と注意

Claude Code／Codex はサブスクリプション認証を使用し、ホスト API キーは未設定だった。TypeSafe 鍵の値は表示・保存していない。秘密、生ログ、作業木をそのまま GitHub に置かない。`--keep` は作業領域と Devin の ATIF 原本を残すので、診断後にローカルで処理する。別マシンの認証状態・ツール版・クォータは未確認である。
