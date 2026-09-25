# jev-routing：トークン増加と機能分離の調査メモ

記録開始日：2026-09-24。コード・文書の調査、固定通信試験、実ホスト試験の経過を時系列で残す。初期の「未実施」「未修正」は記録時点の状態を指し、現在の状態は次節を正本とする。

**最新の引き継ぎは末尾の「引き継ぎ（2026-09-25 セッション終了時）」を参照。**

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

## Grok Build・Devin CLI の６ペア反復（2026-09-24 引き継ぎ後）

引き継ぎの手順2を実施した。最初の Grok シリーズ `/tmp/jev-grok-xcell-reps6-20260924b/` は、rep1 の対照ランだけ主モデルが `grok-4.6` になり `settings_mismatch` で比較不能になった（効果判定は `incomplete_pairs`）。原因は `bench` が `--model` 未指定時に Claude／Codex だけ既定モデルを補い、Grok CLI に `--model` を渡さずモデル解決を CLI 起動時の処理に任せていたこと。`~/.grok/config.toml` の既定は `grok-4.7` だが、シリーズ初回の起動だけ全８要求が `grok-4.6` だった。CLI 内部でなぜ初回だけそうなるかは未確認。[既定値補完](../internal/bench/run.go#L140) に `grok` → `grok-4.7` を追加し、このシリーズは効果判定に使わず、`--model grok-4.7` を明示して新規に取り直した。全ランに出るセッションタイトル生成の補助要求（`grok-4.6`、入力約480）は CLI の仕様とみられ、補助モデルとして別に加算される。

| ホスト・課題 | 記録 | 比較可能／全ペア | 総トークン差の中央値 | 削減率の中央値（95%以上区間） | 判定 |
|---|---|---:|---:|---|---|
| Devin CLI `gpt-5-6-terra-medium`・`xcell-module` | `/tmp/jev-devin-xcell-reps6-20260924b/` | 6／6 | +87,730.5 | 57.56%（41.86%〜71.07%） | **減少** |
| Grok Build `grok-4.7`・`xcell-module` | `/tmp/jev-grok-xcell-reps6-20260924c/` | 6／6 | +14,339.5 | 11.30%（−2.51%〜48.73%） | **保留**（区間がゼロをまたぐ） |

両シリーズとも全12ランで品質3/3、介入側の Jev 適用あり、実モデルは指定どおりで、事前固定の `--min-pairs 6`・`--min-savings-pct 0` で判定した。Devin の使用量出典は `devin_atif_steps`（ATIF の手順合計、`final_metrics` 一致、子軌跡なし）であり、Connect の要求別使用量は引き続き欠測している。したがって Devin の「減少」は、検証済みセッション総量という別出典による判定である。Devin の介入側の総量は６ペアすべてで約63,000〜82,000にそろい、対照側は112,723〜228,230とばらついた。Grok の各ペアの差は −2,064／−218／−3,172／+28,897／+30,568／+123,160 で、３ペアは僅かな増加だった。対照 rep6（252,758）が外れ値である。

この判定は `xcell-module`（単一の読み取り課題）に限る。他の課題、スキル、子エージェント、製品全体の改善へ読み替えない。Claude Code・Codex の `dual-facts` の結果（増加／保留）とも課題が異なるため、ホスト間では比較しない。

## Claude Code `dual-facts` 増加の分析（2026-09-24）

ホストを一つずつ改善する方針とし、Claude Code から着手した。前回の６ペアの記録は前のマシンにしかないため、`--catalog 2`、`claude-sonnet-5`／`medium` で６ペアを取り直した（`/tmp/jev-bench-dual-claude-reps6-b/`）。全ペアが比較可能で品質3/3、各ペアの差は −14,096〜−14,425 だった。削減率の中央値は **−27.25%**（区間 −27.32%〜−26.54%）で判定は**増加**。前回（−27.24%）を再現した。

| 項目（on−off の中央値） | トークン | 総差に対する割合 |
|---|---:|---:|
| Jev 入力 | +13,661.5 | 94.9% |
| Jev 出力 | +618 | 4.3% |
| 上流 入力＋キャッシュ読取＋書込＋出力 | +118.5 | 0.8% |

12ランすべてで総トークンが各項目の和に一致することを確認した。rep1 のみ上流のキャッシュ読取・書込が ±8,000 程度入れ替わったが、合計への影響は小さく、原因は未特定。

要求別（rep2）では、off と on のどちらも上流の推論要求が３件で、順序・ツール・上流使用量はほぼ同じだった。on は３要求すべてで Jev を１回ずつ呼び、平均すると１回あたり入力約4,550・出力約206になる（呼出ごとの内訳は記録がなく、均等割りの推定）。判定は３件とも実際の次の行動と一致した。ただし１件目は観測のみで適用されず、２・３件目は助言として挿入された（上流入力 +38〜40）。助言によるキャッシュ接頭辞の破壊は見られなかった。

**結論**：Claude Code の現行介入（`advise`）は、ツール候補を削らず（[実装](../internal/proxy/rewrite.go#L262)）、要求数も変えないため、上流のトークンを減らす仕組みを持たない。この課題では主モデルがもともと最短の３要求で解くので、助言で改善する余地もない。増加分の99%は Jev 判定の費用で、１要求ごとに全候補ツールの説明と直近履歴を送っている（[判定要求](../internal/proxy/rewrite.go#L1248)・[付帯情報](../internal/proxy/judgment.go#L110)）。Claude Code で純減させるには、(1) 効果のない Jev 呼出を省く（適用されない判定、最終応答の判定）、(2) Jev 入力を縮める、(3) 上流の推論やターンを実際に減らす介入に変える、のいずれかが必要である。また、主モデルが迷って余分なターンを使う課題でなければ、(3) の効果は測れない。どれを採るかは未決定。

## Claude Code：Jev 判定費用の削減と、上流推論の置換の検討（2026-09-24）

利用者の選択により、上節の (1)(2) を実装した。(1) Claude の advise 経路で、助言を挿入する `tool_result` がない要求では Jev を呼ばない（理由 `no_advise_target`。もともと適用されず捨てていた判定）。(2) Jev の判定要求から、`actions_taken` と同じ内容の `recent_tool_results` を削除した。また Claude のときだけ、候補ツールの説明文を最初の段落・300バイト以内にした（[shortenCriteria](../internal/proxy/transform.go)）。上流へ送るツール定義は変えていない。他ホストの判定要求も変えていない。回帰テストを追加し、`go test ./...` は557件成功。未コミット。

| `dual-facts`・`claude-sonnet-5`／`medium`・６ペア | 修正前 `/tmp/jev-bench-dual-claude-reps6-b/` | 修正後 `/tmp/jev-bench-dual-claude-reps6-c/` |
|---|---:|---:|
| 対照−介入の中央値 | −14,388 | −3,276 |
| 削減率の中央値（区間） | −27.25% | −6.20%（−6.25%〜−6.16%） |
| Jev 呼出／ラン | 3 | 2 |
| Jev 入力の中央値 | 13,661.5 | 2,748.5 |
| Jev 出力の中央値 | 618 | 408 |
| 品質・比較可能 | 3/3・6/6 | 3/3・6/6 |

修正後も判定は `Write`→`respond_to_user` で、実際の次の行動と一致した。判定は依然として**増加**である。advise は上流の要求を減らさないため、Jev の費用を減らしても、良くて対照と同じ量までしか戻らない。利用者の指摘どおり、これは本質的な解決ではない。

純減には、Jev の判定で上流の推論を置き換える必要がある。既存の `direct` は Claude では使えない。Claude の要求は advise 分岐で先に返り（[rewrite.go](../internal/proxy/rewrite.go#L457)）、適用条件が `chat` 形式に限られ（[rewrite.go](../internal/proxy/rewrite.go#L498)）、合成できる応答も OpenAI Chat 形式だけである（[direct.go](../internal/proxy/direct.go#L174)）。引数の合成も、スキーマの全項目が `const` の場合に限られる（[direct.go](../internal/proxy/direct.go#L48)）。

`dual-facts` の上流３要求は、①２つの MCP ツールの並列呼出、②結果から計算した値での `Write`、③最終応答である。引数なしで置き換えられるのは①だけである。Jev の Choice は１候補しか返さないため、①を１ツールだけ置き換えても、残りのツールを呼ぶための上流推論が１回増え、純減はほとんど生じない。２つの呼出を１つの合成応答にまとめた場合の概算は、17,000 − Jev 約1,600 ≈ **約15,000（約30%）の純減**である（実測ではない）。ただしこの節約は「最初の手が引数なしで決まる」という課題の性質に強く依存する。引数を主モデルが決める一般的な作業では、Jev が引数まで生成する必要があり、誤判定時には主モデルの判断を上書きする危険がある。ベンチ専用の最適化を避けるため、方向の決定は利用者に委ねた。


## 実作業での Read 置換の再現実験（2026-09-24）

利用者の選択により、置換の余地を実作業で計測した。直近30日の Claude Code メインセッション1,622件から200件を無作為に抽出した（seed 20260924、1,943ターン）。テキストを含まない tool_use だけのターンで、全引数がそれ以前の会話・ツール結果に現れる値からなるものを置換候補とした。候補は250件で、総費用の11.4%。うち197件が `Read` だった。Jev 費用（１回1,600と仮定）を差し引いた上限は総費用の11.25%である。集計は `/tmp/claude-turn-audit/`。この環境の記録には `isSidechain` のサブエージェントがなく、子は集計できていない。

次に、候補ターン100件と非候補ターン100件で、プロキシと同じ判定要求を実 Jev に送り、次のツールと Read のパスを予測させた。次のツールは `askNextTool` などの実コードを `go test -overlay` で呼んだ。ツール定義は記録にないため、既出のツール名と短い説明で近似した。パスの選択はリポジトリに実装がないため、同じ HTTP 契約の Python で模倣した。会話の原文と送信した本文は保存していない。記録は `/tmp/jev-replay/`。21件は Jev 入力の上限超過で失敗し、比較不能として除外した（計測179件）。

親セッションで `Read` の置換に限って再計算した（２つの質問の確信度の小さい方で閾値を適用）。サブエージェントが作成した閾値表は、Read 以外の予測も置換数に含めていたため採らない。

| 閾値 | 置換 | 完全一致 | パス誤り | Read 以外への誤置換 | 省けた上流 | 誤置換の損失（そのターン費用×1と仮定） |
|---|---:|---:|---:|---:|---:|---:|
| なし | 49 | 16 | 27 | 6 | 1,439,709 | 3,170,276 |
| 0.5 | 18 | 9 | 6 | 3 | 761,155 | 813,907 |
| 0.7 | 6 | 5 | 1 | 0 | 433,888 | 70,317 |
| 0.9 | 1 | 1 | 0 | 0 | 175,572 | 0 |

Jev の実測使用量は2,057,185で、標本の上流総費用25,400,945の約8%である。実作業では判定１回の入力が中央値7,406（最大33,234）であり、`dual-facts` の約1,370を大きく上回る。最良の閾値0.7でも、純損益は約 −169万（−6.7%）になる。標本は候補を過剰に含むため、実際の構成ではさらに悪化する見込みである。

**結論**：`Read` の置換は採算が合わない。より重要なのは、現行の Claude 向け advise が上流を減らさずに、１ターン数千トークンの Jev 費用を加えている点である。実作業では約8%の純増と推定される（標本からの推定であり、実走行の反復比較ではない）。Claude Code の費用の大半は、ターンごとの文脈の再読（標本の平均は約13万／ターン）で占められる。したがって、節約にはターン数か文脈の大きさを減らす必要があり、次のツールの選択はどちらにも効きにくい。次の方向は利用者の判断を待つ。

## Claude Code：Jev を使わない構造的削減の解析（2026-09-24）

利用者の選択（A）により、Jev を使わず、プロキシの決定的な処理だけで主モデルのターンを不要にできる余地を、実作業の記録で調べた。対象は直近30日・メインセッション172件・2,017ターンで、総費用は266,467,486。集計は `/tmp/claude-structural/structural_audit.py`（assert 5件）。

| 避けられた可能性のあるターン | 件数 | 総費用比 | 判断 |
|---|---:|---:|---|
| 待機・ポーリング（sleep を含む Bash、TaskOutput 等） | 41 | 3.0% | 唯一の候補。同じポーリング呼出の結果が実行中なら、同じ呼出をプロキシが合成する決定的規則が考えられる。上流を呼ばずに応答を返す既存例は [native_compact.go](../internal/proxy/native_compact.go) |
| エラー直後の同一ツール再試行 | 165 | 6.6% | ほぼ不可。再試行は主モデルによる引数修正を伴い、モデルなしでは合成できない |
| ToolSearch だけのターン | 64 | 2.4% | 非推奨。スキーマを先に渡すと以後の全ターンの文脈が膨らみ、選択機能の方針とも逆行する |
| 短い中間テキスト | 44 | 2.7% | 不可。プロキシは推論が終わった後にしか関与できない |
| 同一引数の読み取り再呼出 | 5 | 0.2% | 効果なし |
| 記録専用ツールのみ | 0 | 0% | 該当なし |
| 重複除去後の合計 | 318 | 14.8% | 上の判断により、現実的な上限は約3% |

文脈の大きさの側では、`Read` の結果と doctrine 系 MCP の大きな応答（中央値1.7万〜2.9万文字）が大きい。一方、フックの注入文は毎回内容が異なり、完全一致の重複はほぼなかった。これらを削るのはコンパクションの領域であり、選択機能とは別に扱う。

**結論**：プロキシが上流の推論を省くには、主モデルの次の応答をモデルなしで合成する必要があり、それが決定的にできるのは同一の状態確認の反復程度である。次の段階として、記録上でポーリング規則の一致率（前回と同じポーリング呼出の結果が実行中のとき、主モデルが次も同一の呼出をしたか）を測り、合成の安全性を判断する。

## ポーリング合成規則の検証（2026-09-24）

前節の唯一の候補を、会話記録だけで検証した（外部サービスは不使用）。対象は直近30日の全メインセッション1,617件・15,541ターンで、総費用は2,286,146,622。集計は `/tmp/claude-polling/analyze.py`（assert 4件）。ポーリング型は、TaskOutput／Monitor／BashOutput、status・wait 系の MCP、sleep を含む Bash、同一セッション内で同じコマンドを繰り返す Bash とした。

| 指標 | R1：直前の結果が未完了なら同じ呼出を合成 | R2：直前の結果が前回と同一なら合成 |
|---|---:|---:|
| 発火 | 106 | 32 |
| 完全一致 | 19（17.9%） | 3（9.4%） |
| 引数の変更 | 24 | 8 |
| 別のツール | 47 | 15 |
| テキストのみ（完了報告・ユーザーへの応答） | 14 | 5 |
| 節約の上限（総費用比） | 0.28% | 0.05% |

約８割が誤合成となり、13〜16%はユーザーへの応答を奪う。sleep を含む呼出は、合成しても待ち時間が変わらない。**実装しない。** Claude Code では、Jev を使う選択は実作業での判定費用のため赤字になり、Jev を使わない決定的な合成もほとんど余地がない。費用の大半はターンごとの文脈の再読であり、削減の余地は文脈の縮小（コンパクション）側にある。

## Claude の助言停止とコンパクションの分析（2026-09-24）

**助言の既定停止**：利用者の決定により、Claude の通常要求では既定で Jev もローカル選定も呼ばず、本文を無変更で転送する（理由 `claude_advise_disabled`、[実装](../internal/proxy/rewrite.go#L197)）。従来の助言は `JEV_CLAUDE_ADVISE=on` で有効になる。ベンチの Claude `on` 条件でだけこれを有効化し、比較を維持する。スキル配信、圧縮要求、他ホストは変えていない。`go test ./...` は559件成功。未コミット。Shadow 観測と Claude の組合せは未検証。

**参考：jev-gateway のベンチ**：[jev-gateway](https://github.com/vinilana/jev-gateway#benchmark) の Claude Code の増減は、同じ `hint`（助言）方式による作業経路の変化で生じている。作者自身、Claude Code については "not lower cost or latency" と書いている。LLM 要求数は −26%〜+47% で、トークンの増減と連動する。表のトークンに Jev の使用量は含まれず（金額のみ別記）、各５ランの非ペア中央値である。当方の `dual-facts` は経路が固定された課題で、助言の構造的な損益（純増）をばらつきなしで示したものと位置づける。

**コンパクションの分析**：直近30日から21セッション・269ターンを抽出して解析した（`/tmp/claude-compaction/analyze.py`。標本が小さく目安）。文脈の成長に寄与するのは、MCP 結果 22.4%、assistant 出力 19.8%、Read 16.3%、Bash 14.2% の順。方策別の純額（総費用比）は次のとおり。S1（新しい大きなツール結果を初回送信時に先頭と末尾へ切り詰める。未キャッシュの内容だけを変えるので接頭辞を壊さない）は2,000トークン超で +4.2%、5,000トークン超で +2.2%。S2（K ターン前の結果を独自に消去する）は、キャッシュの書き直しで −18〜−46%（最悪ケースの近似）となり不可。S3（重複除去）は +0.02%。

**ネイティブ context editing の確認**：サブスクリプション認証（`authMethod=claude.ai`）の実 Claude Code を最小の中継プロキシで観測した（`/tmp/claude-ctxedit/`。認証情報と本文は保存していない）。Claude Code はすでに毎要求 `anthropic-beta: context-management-2025-06-27` と `context_management.edits=[clear_thinking_20251015]` を送っており、応答にも `applied_edits` が返る。既存の edit を保持したまま `clear_tool_uses_20250919`（最小の trigger・keep・clear_at_least）を追記すると、全要求が 200 で受理され、`applied_edits` に `cleared_tool_uses: 2, cleared_input_tokens: 58` が返った。回答も正しかった。[公式資料](https://platform.claude.com/docs/en/build-with-claude/context-editing)の `clear_at_least` で消去をまとめれば、S2 の欠点であるキャッシュ再構築の頻度を抑えられる可能性がある。総トークンへの効果は未測定（各１回のみ）。

## Claude Code で tool call の置き換えを断念した理由（README 転記用・2026-09-25）

**結論**：Claude Code では、Jev でツール選択を代替・誘導する方法（`hint`／`advise`）でも、Jev を使わない決定的なツール呼出の合成でも、品質を保ったまま総トークンを減らすことはできなかった。そのため Claude Code への Jev 助言は既定で無効とし（`JEV_CLAUDE_ADVISE=on` で有効化）、トークン削減は Anthropic ネイティブの context editing（文脈の縮小）で狙う方針に切り替えた。

**なぜ構造的に減らないのか**
- Claude Code は拡張思考（thinking）を有効にして動作し、Anthropic API は thinking 中のツール強制（`tool_choice`）を拒否する。さらにツール一覧や `tool_choice` を変えるとプロンプトキャッシュが無効になる。このためプロキシにできるのは、ツールを削らずに次の一手を助言することだけである。
- 助言は上流の要求数も文脈の大きさも変えない。一方で、Jev の判定費用は毎ターン加わる。主モデルがもともと最短の経路で解く課題では、Jev の費用がそのまま純増になる。

**調査したことと結果**

| 調査 | 方法 | 結果 |
|---|---|---|
| 助言の構造的な損益 | `dual-facts`（経路が固定された課題）、`claude-sonnet-5`／`medium`、６ペア | 総トークン中央値 −27.25%（増加）。増加分の99%が Jev 判定の費用で、上流の要求・使用量は対照とほぼ同一 |
| Jev 費用の削減 | 助言を付けられない要求の判定を省略し、候補説明文を短縮 | −6.20% まで改善したが、依然として増加。良くても対照と同量にしか戻らない |
| 実作業での置換余地 | 直近30日の実セッション200件（1,943ターン）を集計 | 引数が既出の値だけの「ツールのみのターン」は費用の11.4%（大半が `Read`）。Jev 費用を引いた上限は11.25% |
| Jev による `Read` 置換の的中率 | 実ターン200件でプロキシと同じ判定要求を実 Jev に送る再現実験 | 実作業では Jev 判定１回の入力が中央値7,406トークン。最良の閾値でも純損益は約 −6.7% で、全閾値で赤字。現行の助言は実作業で約8%の純増と推定 |
| Jev なしの決定的な合成 | 実セッション1,617件・15,541ターンで、ポーリングを同一呼出で合成する規則を検証 | 精度17.9%、節約上限0.28%。13〜16%はユーザーへの応答を奪うため、実装しない |
| 他製品との照合 | [jev-gateway のベンチマーク](https://github.com/vinilana/jev-gateway#benchmark) | 同じ `hint` 方式。作者自身が Claude Code では "not lower cost or latency" と記述。増減は要求数（−26%〜+47%）の変化、すなわち経路のばらつきに連動し、表のトークンに Jev 分は含まれない |

**採った対応**：Claude の通常要求では、既定で Jev を呼ばずに無変更で転送する。Claude Code の費用の大半はターンごとの文脈の再読なので、削減は文脈の縮小側で行う。独自の履歴書き換えはキャッシュを崩して赤字になる（試算 −18〜−46%）。そのため、サーバー側で古いツール結果を消す Anthropic のネイティブ機能 `clear_tool_uses_20250919` を、プロキシが付加する方式とした（`JEV_CLAUDE_CLEAR_TOOL_USES=on`）。サブスクリプション認証の Claude Code でも受理されることを確認済み。

## Claude Code：ネイティブ context editing の計測（2026-09-25）

`jev-routing bench --agent claude --tasks chess-bugfix --modes off,on --claude-clear`（trigger 30000・clear_at_least 10000・keep 3、`claude-sonnet-5`／`medium`）で計測した。計測を完走させるためにベンチを３点修正した。(1) 各エージェントを bwrap で実行し、ランごとに専用の tmpfs `/tmp` を与える（他ランの残骸が見えず、終了時に破棄される）。(2) 監査の created 判定を、開始時スナップショットで行う（`cp`・`node` での書込・相対パス作成を誤って汚染と判定していた）。(3) pid 再利用で古い砂場が残る不具合を直し、ロック取得後に孤児を無条件で掃除する。消去が発生しなかったランも除外しない（除外すると長いランだけが残る選択バイアスになるため）。`go test ./...` は583件成功。

| rep | 対照 → 介入 | 対照−介入 |
|---:|---|---:|
| 1 | 1,517,732 → 1,064,659 | +453,073 |
| 2 | 2,014,989 → 804,403 | +1,210,586 |
| 3 | 1,473,057 → 1,746,272 | −273,215 |
| 4 | 1,091,615 → 450,045（消去なし） | +641,570 |
| 5 | 1,328,669 → 1,516,428 | −187,759 |
| 6 | 1,645,054 → 906,904 | +738,150 |

記録は `/tmp/jev-bench-claude-clear-reps6-c/`。６ペアすべて比較可能で、汚染は０、品質は全ラン36/36。削減率の中央値は **+37.4%** だが、区間が −18.5%〜+60.1% とゼロをまたぐため、判定は**保留**である。消去が起きた５ランでは、入力から除かれたトークン（要求ごとの合計）は144,840〜293,748で、キャッシュ書込の増分（対照中央値との差、約２万〜11万）を上回った。つまり経路が不変と仮定すれば、各ランで差し引きプラスと推定される。一方、主モデルの経路のばらつき（同条件で約4倍）が差を覆っている。

**事前登録（結果を見る前に記録）**：設定を変えずに６ペアを追加し（`/tmp/jev-bench-claude-clear-reps6-c2/`）、上記と合わせた**12ペアで一度だけ判定する**。12ペアでも保留なら、消去回数を減らす設定（clear_at_least を上げる）に変え、改めて12ペアを事前登録して計測する。途中結果を見ての打ち切りや延長はしない。


## Claude Code：context editing 12ペアの判定と v2 の事前登録（2026-09-25）

事前登録どおり、同じ設定で６ペアを追加した（`/tmp/jev-bench-claude-clear-reps6-c2/`）。先の６ペアと合わせ、rep を7〜12に振り直して結合し、`bench report` で再計算した（`/tmp/jev-bench-claude-clear-12/`）。12ペアすべてが比較可能で、品質は全ラン36/36、汚染は０、消去の発生は11/12ペア。削減率の中央値は **+10.3%**、区間は −18.5%〜+44.9% で、判定は**保留**である。追加した６ペアでは、６ペア中４ペアで増加した（差 −106,337／−775,617／+641,683／−353,508／+421,968／−101,473）。

要求別に見ると、消去の仕組み自体は差し引きプラスと推定される。消去ありのランでは、入力から除かれたトークン（要求ごとの合計）が１ランあたり145,000〜440,000ある。一方、キャッシュ書込は off の約45,000〜64,000に対して on は75,000〜165,000で、１ランに３〜６回の書込急増がある。ただし上流要求数の中央値は off 32.5 に対して on 36 と増えている。以前の分析でも、手戻りの大半はファイルの `Read` の読み直しだった。消去によってファイル内容が失われ、再読でターンが増え、仕組みによる節約を相殺していると推定する（因果は未検証）。

**v2 の事前登録（結果を見る前に記録）**：`exclude_tools: ["Read"]`（ファイル内容は残し、古いテスト出力などだけを消す）と `clear_at_least` 20000（消去回数とキャッシュ再構築を減らす）に変更する。trigger 30000・keep 3 は据え置く。同じ課題・モデルで12ペアを計測し、12ペアで一度だけ判定する。v2 でも保留なら、計測値（手戻り・書込・消去量）に基づいて次の設定を再度事前登録する。

## Claude Code：v2 の結果、同一経路の純削減指標、確認系列の事前登録（2026-09-25）

**v2 は検証不成立**：`exclude_tools: ["Read"]`・clear_at_least 20000の12ペア（`/tmp/jev-bench-claude-clear-v2-12/`）では、24ラン全て品質36/36だったが、消去が一度も起きなかった。Read を除くと、keep 3 より古い消去可能量が20,000に達しなかった（文脈は最大91,130まで伸びた）。事前登録の判定は、rep10 の対照がホスト使用量とプロキシの不一致で比較不能となり、判定不能（11/12）。この系列は実質的に同一条件同士の A/A 試験となり、ペアごとの「削減率」は −271%〜+90%（中央値 +20.9%）に散った。`chess-bugfix` でラン間の総トークンを比べても、10〜30%規模の効果を12ペアで判定するのは原理的に困難である。

**同一経路の純削減指標**：ラン間比較とは別に、消去が起きた on ランについて、同じ経路上の反実仮想を API の `applied_edits` から計算する指標を bench に実装した（[clearnet.go](../internal/bench/clearnet.go)）。要求ごとに入力から除かれた量の和から、消去によって増えたキャッシュ書込（反実仮想の文脈増分を超えた cache_creation）と、手戻り（初回消去後に keep より古い呼出と同じツール・入力を再実行したターンの費用）を差し引く。ターンと要求の対応が取れないランは欠測とし、ゼロでは補わない。ラン間比較（保留判定）は置き換えずに併記する。v1 の12ペア（`/tmp/jev-bench-claude-clear-12/`）を再計算すると、消去が起きた11ランすべてで純削減は正、中央値 **12.0%**（2.8%〜17.7%）だった。手戻りを差し引かない試算では中央値14.5%だった。限界：手戻りは完全一致の再実行しか数えず、消去が主モデルの判断を間接的に変える影響は、ラン間比較でしか捉えられない。

**確認系列の事前登録（結果を見る前に記録）**：この指標は同じ12ランを見ながら定義したため、新しいデータで再現を確かめる。v1 設定（trigger 30000・clear_at_least 10000・keep 3、Read 除外なし）で新たに６ペアを計測する（`/tmp/jev-bench-claude-clear-confirm/`）。合格条件は、全ランの品質合格、消去が起きた on ランの同一経路純削減がすべて正、かつ中央値5%以上。ラン間比較の判定も併記する。

## Claude Code：確認系列の結果（2026-09-25）

事前登録した確認系列（`/tmp/jev-bench-claude-clear-confirm/`、v1 設定、`claude-sonnet-5`／`medium`、bwrap 隔離）は、合格条件をすべて満たした。

| 合格条件 | 結果 |
|---|---|
| 全ランの品質合格 | 12ラン全て36/36、汚染0 |
| 消去が起きた on ランの同一経路純削減がすべて正 | 消去は6/6ランで発生、6ランすべて正（4.8%〜14.8%） |
| 同一経路純削減の中央値5%以上 | **8.5%** |

on ラン別（節約／追加書込／手戻り／純削減／率）：92,560／10,061／44,291／38,208／4.8%、96,793／7,096／27,158／62,539／9.8%、50,241／13,487／0／36,754／5.2%、117,320／6,833／26,123／84,364／11.6%、91,812／22,908／0／68,904／7.2%、158,705／9,547／0／149,158／14.8%。

ラン間比較も今回は**減少**と判定された（中央値44.7%、区間19.4%〜75.6%、6/6ペアで on が少ない）。ただし A/A 相当の v2 系列で同一条件の差が −271%〜+90% に散ったことから、この効果量には偶然が大きく含まれるとみなし、根拠には採らない。

**結論**：Claude Code では、プロキシが Anthropic ネイティブの `clear_tool_uses_20250919`（trigger 30000・clear_at_least 10000・keep 3）を付加すると、品質を保ったまま同じ経路の総トークンが減る。独立した２系列（v1 の12ペア：中央値12.0%、確認系列：中央値8.5%）で、消去が起きた全17ランが正だった。

**限界**：対象は `chess-bugfix` の１課題・１モデル。閾値は、ベンチの文脈（最大4万〜9万トークン）で消去が起きる値を選んだ。実作業の文脈（中央値約13万／ターン）に合う既定値（API 既定の trigger 100000 など）は未検証。手戻りは完全一致の再実行だけを数えている。機能は既定で無効（`JEV_CLAUDE_CLEAR_TOOL_USES=on` で有効化）。変更はすべて未コミットで、GitHub には公開していない。

（追記 2026-09-25：上記の変更はその後コミット `d1dcfe9`・`786840c`・`d31a508` として `origin/main` へプッシュ済み。）

## Claude Code：実作業向け設定とばらつきの調査（2026-09-25）

利用者の方針：節減は確認できたがばらつきが大きいため、実用になる設定を探す。外部 LLM は較正用のベンチ１ランだけ使い、残りは会話記録のオフライン再現で調べた。スクリプトはセッションのスクラッチパッド `ctxedit/`（`extract.py`・`sim.py`〔selftest あり〕・`sweep.py`・`calib.py`）。

**サーバー側の消去規則を実測で確定**：`chess-bugfix` の on １ラン（trigger 30000・clear_at_least 10000・keep 3、品質36/36、同一経路の純削減4.9%）で、要求ごとの `applied_edits` を見た。req15 で初めて発火し、13件・10,329トークンを消去した。以後 req35 まで文脈は61,071まで増え、消去可能な古い結果も増えたが、消去量は約10,100〜10,600のまま増えず、件数は13→12に減った。この挙動に合う規則は「毎要求、全履歴から再計算し、keep より古い結果を**古い順に clear_at_least に届くまで**消す（届かなければ消さない）」だけである。「keep より古いものを全部消す」規則や、消去状態が累積する規則では件数の減少を説明できない。**したがって、１要求あたりの節約はほぼ clear_at_least で頭打ちになる。** また、境界の１件が入れ替わると、そこからキャッシュを書き直す（req20 で約19,600の書込）。根拠は１ラン。

**実作業での上限（経路固定・手戻りなし）**：直近30日のメインセッション569件（9,748要求）に、上記の規則を当てはめて再現した。ツール結果の大きさは、単独の大きな結果の直後の文脈増分から、中央値1.9文字／トークンで較正した。

| 事実 | 値 |
|---|---|
| セッション初回要求の文脈（固定の接頭辞：システム・ツール定義・指示・スキル一覧など） | 中央値 81,574 |
| セッション内の文脈の増加 | 中央値 19,109（p90 75,568） |
| 最終文脈に占めるツール結果 | 中央値 3.8%（p90 15.3%） |

| trigger・clear_at_least（keep 3） | 全使用量に対する総トークン削減 | 発火したセッション | 発火セッションの削減率 中央値（p10〜p90） | 料金加重で損になるセッション |
|---|---:|---:|---|---:|
| 100000・10000 | 3.0% | 28% | 4.6%（1.5〜7.6%） | 73% |
| 100000・20000 | 3.2% | 16% | 6.7%（2.0〜11.4%） | 64% |
| 100000・40000 | 2.8% | 6% | 10.6%（4.1〜16.4%） | 42% |
| 60000・40000 | 2.8% | 6% | 10.6%（4.1〜16.4%） | 42% |

料金加重は、キャッシュ読取0.1・1時間キャッシュ書込2.0・出力5.0（入力1.0に対する比）で計算した。Claude Code の記録は `ephemeral_1h` 書込である。主 KPI（キャッシュ込み総トークン）では削減になるセッションでも、料金ではキャッシュ書き直しの費用が読取の節約を上回ることが多い。サブスクリプションの利用枠がどちらに近い重みで数えるかは未確認。

**結論（暫定）**：
1. 実作業の文脈の大半は、ツール結果ではなく約８万の固定接頭辞である。ベンチ（接頭辞約1.7万で、ツール結果が文脈の大半）の8〜12%は実作業に移らない。実作業の上限は全体の約３%である（手戻りを引く前）。
2. ばらつきの主因は二つある。(a) セッションごとの消去可能量の差。これは設定では消せない。(b) 手戻り。較正ランでは節約216,044に対し、手戻りとして数えられたものが123,476あった。ただし、この中には消去と無関係な `npm test` の再実行も含まれ、過大評価である。
3. 設定で制御できるのは clear_at_least である。大きくするほど、発火は稀になり、発火したときの効果は大きく安定する（C=40000 で p10 4.1%）。trigger は60000〜100000の間ではほとんど効かない。
4. 手戻りと実際の経路への影響は、このオフライン再現には含まれない。実作業に近い接頭辞で確かめるには、`--user-tools` 付きのライブ計測が必要である（未実施）。

## 手戻り指標の修正と、実作業に近い条件のパイロット（2026-09-25）

**手戻りの定義を修正**（[clearnet.go](../internal/bench/clearnet.go)）：旧定義は「最初の消去後、keep より古い呼出と同じツール・同じ入力の再実行」を手戻りとしていた。これでは編集後の `npm test` 再実行など、消去がなくても起きる呼出まで数えてしまう。新定義では、手戻りとなるのは次の三つをすべて満たす場合に限る。(1) 同じツール・同じ入力である。(2) 元の呼出が、その要求で**実際に消去されていた**（要求ごとの `clearedToolUses` と、古い順という消去規則から判定。`exclude_tools` は番号付けから除く）。(3) 結果の本文が元と**完全一致**する。結果が欠けて判定できないときは欠測（nil）とし、０で埋めない。較正ラン（`chess-bugfix`、trigger 30000・clear_at_least 10000）を再計算すると、手戻りは 123,476→0、同一経路の純削減は 67,983（4.93%）→191,459（12.75%）となった。唯一の候補は `Read src/chess.js` の再読だったが、途中で編集されて結果が一致しないため除外された。**限界**：消去が原因で、編集済みのファイルを読み直した場合は数えない。したがって新定義は手戻りの下限にあたる。

**ベンチに `--no-hooks` を追加**：`--user-tools` と併用すると、利用者の MCP・プラグイン・スキルの定義は実環境と同じまま、フックだけを `--settings {"disableAllHooks":true}` で止められる。比較キーにも含めた。`go test ./...` は全パッケージ成功、未コミット。

**パイロット**（`chess-engine`、`--user-tools --no-hooks`、trigger 100000・clear_at_least 20000・keep 3、`claude-sonnet-5`／`medium`、on １ラン）：接頭辞は 64,603（フック分がないため、実作業の中央値 81,574 より小さい）。17要求で終了し、文脈は最大 107,047 に達したが、**消去は一度も起きなかった**。ツール結果の合計は約6,000トークンにすぎず、文脈の増加はほとんどが接頭辞と、モデルの出力（`Write` の入力。req3 で約23,000）だった。外部品質は21/36（58%）で、エージェント自身のテスト27件は通過していた。消去が起きていないため、この品質は消去とは無関係である。

**`clear_tool_inputs` の試算**（実セッション569件、同じ規則）：ツール入力も消去対象にしても、全体の削減は +0.5〜0.9 ポイントにしかならない（C=40000 で 2.8%→3.7%）。

**判断**：オフライン再現とパイロットの結論は一致した。実作業に近い条件では、消去できるツール結果がほとんどない。このまま６ペアを計測しても、消去の起きない A/A 比較になるだけなので、事前登録と本計測は**実施していない**。次の方向は利用者の判断を待つ。

## Claude Code：固定接頭辞の縮小（2026-09-25）

利用者の選択（２）により、実作業の文脈の大半を占める固定接頭辞を分解した。ローカルの中継（`scratchpad/prefix/relay.py`。本文のみ保存し、認証ヘッダーは保存しない）で、`claude -p` の要求を１件採取した（フック無効、`claude-sonnet-5`）。本文は149,319文字。内訳は、`messages` 内の system ロールのブロックが71,799文字、`system` が28,162文字、`tools` 12件が42,902文字（MCP ツールは ToolSearch で遅延読込み）だった。system ロールのブロックのうち、**エージェント一覧（80種）が約33,600文字、スキル一覧（118件）が28,702文字**を占める。

直近30日の実利用では、呼ばれたエージェント種別は10種だけだった（general-purpose 625、Explore 53、fork 36、file-ops-delegate 31、doctrine-executor-light 16 など）。`doctrine-module-*`（29種）と `doctrine-spec-*`（４種）は一度も使われていない。これらは説明文そのものが「モジュール」「仕様書」と書かれた文書で、`doctrine-orchestrator` の退避後も一覧に残っている。プラグインは、figma・claude-security・diagram-design の呼出が０回、slack の MCP が７回だった。

公式の設定だけで削れるかを、同じ `claude -p "Reply with OK only."` の入力＋キャッシュ読取＋書込で測った（中継なし、各２回で同値）。

| 設定（`--settings`） | 固定接頭辞 | 差 |
|---|---:|---:|
| フック無効のみ（基準） | 67,819 | — |
| ＋`permissions.deny` に `Agent(doctrine-module-*/spec-*)` 33件 | 59,107 | −8,712（−12.8%） |
| ＋`enabledPlugins` で slack・figma・claude-security・diagram-design を無効 | 62,130 | −5,689 |
| 両方 | 53,418 | **−14,401（−21.2%）** |

`permissions.deny` で拒否したエージェントは、要求本文の一覧から実際に消えた（中継で確認）。未使用の doctrine 系55種すべてを拒否すると −16,465（中継経由の基準62,749から）だった。ただし `workflows/template-doctrine-{orders,post}.js` は `doctrine-nco-collector`・`doctrine-aggregator`・`doctrine-reflector`・`doctrine-staff-*` を参照しているため、この範囲は Workflow の動作確認なしには推奨しない。なお、中継（非 first-party の `ANTHROPIC_BASE_URL`）を通すと基準が約5,000小さくなる。これは既知の ToolSearch 既定の差と整合する。

**効果の見積もり**：直近30日のメインセッション要求は9,748件、総トークンは1,177M。両方の設定で１要求あたり14,401を削ると、約140M（約12%）の削減になる。context editing の上限（約3%）の約４倍にあたり、手戻りやキャッシュ破壊も伴わない。ただし、セッション内で接頭辞が同じ大きさのまま続くと仮定した推定である。削るのはキャッシュ読取が中心なので、料金換算の削減率はこれより小さい。品質への影響は、削る対象が未使用のものに限られるため小さいと見込むが、実測はしていない。

**実装先**：jev-routing のプロキシでは実装しない。一覧の書き換えは Claude Code の内部形式に依存して壊れやすく、公式の設定で同じ効果が得られるためである。変更先は利用者の Claude Code 設定（ソースは `private_dotfiles`）であり、適用は利用者の判断を待つ。スキル一覧の縮小（未使用の利用者スキルへの `disable-model-invocation: true`）は SKILL.md の編集が必要で、未測定。

**利用者の判断（2026-09-25）**：使う予定のあるスキル・エージェントを無効にして減らすのは本末転倒であり、目標は jev-routing を使った削減である。上記の設定変更案は採らない。

## サブエージェントの消費と context editing の再試算（2026-09-25）

直近30日の記録（要求 message.id の重複を除く）では、**メインセッションが1,223M（10,225要求）、サブエージェントが1,173M（12,164要求）とほぼ同量**だった。どちらも94%がキャッシュ読取、キャッシュ書込が6%、出力は0.1〜0.4%。したがって総トークンはほぼ「要求数×文脈の大きさ」で決まり、出力や推論量を調整する介入は効かない。サブエージェントの消費の87%は general-purpose による。

サブエージェントは初回要求の文脈が中央値35,197と小さい一方、要求ごとの文脈は中央値79,944まで伸びる。つまりメインと違い、文脈の大半がツール結果などの履歴である。同じ消去規則（古い順に clear_at_least まで）でサブエージェント583件（11,596要求、1,147M）を再現した（経路固定、手戻りなし、1.9文字／トークン）。

| trigger・clear_at_least（keep 3） | サブの総トークン削減 | 発火 | 発火時の削減率 中央値（p10〜p90） | 料金加重の全体 | 料金で損になる割合 |
|---|---:|---:|---|---:|---:|
| 100000・20000 | 8.0% | 35% | 10.6%（3.8〜16.5%） | −2.5% | 79% |
| 60000・40000 | 10.1% | 22% | 15.8%（6.8〜22.9%） | +0.6% | 62% |
| 60000・60000 | 10.0% | 15% | 18.5%（8.5〜29.6%） | +1.8% | 56% |

メイン（上限約３%）と合わせると、設定 60000・40000 では全体で約6.4%と見積もられる（(2.8%×1,223M＋10.1%×1,147M)／合計）。サブエージェントでは、発火時の効果が大きく、ばらつきの下限（p10）も高い。ただし料金加重では多くのセッションで損になる点は変わらない。手戻りは未計測である。サブエージェントを実際に使うライブ計測（`child-facts` 型の課題）で確かめる必要がある。

## サブエージェント読取課題 `child-survey` とパイロット（2026-09-25）

`child-survey` を追加した。親が１回だけサブエージェントに委譲し、子はこのリポジトリの Go ソース８本（約250KB）を Read で全文読み、各ファイルのトップレベル func 数と最後の func 名を返す。親はそれを answer.json に書く。外部採点は go/parser で計算した16項目。証拠として、子が８ファイルすべてを Read したことを要求する。

計測の不備を３点直した。(1) Claude の親子は同じセッションキーを使うため、既存の照合が親だけの一致で「成功」し、子の帰属処理に進まなかった。今は、子の未確認が残る場合に `claudeChildAttribution`（stream-json の各メッセージの使用量と、プロキシの要求を一意に照合）へ進む。(2) 同一経路の純削減を、親と子の要求列ごとに計算するようにした。(3) ベンチの子プロセスに、親の Claude Code セッションの環境変数（`CLAUDE_CODE_SESSION_ID`・メッセージング用ソケットなど）が漏れていたので、除去した。`CLAUDE_CODE_SUBAGENT_MODEL` は利用者の実設定（`opus`）なので残し、記録と比較キーに含めた。

パイロット１ペア（親 `claude-sonnet-5`／`medium`、子 `claude-opus-5-5`、trigger 60000・clear_at_least 40000・keep 3）では、両条件とも品質16/16だった。on では子で消去が起き（21件、要求ごとの合計381,387）、総トークンは 1,102,603→693,549（対照−介入 409,054）、同一経路の純削減は +317,796（31.4%）、手戻りは０と計算された。帰属は、子の要求がすべて opus、親の要求がすべて sonnet であることと一致した。１ペアなので効果判定には使わない。

**事前登録（結果を見る前に記録）**：同じ設定（子は利用者実設定の opus、フックなし・利用者ツールなしの隔離設定）で `child-survey` を off,on ６ペア計測する。判定は既定の `--min-pairs 6`・`--min-savings-pct 0` による１回だけとし、途中での打ち切りや延長はしない。併せて、消去が起きた on ランの同一経路純削減がすべて正で、中央値5%以上かを確認する。

**事前登録の訂正（計測開始前、2026-09-25）**：利用者の指示により、子も含めてベンチ基準の `claude-sonnet-5`／`medium` に固定する（`CLAUDE_CODE_SUBAGENT_MODEL=claude-sonnet-5`）。子が opus だった上記パイロットと、１ラン目の開始直後に止めた opus 系列（`survey-reps6/`）は効果判定に使わない。新しい系列は `survey-reps6-sonnet/` に保存する。判定の条件は上記と同じ。

## `child-survey` ６ペアの結果（2026-09-25）

記録は `scratchpad/ctxedit/survey-reps6-sonnet/`。親子とも `claude-sonnet-5`／`medium`、trigger 60000・clear_at_least 40000・keep 3。全12ランで親子の帰属は確認済み（`parentChildVerified`）、計測エラーは０。

| rep | 対照（品質・総トークン・要求） | 介入（品質・総トークン・要求） | 対照−介入 | 介入の消去（件・要求ごとの合計） | 同一経路純削減 | 手戻り |
|---:|---|---|---:|---|---:|---:|
| 1 | 15/16・794,820・11 | 16/16・1,184,571・18 | −389,751 | 179・2,641,056 | 59.6% | 699,727 |
| 2 | 16/16・516,915・7 | 16/16・601,452・12 | −84,537 | 25・464,292 | 30.0% | 136,846 |
| 3 | 15/16・1,075,455・12 | 16/16・506,161・11 | +569,294 | 14・280,336 | 32.3% | 0 |
| 4 | 16/16・497,731・6 | 15/16・455,459・9 | +42,272 | 14・280,984 | 34.8% | 0 |
| 5 | 15/16・684,728・9 | 15/16・870,302・15 | −185,574 | 33・579,046 | 37.1% | 0 |
| 6 | 15/16・795,569・11 | 15/16・479,513・10 | +316,056 | 14・280,687 | 33.5% | 0 |

**事前登録の判定**：`quality_worse`（rep4 で介入側だけが15/16）。比較可能ペアは1/6（品質が両側で満点のペアだけが比較対象になるため）。**効果は認められない**。

所見：
- 課題そのものの品質が不安定だった。対照でも６ラン中４ランが15/16で、子が func 数を１つ数え違える。採点項目の合計は、対照92/96、介入93/96で、介入で悪化したとは言えない。しかし判定規則上は比較可能なペアがほとんど残らない。
- ラン間の総トークン差は −389,751〜+569,294 と大きく散り、中央値は約 −4% だった。介入は要求数が増える傾向がある（中央値 11.5 対 10）。
- 同一経路の純削減は全６ランで正（30〜60%）だったが、これは経路が変わらないという仮定の値である。rep1・rep2 では、消去された内容と同じものを再取得した手戻りが 699,727 と 136,846 あった。rep1 では消去が179件に及び、子が消去されたファイルを繰り返し読み直した。**この課題は、読んだ内容を最後にまとめて報告する形であり、消去が再読を誘発しやすい**。実作業のサブエージェントも調査結果を最後にまとめることが多いため、同じ危険がある。
- したがって、オフライン試算（手戻りなしで約10%）は、手戻りを含む実走では再現しなかった。

## Jev による消去ゲートの実装と事前登録（2026-09-25）

`JEV_CLAUDE_CLEAR_GATE=jev`（ベンチでは `--claude-clear-gate jev`）を実装した（[clear_gate.go](../internal/proxy/clear_gate.go)）。会話の識別は、モデルと最初の user メッセージ本文から作るので、親とサブエージェントは別の会話になる。前回応答の文脈が trigger − clear_at_least/2 に達した時点で、Jev に Choice を１回だけ問う。選択肢は `clear_old_results`（古い出力は使い終えた、逐次作業）か `keep_all_results`（最後にまとめて報告するため必要）。入力はタスク文（1,500字まで）とツール呼出の履歴（名前と短い引数）で、結果の本文は含めない。決定までと `keep` では edit を付けず、`clear` 以降は付け続ける（途中で戻すとキャッシュが崩れるため）。Jev の失敗時は消去しない。判定の Jev 使用量は、イベントと総トークンに計上する。`go test ./...` は成功。

パイロット（各 on １ラン、trigger 60000・clear_at_least 40000）：`child-survey` の子は `keep`（Jev 入力1,049・出力38）、`chess-bugfix` は `clear`（1,705／38）と判定した。後者は閾値に届かず、実際の消去は起きなかった。`child-survey` は `lines` の定義が曖昧で（Read は末尾に空行番号を表示する）、全ファイルで１ずれて8/16になった。このため `last_line`（最後のトップレベル func の `func` キーワードの行番号）に変更し、selftest 16/16 を確認した。

**事前登録（結果を見る前に記録）**：親子とも `claude-sonnet-5`／`medium`、trigger 30000・clear_at_least 10000・keep 3、`--claude-clear-gate jev`。`child-survey` と `chess-bugfix` を各 off,on ６ペア計測し、`survey-gate6/`・`bugfix-gate6/` に保存する。途中での打ち切りや延長はしない。合格条件：
1. `child-survey`：効果判定が `quality_worse` でない。ラン間の総トークン判定が「増加」でない（ゲートが `keep` を選び、無害であること）。
2. `chess-bugfix`：効果判定が `quality_worse` でない。on ランでゲートが `clear` を選び、消去が起きたランの同一経路純削減がすべて正で、中央値5%以上。
3. 両系列で、ゲートの判定（clear／keep／error）と Jev 使用量を併記する。

## Jev 消去ゲート：事前登録した２系列の結果（2026-09-25）

記録は `scratchpad/ctxedit/survey-gate6/`・`bugfix-gate6/`。全24ランで品質は満点（`child-survey` 16/16、`chess-bugfix` 36/36）、全ペアが比較可能、計測エラーは０。

| 系列 | Jev の判定 | Jev 使用量／ラン | 消去が起きた on ラン | 同一経路純削減（消去ありラン） | 手戻り | ラン間の効果判定 |
|---|---|---|---:|---|---|---|
| `child-survey` | 6/6 `keep` | 入力974〜1,429・出力38 | 0/6 | —（消去なしのため0） | 0 | 保留（中央値 +30.6%、区間 −112.2%〜+37.2%） |
| `chess-bugfix` | 6/6 `clear` | 入力823〜940・出力38 | 5/6 | 2.9〜14.2%、中央値 **11.1%**、全て正 | rep5 のみ 34,344 | 保留（中央値 +32.3%、区間 −63.1%〜+76.7%） |

**事前登録の判定：両系列とも合格。**
1. `child-survey`：`quality_worse` ではなく、ラン間判定は「増加」ではない。ゲートは全ランで `keep` を選び、前回ゲートなしで起きた消去と再読（手戻り最大699,727）は起きなかった。on は消去しないため、ラン間の差（−601,026〜+399,793）は実質的に同一条件同士（A/A）のばらつきである。
2. `chess-bugfix`：`quality_worse` ではなく、ゲートは全ランで `clear` を選んだ。消去が起きた５ランの同一経路純削減はすべて正で、中央値11.1%（≥5%）。rep2 は短い経路で閾値に届かず、消去は起きなかった。

所見：Jev の判定費用は、１会話１回・約1,000トークンで、総トークンの0.1〜0.2%にとどまる。ゲートは、消去が害になる「最後にまとめて報告する」型では消去を止め、逐次作業では消去を通した。ラン間比較は両系列とも A/A 並みにばらつき、効果の根拠にはならない。根拠は同一経路の指標と、害の回避（品質・手戻り）である。

**限界**：対象は２課題・１モデルで、Jev の判定の一般化（実作業の多様な課題での正答率）は未検証。閾値は trigger 30000・clear_at_least 10000 でベンチ向けの値であり、実作業（メインの接頭辞約８万）向けの既定値は別に決める必要がある。同一経路の指標は経路が変わらないという仮定に立ち、手戻りは完全一致の再取得だけを数える（下限）。

## 実作業向け閾値の試算（2026-09-25）

メイン569件とサブエージェント583件（直近30日）を合わせて、同じ消去規則で試算した（経路固定、手戻りなし、ゲートは常に `clear` と仮定した上限）。

| trigger・clear_at_least（keep 3） | 全体の総トークン削減 | メイン | サブ | 料金加重の全体 | 発火（メイン／サブ） | 発火セッションの削減 p10・中央値 | 料金で損になる割合 |
|---|---:|---:|---:|---:|---|---|---:|
| 100000・10000 | 4.0% | 3.0% | 5.0% | −3.1% | 28%／39% | 1.9%・5.3% | 83% |
| 100000・20000（現行の既定値） | 5.6% | 3.2% | 8.0% | −1.3% | 15%／35% | 3.0%・9.2% | 74% |
| 100000・40000 | 6.3% | 2.8% | 9.9% | +0.5% | 6%／22% | 6.1%・14.7% | 59% |
| 100000・60000 | 5.9% | 1.8% | 10.0% | +1.1% | 2%／15% | 7.5%・17.6% | 52% |
| 60000・40000 | 6.4% | 2.8% | 10.1% | +0.5% | 6%／22% | 6.2%・15.0% | 57% |

所見：
- trigger は 40000〜100000 の間ではほとんど効かない。発火を決めるのは「keep より古い結果が clear_at_least 以上たまったか」であり、実作業では文脈が trigger を先に超えている。
- clear_at_least 40000 が総トークン削減の最大付近にあり、料金加重でも損にならない（+0.5%）。発火したセッションの下限（p10 6.1%）も高く、ばらつきが小さい。60000 にすると、発火はさらに稀になり、料金面は良くなるが、総トークン削減は下がる。
- 推奨：**trigger 100000（API 既定）・clear_at_least 40000・keep 3・ゲート `jev`**。上限見積もりは全体で約6.3%。実際には、ゲートが `keep` を選ぶ会話と、手戻りの分だけ下がる。

**ゲートの問合せ時期の問題**：現行のゲートは、前回の文脈が trigger − clear_at_least/2 に達した時点で Jev に問う。実作業のメインは接頭辞だけで約８万あるため、上の推奨値では、ほぼ全会話で最初の応答直後に、履歴のない状態で判定することになる。Jev の費用自体は小さい（１会話約1,000〜1,500、全体の約0.07%と試算）。しかし、判定材料がタスク文しかない。消去対象（keep より古いツール結果）が clear_at_least に達した時点で問う方が、判定材料が増え、問合せも実際に消去がありうる会話（メイン約6%、サブ約22%）だけに絞れる。プロキシは要求本文からツール結果の大きさを数えられるので、実装できる。

## ゲートの問合せ時期の変更と既定値の更新（2026-09-25）

ゲートは、(1) 会話の使用量を１回以上観測済み、(2) 前回の文脈 ≥ trigger、(3) 送信する要求本文で消去可能と見積もったツール結果（keep より古く、除外対象でない tool_result の文字数を1.9文字／トークンで換算）≥ clear_at_least、の３条件がそろった時点で、会話ごとに１回だけ Jev に問うようにした。1.9 は実作業の中央値で、トークン数を多めに見積もるため、問合せはやや早めになる。既定の clear_at_least は 20000→40000 に変更した（trigger 100000・keep 3 は据え置き）。README も更新した。`go test ./...` は成功。

回帰確認（各 on １ラン、`claude-sonnet-5`／`medium`）：
- `child-survey`（既定値 100000・40000）：子の文脈が110,690 に達した次の要求（seq10、130,364）で問い、`keep`（Jev 入力1,253）を選んだ。消去なし、品質16/16。
- `chess-bugfix`（30000・10000）：文脈31,723 の次の要求（seq11）で問い、`clear`（Jev 入力1,175）を選んだ。サーバーの消去は seq17 から始まり（約10,000〜11,000／要求）、品質36/36、同一経路純削減9.4%。見積もりは実際の発火より約６要求早く、意図どおり「早め」だった。

ベンチの文脈は既定の trigger 100000 に届かないため、`clear` 側は既定値で確認できていない。既定値での clear 側の検証には、実作業規模の長いセッションが必要である。

## 最終設定と期待効果のまとめ（2026-09-25）

**最終的な推奨設定**（context editing 自体は既定で無効のまま）：`JEV_CLAUDE_CLEAR_TOOL_USES=on`、`JEV_CLAUDE_CLEAR_GATE=jev`、trigger 100000（既定）、clear_at_least 40000（新しい既定）、keep 3（既定）。ゲートには Jev の鍵が必要で、鍵がなければ消去しない。

**期待効果**（直近30日の実セッションの再現。ゲートが常に clear を選び、手戻りがないと仮定した上限）：

| 対象 | 全体の総トークン削減 | 消去が起きたセッション | その中の最低／中央値／最高 |
|---|---:|---:|---|
| メイン（569件） | 2.8% | 36件（6%） | 2.0%／10.6%／20.0% |
| サブエージェント（583件） | 9.9% | 129件（22%） | 1.4%／15.8%／30.2% |
| 合計 | 約6.3% | — | 全セッションの中央値は0%（大半は消去が起きない） |

実際の削減は、ゲートが `keep` を選ぶ会話と手戻りの分だけ、この上限より小さくなる。実走の参考値（ベンチ閾値 30000・10000）は、`chess-bugfix`（clear）が同一経路純削減2.9%／11.1%／14.2%（最低／中央値／最高）、`child-survey`（keep）が0%で、いずれも品質は満点だった。既定値（trigger 100000）で clear 側が実走で効くことは、まだ確認していない。

**未完了**：実作業規模の長いセッションでの既定値の実走確認。実作業の多様な課題での、ゲート判定の妥当性の点検。README の Claude Code 節にこの経緯と設定を反映した。

## 引き継ぎ（2026-09-25 セッション終了時）

### 現在地

- すべての変更は `origin/main` にプッシュ済み（最新 `d21a40f`）。この引き継ぎ節だけは、追記した時点で未コミット。
- Claude Code：ツール選択による置き換えは断念し（`JEV_CLAUDE_ADVISE` は既定で無効）、削減の本線は Anthropic のネイティブ context editing ＋ Jev の消去ゲートとした。推奨設定は `JEV_CLAUDE_CLEAR_TOOL_USES=on`・`JEV_CLAUDE_CLEAR_GATE=jev`・trigger 100000・clear_at_least 40000・keep 3（context editing 自体は既定で無効）。経緯と数値は本メモの「Claude Code：…」各節と README の `## Claude Code` 節を参照。
- 利用者の方針：Claude Code は、しばらく実際に使って記録を集める。次の改善対象は Codex。
- 利用者の判断として確定済みのこと：使う予定のあるスキル・エージェント・プラグインを無効にして固定接頭辞を削る案は採らない（jev-routing による削減ではないため）。ベンチの基準は親子とも `claude-sonnet-5`／`medium`（Codex は `gpt-5.6-terra`／`medium`）。

### Claude Code：実利用での記録手順（利用者が実施）

```bash
cd ~/repos/jev-routing && go build -o ~/.local/bin/jev-routing ./cmd/jev-routing && mkdir -p ~/jev-dogfood
# プロキシ常駐（TYPESAFE_API_KEY か JEV_API_KEY が必要。JEV_COMPACTION=off は context editing だけを測るため）
JEV_CLAUDE_CLEAR_TOOL_USES=on JEV_CLAUDE_CLEAR_GATE=jev JEV_COMPACTION=off \
  nohup jev-routing serve --host claude --listen 127.0.0.1:8787 >> ~/jev-dogfood/serve.log 2>&1 &
# イベントの永続化（プロキシはメモリに直近1000件しか持たない。使用量は後から追記されるので100件ずつ重ねて取り、集計時に (instanceId, seq) の最後の行を採る）
nohup bash -c '
U=http://127.0.0.1:8787/dashboard/events; F=~/jev-dogfood/events.jsonl; since=0; cur=
while sleep 30; do
  r=$(curl -s "$U?since=$since") || continue
  iid=$(jq -r .router.instanceId <<<"$r") || continue
  if [ "$iid" != "$cur" ]; then cur=$iid; since=0; r=$(curl -s "$U?since=0") || continue; fi
  jq -c --arg i "$iid" ".events[]|.+{instanceId:\$i}" <<<"$r" >> "$F"
  max=$(jq "[.events[].seq]|max // 0" <<<"$r")
  [ "$max" -gt 0 ] && since=$(( max>100 ? max-100 : 0 ))
done' > /dev/null 2>&1 &
unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN
ANTHROPIC_BASE_URL=http://127.0.0.1:8787 claude
```

`jev-routing run claude` でも同じ環境変数で使える（引数は `--` の後ろ）。ただし、イベントはプロセスの終了とともに消えるため、集計には `serve` を使う。非 first-party の `ANTHROPIC_BASE_URL` では ToolSearch の既定が変わり、固定接頭辞が約5,000小さくなる。そのため、プロキシなしの過去セッションとは直接比較しない。

**１〜２週間後の作業（未着手）**：`~/jev-dogfood/events.jsonl` と `~/.claude/projects/**` を突き合わせる集計スクリプトを作る。要求ごとの使用量と `clearedInputTokens` から同一経路純削減（`internal/bench/clearnet.go` と同じ式）、手戻り（消去済みの呼出の同一内容の再取得）、ゲート判定（`clearGate`：clear／keep／error、`clearGateReason`）の内訳、Jev の使用量を出す。確認したいのは、既定値（trigger 100000）で clear 側が実際に効くか。ネイティブ圧縮（`JEV_COMPACTION` 既定 on：Claude Code の `/compact`・自動圧縮を Jev の drop/truncate 結果で置き換える）を評価する場合は、別の期間に分けて比べる。

### Codex：次にやること（未着手）

1. **増加の原因分解**：`dual-facts` を２〜３ペア取り直し（`jev-routing bench --agent codex --model gpt-5.6-terra --effort medium --tasks dual-facts --catalog 2 --modes off,on --reps 3`、`JEV_SELECTION_MODE=jev`、推論設定は bench が `preserve` を固定）、要求ごとに (a) Jev 判定費用、(b) 上流のキャッシュ読取・書込の変化（候補の絞り込みでツール一覧が要求ごとに変わり、プロンプトキャッシュを崩している疑い。未検証）、(c) 要求数・`codex-auto-review` の補助要求の変化に分ける。Claude では増加の99%が Jev 費用だった。前回の６ペア（削減率中央値 −9.55%、区間がゼロをまたいで保留）の生ログは前のマシンにしかない。
2. **実セッションの削減余地（オフライン、費用なし）**：`~/.codex/sessions` の記録から、固定接頭辞・ツール結果・キャッシュ・サブエージェントの内訳を集計し、ツール選択・履歴縮小・圧縮のどれに余地があるかを試算する（Claude で使った手法は下記スクリプト参照）。
3. 1・2 に基づいて方針を選ぶ（Jev 費用の削減、会話ごとに一度だけ絞って固定するキャッシュ安定な選択、ネイティブ圧縮置換の評価、または何もしない）。
4. 実装し、事前登録した６ペアで判定する。`xcell-locate` は候補が少なく、費用の足切りで Jev が呼ばれなかった。Codex のサブエージェント試行は子の起動失敗で0/3だった。これらも課題選びの注意点とする。

### 作業上の注意

- ベンチの計測値の生データ（`survey-reps6-sonnet/`・`survey-gate6/`・`bugfix-gate6/`・`gate2-*/` など）と、分析スクリプト（`extract.py`・`sim.py`〔観測した消去規則 `stateless_min`、selftest あり〕・`sweep.py`・`calib.py`・`prefix/relay.py`）は、前セッションのスクラッチパッド（`/private/tmp/claude-502/…/scratchpad/ctxedit/`）にあり、セッション終了とともに消える可能性が高い。数値は本メモに転記済み。必要ならスクリプトは作り直す。
- Claude Code の中からベンチを起動すると、以前は親セッションの `CLAUDE_CODE_*` 環境変数が子に漏れていた（修正済み）。`CLAUDE_CODE_SUBAGENT_MODEL` は意図的に引き継ぎ、記録と比較キーに含める。比較可能なランを得るには、明示的に固定する（例：`CLAUDE_CODE_SUBAGENT_MODEL=claude-sonnet-5`）。
- ベンチのラン間比較（対照と介入を別々に走らせた総トークン差）は、Claude では同一条件同士（A/A）でも −271%〜+90% 散る。効果の根拠には、同一経路の指標と品質・手戻りを使い、結果を見る前に判定条件を本メモへ事前登録する。
- 本メモ冒頭の「当面の作業リスト」（V1〜E1）のチェックは古く、多くは後の節で実装済みである。更新は未実施。

## Codex `dual-facts` 増加原因の再計測（2026-09-25）

引き継ぎの第１項を実施した。`gpt-5.6-terra`／`medium`、`--catalog 2 --modes off,on --reps 3`、`JEV_SELECTION_MODE=jev`、ベンチ固定の `JEV_COMPACTION=off`・`JEV_REASONING=preserve`。現行の `JEV_COST_GATE_MAX=3` では候補３件以下のこの課題で Jev 呼出が全介入ラン０件となり、３ペアとも `jev_not_applied` で比較不能だった。そのため、過去の Jev 使用時の増加原因を調べる系列は `JEV_COST_GATE_MAX=0` を明示して別ディレクトリで取り直した。生データは `/tmp/jev-codex-recheck-okQAaF/results/`（既定ゲート）と `/tmp/jev-codex-recheck-okQAaF/ungated/`（ゲート無効）に保存した。いずれも全ランで品質3/3。

ゲート無効の６ランは全て使用量・モデル・品質の照合を通り、３ペアとも `comparison.json` で比較可能。要求別 `proxy-events.json` を `runs.jsonl` と突き合わせ、入力・キャッシュ読取・出力・モデル別要求数の合計一致を確認した。差は介入−対照。総トークンは上流入力（キャッシュ読取を含む）＋上流出力＋Jev 入出力で、キャッシュ読取を別に加算しない。

| ペア | 総トークン差 | Jev 入出力 | 上流入出力差 | 上流要求数差 | 非キャッシュ入力差 | `codex-auto-review` 要求数 |
|---|---:|---:|---:|---:|---:|---|
| 1 | +81,082 | +13,122 | +67,960 | +2 | +33,058 | 2→2 |
| 2 | −118,050 | +13,141 | −131,191 | −4 | +37,200 | 2→2 |
| 3 | −17,826 | +13,097 | −30,923 | −1 | +41,580 | 2→2 |

- Jev は各介入ランで２回呼ばれ、うち選定が実際に適用されたのは１回。`codex-auto-review` は両条件で毎回２要求、入出力合計の差は +102／+94／+17 であり、この３ペアの主な差ではない。
- 最初の主モデル要求では、対照のキャッシュ読取が３回とも24,320、介入は３回とも０。介入では候補が３→２に書き換わり、最初の要求の非キャッシュ入力が大きく増えた。候補一覧の変化による接頭辞キャッシュの破壊と整合するが、上流のキャッシュキーを直接検証したものではなく因果は未確定。Codex のキャッシュ書込量は使用量に報告されず、実測として分解できない。
- 主モデルの要求数が介入で +2／−4／−1 と変動し、総差の符号も揺れた。３ペアは原因診断であり、削減効果の採否には必要な６比較可能ペアに達しない。既定ゲートではこの課題の Jev 費用は０で、過去の Jev 使用時の増加をそのまま現行既定値の効果に読み替えない。

## Codex 実セッションの内訳と介入判断（2026-09-25）

`python3 scripts/analyze-codex-sessions.py 2026-08-26 2026-09-24` で、進行中の記録を除く完了済み30日間の `~/.codex/sessions` を集計した。475ファイル中339件に応答別の使用量がある。各ファイルで応答別入力合計と最終のセッション累計が一致することを検証した。対話型の親（`cli`）とサブエージェントを主対象とし、短い自動実行（`exec`）は別集計にした。本文・ファイル名・認証情報は集計出力に含めない。

| 対象 | 使用量あり | 入力総量 | キャッシュ読取／入力 | 初回要求の入力中央値 | 初回の開発者指示の文字数中央値 | ツール結果の文字数総量 |
|---|---:|---:|---:|---:|---:|---:|
| 対話型の親 | 201 | 1,284,623,609 | 98.1%（セッション別中央値94.5%） | 40,732 | 59,128 | 33,795,224 |
| サブエージェント | 33 | 73,567,083 | 95.3%（同92.1%） | 40,647 | 70,690 | 6,555,788 |
| 親のうち `gpt-5.6-terra`／`medium` の単一設定 | 25 | 26,178,547 | 92.7%（同90.5%） | 41,669 | 58,781 | 2,612,820 |

親子の入力・出力合計ではサブエージェントが約5.4%。親の初回開発者指示のうち、別の親セッション５件以上で同一本文が現れた部分は中央値57,796文字だった。これは固定接頭辞が大きいことを示す文字数の観測であり、ツール定義などを含む実送信プロンプトのトークン内訳や削減可能量ではない。親のツール結果7,337件のうち20,000文字超は538件で、結果文字数の47%を占めた。大きな結果の初回切詰めには余地があるが、必要情報の欠落や再取得を測っていない。ツール結果の文字数を繰返し送信分のトークン数には換算していない。セッション記録だけでは固定接頭辞・ツール結果それぞれの正確な送信トークン数、Jev 使用量、キャッシュ書込量を分離できない。

圧縮は独立した `compacted` 記録で確認した。親201件中12件で計14回あり、サブエージェント33件と標準モデル／推論設定の親25件では０件だった。単一要求の入力が記録上の文脈窓の半分以上になった親は６件。入力の半減だけで圧縮を数えると２件しか拾えないため、圧縮回数には `compacted` を使う。

**採用判断：Codex の既定の費用ゲート（`JEV_COST_GATE_MAX=3`）を維持し、今回は新しい介入を実装しない（⭐5）。** `dual-facts` の現行既定値では Jev 費用が０で、強制的な Jev 使用は初回のキャッシュ読取消失と約1.3万トークンの判定費用を伴った。実セッションはキャッシュ読取が大半で、会話単位の選択固定（⭐1）は安全な Codex 会話識別・候補変更時の失効が未整備で、初回の候補書換えも残る。Jev 入力だけを縮める案（⭐2）は、候補が３件以下の既定経路には効果がない。履歴の古いツール結果を後から縮める案（⭐1）は、キャッシュ接頭辞への影響と再取得が未検証。大きなツール結果の初回切詰め（⭐2）は別課題として品質を含む比較が必要。既存のネイティブ圧縮置換の評価（⭐2）は、発火する長い実セッションを得た段階で行う。いずれも実作業全体の削減効果は未検証であり、３ペアの差を因果効果とは扱わない。

## Codex ツール選択・圧縮ベンチの事前登録（2026-09-25）

ユーザーの追加依頼により、外部成果と使用量を実 CLI で反復確認する。**ツール選択**は標準 `code_mode_host=true` の `dual-facts --catalog 4` を単発試験し、Jev 呼出０・`jev_not_applied` を確認した。MCP は外部名前空間で絞り込み対象外、ローカル候補は３件で既定の費用ゲートに止まる。従来型ツール表示もベンチ MCP が公開されず両条件0/3だった。標準条件の選択効果中央値は「算出不可」とし、ラン間の偶然の差を０%または削減と呼ばない。

**Codex の Jev 圧縮置換**は、閾値32,000で再圧縮を連発し要求本文が肥大、45,000の20段階未満の試行では完了済み読取を再実行して品質0/3になった。圧縮指示が Jev の目標を上書きしていた点と、十分短くならない要約の上流フォールバックを修正した。実 CLI では要求数が増える試行があり、合成応答を CLI が使用量へ含めるため実上流との照合も失敗した。総トークンの効果は判定できない。したがって Codex 向け置換を既定で無効にし、明示した実験設定でのみ有効にした。ここまでのパイロットは効果判定に使わない。

**これから行う６ペアの条件（結果を見る前に固定）**：課題は外部採点・10ではなく20ファイルの全文読取証跡を持つ `compact-facts`。親モデル `gpt-5.6-terra`／`medium`、選択と Jev 圧縮置換は両条件で無効。対照は Codex 標準圧縮閾値900000、介入は55000、`--modes off,on --reps 6 --timeout-min 7` で順序を交替する。単発パイロットでは両側3/3・使用量照合成功、対照の圧縮要求０、介入１だったが、総差を効果とは判定しない。

主指標は各ペアの `100 × (対照総トークン−介入総トークン) / 対照総トークン` の中央値。総量は上流入出力＋Jev入出力（この系列の Jev は０）で、キャッシュ読取は入力の内数。６ペアを打切り・追加なしで実施し、全開始ランの品質、圧縮要求数、要求数、比較可能数、各ペアの差、中央値、最小・最大、経過時間を報告する。比較可能条件は両側品質3/3、20ファイルの全文読取、対照の圧縮要求０・介入１以上、モデル／推論設定一致、CLIとプロキシの使用量一致、欠測・汚染なし。採用目安は品質悪化なし・６比較可能ペア・中央値５%以上の総トークン削減。ただし別経路の６ペアであり、因果を断定しない。

### 圧縮閾値の比較条件を再登録（１系列目の完走後）

上記６ペアは予定どおり完走し、全12ランで品質3/3、20ファイルの読取順・重複なし、ホスト使用量の照合に成功した。介入側の明示的な圧縮要求は２ランだけで、事前登録した比較可能ペアは2/6。よって効果採否の条件を満たさない。参考として全６ペアの総トークン差の中央値は13.11%の削減だが、圧縮要求のない４ペアの差も含むため、圧縮発火の効果とは呼ばない。生データは `/tmp/jev-codex-bench-next-MRE2Pb/native-twenty-55k-reps6/`。

要求本文の観測では、明示的な圧縮要求が０のペアでも同じ22要求の例で、対照の本文増分はツール読取ごと約4,826バイト、低閾値側は途中から約1,187バイトだった。最初は閾値がツール結果の持越量にも作用すると推測したが、実行 CLI と同版の [Codex の設定処理](https://github.com/openai/codex/blob/rust-v0.157.0-alpha.11.1/codex-rs/models-manager/src/model_info.rs#L19-L46)と[履歴保存処理](https://github.com/openai/codex/blob/rust-v0.157.0-alpha.11.1/codex-rs/core/src/context_manager/history.rs#L441-L453)では圧縮閾値とツール結果上限は別の設定で、直接の制御経路は確認できなかった。本文短縮の原因は未特定。したがって次系列は「圧縮イベント単体」ではなく、**Codex の文脈上限設定全体**を評価する。発火したランだけを選ぶと実行経路で標本を選別してしまうため、介入側の圧縮要求０も比較に含め、要求数を別掲する。対照・介入のプロキシ経路も両方 `baseline` に統一する。

**新たな６ペアの事前登録**：課題・モデル・推論量・閾値900000／55000・実行順交替・７分の上限・各20ファイルの全文読取は前系列と同じ。Jev 選択と Jev 圧縮置換は両側無効。全ランの品質3/3、読取証跡、上流と CLI の使用量一致、欠測・汚染なし、同じ比較キーを要求する。圧縮要求の有無は記録するが比較可能性の条件にはしない。主指標は６ペアの総トークン削減率中央値で、採用目安は品質悪化なし・６比較可能ペア・中央値５%以上。各ペア、範囲、要求数、キャッシュ内外、経過時間も報告する。旧系列の数値と合算せず、この新系列を独立に６ペア完走する。

### 圧縮閾値の確認系列：６ペアの結果

保存先は `/tmp/jev-codex-bench-next-MRE2Pb/context-policy-reps6/`。全12ランで `gpt-5.6-terra`／`medium`、品質3/3、20ファイルを１→20の順に各１回全文読取、CLI とプロキシの使用量一致、タイムアウト・欠測なし。要求別使用量の和と `runs.jsonl`、総トークンと入出力の和を再計算して一致を確認した。差は対照−介入、率は差／対照総量。圧縮要求は対照が全て０。

| ペア | 対照総量 | 介入総量 | 削減率 | 要求数 対照→介入 | 介入の圧縮要求 |
|---|---:|---:|---:|---|---:|
| 1 | 1,118,315 | 1,249,163 | −11.70% | 22→30 | 3 |
| 2 | 1,141,829 | 1,099,231 | +3.73% | 24→23 | 0 |
| 3 | 1,160,649 | 1,221,472 | −5.24% | 22→28 | 2 |
| 4 | 1,026,570 | 1,118,717 | −8.98% | 22→27 | 3 |
| 5 | 1,252,316 | 1,093,965 | +12.64% | 24→26 | 2 |
| 6 | 1,231,341 | 1,024,846 | +16.77% | 24→24 | 2 |

**事前登録どおりの６ペア中央値は −0.75%（9,112.5トークンの増加）。** 範囲は −11.70%〜+16.77%、改善３ペア・悪化３ペア。`comparison.json` は全６ペアを比較可能とし、効果判定は閾値をまたぐため `hold`。５%以上の採用目安に届かないので、低い閾値55000は実利用設定に採用しない。介入側は６ラン中５ランで明示的な圧縮が起きたが、圧縮０のペアも差があり、経路のばらつきと要求本文に見られた差の原因を分離できない。介入側の非キャッシュ入力中央値は192,847、対照53,046で、時間中央値も150秒対95秒。総量だけでなく費用・待ち時間にも悪化の懸念がある。探索系列の+13.11%は確認系列で再現しなかった。

最終設定は Codex 標準の圧縮閾値を変更せず、品質・使用量問題を起こした Jev 要約置換を既定で無効のままとする。ツール選択の既定費用ゲートも維持する。標準 Codex では候補３件のため Jev 選択が発火せず、ツール選択の改善率中央値は算出不可。別ホストや候補４件以上の実用条件を測る場合は、新しい課題と事前登録が必要。

### パイロットで棄却した条件と修正の経緯（追補）

以下は正式な効果判定へ算入しなかった試行。生ログはローカルの `/tmp/jev-routing-compact-pilot-*/` と `/tmp/jev-codex-bench-next-MRE2Pb/` に保存したが、一時領域なので消える可能性がある。品質は各ランの外部採点３項目。

| 対象・条件 | 観測と棄却理由 | 次の処置 |
|---|---|---|
| ツール選択、標準 Codex・`dual-facts --catalog 4` | 対照・介入とも3/3だが Jev 呼出０。追加 MCP は外部名前空間として絞り込み対象外で、ローカル候補３件のまま。別々のランの総量差は選択効果ではない | 既定の費用ゲートを維持し、中央値は算出不可 |
| 従来型ツール表示 `features.code_mode_host=false` | ベンチ用 MCP が公開されず両側0/3 | 標準構成と異なる経路の採用を断念 |
| Jev 圧縮置換、反復文ログ・閾値50000 | 両側3/3だが介入の圧縮要求０ | 読取結果を高エントロピーの決定的ログに変更して発火を確認 |
| 同ログ・閾値32000 | 対照で圧縮警告９回、介入で Jev 置換７回。介入の要求本文は134,612→806,643バイトに肥大し、使用量・ツール結果の照合に失敗 | 低すぎる閾値を棄却。要約が十分縮まなければ上流へ転送する安全策を追加 |
| 高エントロピー６段階・閾値45000、修正前後の４組 | 介入側の品質は修正順に3/3、3/3、0/3、3/3と揺れ、全４組で CLI と実上流の使用量が一致せず `taskTokens=null`。0/3の回は既読ファイルを再読して回答を作らなかった。品質回復後も要求数が対照９→介入18に増えた回がある | 古い圧縮指示の誤検出防止、前回要約の短縮不足時の転送、圧縮指示を Jev の目標から除外を順に実装。総量効果は比較不能とし、Codex の Jev 置換を既定で無効化 |
| Codex 標準圧縮、６段階・閾値45000／60000 | 45000は介入で圧縮２回・両側3/3、単発の総量は増加。60000は品質3/3だが圧縮０で、ラン間差だけでは効果不明 | 圧縮後の作業が続くよう課題を延長 |
| 標準圧縮、10段階・閾値50000／55000 | 50000は介入で圧縮２回・両側3/3だが、対照の最初の短い `cat` 出力が CLI 記録に残らず全文読取証跡が欠測。55000は圧縮０。いずれも単発 | 端のログにも検証可能な出力を追加し20段階へ延長。読取の重複・順序違反を見逃さないよう採点証跡も修正 |
| 標準圧縮、20段階・閾値55000の単発と最初の６ペア | 単発は両側3/3・介入圧縮１回で見かけの削減が大きかったが、最初の６ペアでは介入の発火が2/6。発火必須という事前条件を満たさない | 先の+13.11%を採用せず、設定全体の比較条件を別系列として事前登録し６ペアを取り直した |

**計測上の教訓**：Codex CLI は Jev が合成した圧縮応答の見かけの入出力も自身の使用量へ加えるが、プロキシは実際に上流へ送っていないため課金対象の使用量には加えない。両者の差を補正しない Jev 置換ランは比較不能とする。一方、標準圧縮の閾値比較は両条件で Jev 選択・置換を止め、同じ `baseline` プロキシ経路を通し、CLI とプロキシの使用量を照合できた。`summary.md` の列ごとの中央値差はペア差の中央値ではない。採用判断には `comparison.json` の比較可能ペアと、要求別記録から再計算したペア差を用いる。

## Codex：大きなツール結果の決定的切り詰め（2026-09-25）

**実装**：`JEV_CODEX_TOOL_OUTPUT_MAX=N`（既定0＝無効）で、Codex の Responses 要求に含まれる全ツール結果（`function_call_output` 等）のうち N バイトを超えるものを、先頭 N/2・末尾 N/2 バイトと省略注記に置き換える。Codex は毎要求で履歴全体を再送するため、最新の結果だけでなく全件を毎回同じ規則で変換し、接頭辞キャッシュを保つ。ルーティング・選択・圧縮の設定とは独立で、Jev は呼ばない。

**根拠**：直近150セッションの送信済みツール結果2,301件のうち、20,000バイト超は305件（13%）だがバイトの54%を占める。`gpt-5.6-terra` の Codex は `truncation_policy` が tokens 10000 で、モデルが exec の `max_output_tokens` を呼出しごとに指定する（exec 2,062件中1,288件、値1000〜30000）。大きな出力はモデルが大きな上限を選んだときに生じる。

**ベンチ課題 `large-facts`**：約32KBの低エントロピーのログ6個の先頭・中央・末尾付近に事実を１行ずつ埋め、全文 `cat` の後に答えさせる。課題文で `max_output_tokens` を12000以上にするよう指示する（指示なしのパイロット２組では、モデルが上限を小さく選んで各結果が約10KBになり、切り詰めが発火しなかった）。したがってこの課題は「大きな出力を要求した場合」の負荷試験であり、実作業全体の削減率ではない。パイロット（run3、効果判定に使わない）は両側6/6、介入で切り詰め42件（要求ごとの再適用を含む）、キャッシュ外入力は１要求あたり約1.35万→約0.9万で接頭辞キャッシュの崩壊は見られなかった。

**これから行う６ペアの条件（結果を見る前に固定）**：`gpt-5.6-terra`／`medium`、`--tasks large-facts --modes off,on --reps 6 --codex-tool-output-max 20000`。両側とも同じ `baseline` プロキシ経路、Jev 選択・Jev 圧縮置換は無効、差は介入側の切り詰めだけ。打切り・追加なし。比較可能条件は両側品質満点、６ファイルの全文読取証跡、CLI とプロキシの使用量一致、欠測・汚染なし、同じ比較キー。切り詰めの発火は比較可能性の条件にせず（経路による標本選別を避ける）、件数を別掲する。主指標は各ペアの `100 × (対照総トークン−介入総トークン) / 対照総トークン` の中央値。採用目安は、介入側の品質悪化なし・６比較可能ペア・中央値５%以上の削減。各ペアの差、範囲、要求数、キャッシュ内外の入力、再取得回数、経過時間も報告する。採用しても既定値は無効のままとし、実利用での有効化は別途判断する。

### 大きなツール結果の切り詰め：６ペアの結果

保存先は `/tmp/jev-codex-trunc-reps6/`。全12ランで `gpt-5.6-terra`／`medium`、品質6/6、６ファイルの全文読取証跡あり、CLI とプロキシの使用量一致、タイムアウト・欠測・汚染なし。実行順は rep ごとに交替した。要求別 `proxy-events.json` から総量を再計算し、`runs.jsonl`・`comparison.json` と全ランで一致した。差は対照−介入、率は差／対照総量。

| ペア | 対照総量 | 介入総量 | 削減率 | 要求数 対照→介入 | 介入の切り詰め件数 | 再取得 対照→介入 | キャッシュ外入力 対照→介入 | 経過秒 対照→介入 |
|---|---:|---:|---:|---|---:|---|---|---|
| 1 | 628,546 | 644,338 | −2.51% | 9→12 | 41 | 0→6 | 104,995→76,562 | 46.8→59.6 |
| 2 | 626,894 | 541,287 | +13.66% | 9→10 | 39 | 0→6 | 97,597→70,486 | 49.2→49.6 |
| 3 | 575,399 | 801,236 | −39.25% | 9→13 | 56 | 0→1 | 100,284→85,873 | 43.5→78.9 |
| 4 | 939,950 | 679,631 | +27.69% | 12→12 | 45 | 1→0 | 117,000→78,695 | 61.7→62.5 |
| 5 | 704,126 | 646,068 | +8.25% | 10→11 | 49 | 1→7 | 142,778→79,104 | 43.2→58.8 |
| 6 | 704,557 | 647,288 | +8.13% | 10→11 | 49 | 1→1 | 113,992→79,230 | 53.4→69.1 |

**事前登録どおりの６ペア中央値は +8.19%（57,663.5トークンの削減）。** 範囲は −39.25%〜+27.69%、改善４・悪化２。事前登録の採用目安（品質悪化なし・６比較可能ペア・中央値５%以上）は満たした。ただし `comparison.json` の効果判定は区間が閾値をまたぐため `hold`（`interval_crosses_threshold`）であり、ばらつきは大きい。最悪の rep3 は介入側の要求数が 9→13 に増えた経路の差による。

同じ経路で比べられる指標では一貫した差がある。キャッシュ外入力は６ペアすべてで減った（中央値 109,493.5→78,899.5）。一方、介入側は切り詰めた中央部分を `rg` などで取り直すことが多く（再取得０〜７回）、要求数は５ペアで増えた。経過時間の中央値は 48.0秒→61.05秒で、待ち時間は悪化した。

**判断**：事前登録の目安を満たしたため、実験機能として採用する。既定値は無効のまま（`JEV_CODEX_TOOL_OUTPUT_MAX=0`）とし、`JEV_CODEX_TOOL_OUTPUT_MAX=20000` で有効にする。この課題は、モデルが大きな出力上限を選んだ場合の負荷試験である。実作業全体の削減率ではなく、区間が広いので因果も断定しない。実利用で有効化するかは、待ち時間の悪化と再取得の増加を踏まえて利用者が判断する。既知の表示上の問題：`summary.md` の「Requests Jev steered」は、この切り詰めで `changed` になった要求も数える（`JevCalls=0`）。
