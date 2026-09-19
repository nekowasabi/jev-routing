# Grok・Cursor CLI・Devin CLI の修正ポイント

調査日: 2026-09-19。対象は現在の作業ツリーとインストール済みCLI。製品コードの修正・課金を伴うライブ推論は実施していない。Cursor CLI は、このリポジトリが起動する `cursor-agent` を指す。

目的は、Claude／Codexと同様に、既定の `filter` モードでツール候補の選択と履歴圧縮を実際に適用し、同じ課題を正常に完了させること。`forced`／`direct` の追加対応は別件とする。

## 結論と優先順位

推奨順序 ⭐⭐⭐⭐⭐: **既存の計測で実形式を確認 → GrokのJSON経路を修正 → Cursor／Devinの統合点を確定 → 対応可能な経路だけ実装 → ホスト別に反復検証**。Grokは既存のJSON処理を利用できる見込みがある一方、Cursor／Devinは通信内に操作対象が存在するか自体が未確認だからである。

| ホスト | 現時点の障害・不足 | 最初にすること | 主な変更候補 |
| --- | --- | --- | --- |
| Grok | 未知定義1件でカタログ全体を拒否する | 実際に拒否された定義の型・キー・実行主体を確認 | カタログ分類、必要なら履歴型と結果抽出 |
| Cursor CLI | 制御RPC・内包データと、モデルへの推論要求を区別できていない | 復号された要求内に候補一覧と履歴本文があるか確認 | 限定したRPC変換、元の位置への書き戻し、履歴抽出 |
| Devin CLI | 想定RPCが推論パス判定の対象外。JSON以外も未対応 | プロキシへ実際に届く経路・内容型・要求型を確認 | 経路分類、必要なフレーム処理、候補・履歴の変換 |

**パス追加やprotobuf復号だけで対応完了とはしない。** サーバー内部で作られる候補や履歴は、CLI通信を書き換えるだけでは制御できない可能性がある。

## 既に実装済みの共通基盤

[元の障害解析](test-x-cell-failure-analysis.md) 以降に次が追加されている。新規実装として重複計上しない。

- 全要求数・経路別件数と、対象推論要求数の分離: [proxy.go](../../internal/proxy/proxy.go) の `Handler`（270行付近）、`RunStats`（105行）。
- カタログの型・キー・件数・抽出位置、履歴の型、遅延読み込み等の有無: [catalog_shape.go](../../internal/proxy/catalog_shape.go)。本文・引数・認証情報は保存しない。
- 要求別の選択元・適用方式・Jev呼び出し目的・上流状態: [events.go](../../internal/proxy/events.go:14)。
- Noulの正式応答形式、候補の能力説明に基づく選択、補助通知を除いた判断対象: [rewrite.go](../../internal/proxy/rewrite.go) の `askNextTool`、[plan.go](../../internal/plan/plan.go) の `WorkRequest`。
- 選択と圧縮の分離、呼び出しと結果の対応保護、発見用定義・遅延定義・参照定義の保持。各ホストの実形式への適合は別途確認する。
- 外部の正答照合、作業ツリー非変更、CLI終了値、選択・圧縮の適用と上流正常完了を要求する受け入れ判定: [summarize_x_cell.py](../../scripts/summarize_x_cell.py:58)。不合格はハーネスの非ゼロ終了値になる。

## 1. Grok

### 確認できたこと

- `catalogReason`（[rewrite.go](../../internal/proxy/rewrite.go:486)）は、名前を取得できない未知定義があると、認識済みの関数も含めて全体を `unrecognized_format` として通過させる。
- `isProviderExecuted`（同524行）に `x_search` はない。ただし、過去ランの未知項目がこれだった証拠はない。型名を推測で追加して解決扱いにしない。
- 既存の `TestGrokMixedHostedToolsStillFiltersFunctions`（[rewrite_test.go](../../internal/proxy/rewrite_test.go:931)）は通常関数と既知の `web_search` の人工例であり、実際の拒否対象を含まない。
- 現地 `grok --help` では、ハーネスで使う `--single`、`--output-format json`、`--no-plan`、`--no-subagents`、権限モードを確認した。標準出力形式がJSONであることは、上流要求の形式の証明にはならない。

### 修正の順序

1. 既存の `events[].catalog` と `reason` で、実際の未知定義を特定する。型・キー・構造だけを最小テスト入力に残す。
2. 実行主体が確認できた提供側ツールは定義を完全に保持し、ローカル関数だけを選択する。未知項目の無条件削除や、全未知型の一括許可はしない。
3. 提供側ツール使用後の履歴型も確認する。カタログだけ通っても、`historyReason`／`contentReason` で止まる可能性がある。必要な型だけ対応し、結果の対応・順序を保つ。
4. 現行の結果抽出がGrok実出力と一致するか確認する。不一致ならGrok用の最終回答・終端状態・使用量の抽出を修正する。

修正候補は主に `internal/proxy/rewrite.go` と回帰テスト。既存の `run_terminal_command`／`spawn_subagent` 対応を、根拠なく一律に作り直す必要はない。

## 2. Cursor CLI

### 確認できたこと

- 起動側は `CURSOR_API_ENDPOINT`／`CURSOR_API_BASE_URL` と `--endpoint` を設定する（[host.go](../../internal/host/host.go:144)、同190行）。現CLIのヘルプでも接続先指定を確認した。
- インストール済みCLIのJSには `Run`／`RunSSE`／`RunPoll`、`BidiAppend` と、内部データを `dataBinary` または16進表現の `data` に入れる経路がある。これは静的確認であり、次の実ランで使われる経路は未確認。
- `AgentRunRequest` の `mcpTools` が、組込みツール全体のモデル向けカタログと同じとは確認できていない。
- 現プロキシは対象パスのPOSTで、本文全体がJSONとして妥当な場合だけ変形する（[proxy.go](../../internal/proxy/proxy.go:278)、同309行）。内包RPCの復号はしない。
- `TestCursorMcpToolsCatalogIsFilterable`（[rewrite_test.go](../../internal/proxy/rewrite_test.go:975)）は `mcpTools` に組込み名を直接並べた人工例であり、実CLI対応を証明しない。

### 先に確定する統合点

1. 匿名化した相関IDで、送信経路・内容型・内部要求型を対応づける。
2. その要求に、モデルが選択する候補の完全な定義と、圧縮可能な会話履歴本文が存在するか確認する。
3. 候補と履歴が存在するなら、その要求型だけを復号し、既存の `plan.Spec`／`compact.Item` へ変換して元の領域へ書き戻す。
4. 候補がサーバー内部で構築される、または通信が参照ID・状態差分のみなら、モデル呼び出し境界や公式拡張点の有無を調べる。MCP登録や実行許可フックだけで、組込みツールの選択・履歴圧縮まで代替できるとは扱わない。

### 現JSON経路の独立した修正箇所

- **抽出と書き戻しが非対称。** `cursorToolDefs`（[rewrite.go](../../internal/proxy/rewrite.go:853)）は `action` 内を再帰探索するが、`setCursorTools`（同876行）は再帰しない。`action.mcpTools` から取得した場合、元カタログを残して最上位に別の `mcpTools` を追加し得る。同じ場所に書き戻す回帰テストが必要。
- **履歴抽出が不足。** `RewriteWith` は最上位の `messages`／`input` だけから履歴を得る。`userFromAction` は入れ子の履歴から依頼文だけを取得するため、その経路では履歴圧縮・実行履歴に基づく次ツール選択が成立しない。実形式を確認したうえで、候補と履歴を同じ要求から取り出す必要がある。

これらのJSON修正だけで、実RPCへの対応完了とはしない。

## 3. Devin CLI

### 確認できたこと

- 起動側は `DEVIN_API_URL` を設定する（[host.go](../../internal/host/host.go:144)）。これだけでは、実際に推論要求がプロキシ経由になった証明にならない。
- 現地バイナリに `/exa.api_server_pb.ApiServerService/GetChatMessage`、`GetDevstralStream`、`application/proto` が存在する。ただし、対象実ランのURL・形式は未確認。
- この候補パスは `looksLikeLLM`（[proxy.go](../../internal/proxy/proxy.go:428)）に一致しない。`reached=0` でも `totalRequests`／`requestRoutes` を見て、迂回と対象外パスを区別する必要がある。
- `TestDevinPromptToolsCatalogIsFilterable`（[rewrite_test.go](../../internal/proxy/rewrite_test.go:1014)）は `prompt + tools` の人工JSON。実RPCの形式や履歴圧縮を証明しない。

### 修正の順序

1. 既存の経路別件数で実到達を確認し、内容型・内部要求型を取得する。
2. 実際に推論を運ぶRPCだけを明示分類する。`/exa...` 全体を一律に推論扱いしない。
3. protobuf等なら、対象データを運ぶ型・フレームだけに対応する。パス追加だけでは `json.Valid` の条件を越えられない。
4. 候補一覧・履歴本文がその通信にない場合は別の統合点を調べる。到達数が増えただけでは、選択・圧縮対応と判定しない。
5. CLI最終回答と失敗状態の抽出を、実出力に合わせる。現ハーネスのDevin用「JSONとして読めなければ標準出力全体」の扱いが妥当か検証する。

## 4. 追加する共通の観測・判定

| 項目 | 現状 | 次の対応 |
| --- | --- | --- |
| 全通信 | 件数とmethod/pathは計測済み | 対象外パスにも、許可した内容型・圧縮方式・応答状態・RPC種類を記録できるようにする |
| RPCの正常終了 | 現在のイベントはHTTP応答と本文読取りの完了が中心 | ストリーム内エラー・終端通知・トレーラー等、該当プロトコルの意味上の成功を判定する |
| 復号・再符号化 | JSON経路のみ | 対応する場合は要求ID・順序・未知フィールド・フレーム境界を維持する |
| CLI結果 | Codex以外は主に単一JSONの `result`／`text` を想定 | 各CLIの実際の終端形式を最小テスト入力で検証する |
| 性能比較 | モデル・推論量の明示と記録は主にCodex | 各ホストで比較条件をそろえ、課題・モデル・推論量・圧縮設定を記録する |
| 削減量の単位 | `len(raw)` のバイト差を「文字」と表示 | バイトと明記する。入力トークン、キャッシュ、Jev自身の消費は別集計にする |

本文・引数・認証情報は保存しない。必要な実形式の証拠は、型・キー・件数・フレーム属性と、秘密情報を除いた固定テスト入力で残す。未知形式は破壊せず通過させ、未適用と記録する。

根拠: [proxy.go](../../internal/proxy/proxy.go) の `Handler`／`ModifyResponse`、[usage.go](../../internal/proxy/usage.go) の `usageCollector`／`wrapUsage`、[test-x-cell.sh](../../scripts/test-x-cell.sh:103) の結果抽出と同170行の表示。

## 合格条件と実装順序

1. **実形式の確認** → 候補の所有者、候補と履歴の所在、通信形式を特定する。未特定のホストは実装方式を確定しない。
2. **回帰テスト** → 実際の未対応分岐を再現し、元の位置への書き戻し、発見用定義、呼び出し・結果の対応、未知フィールドの保持を確認する。
3. **選択単独** → `compaction=off / reasoning=preserve` で選択適用・上流の意味上の成功・正答・CLI終了値0・作業ツリー非変更を確認する。
4. **圧縮併用** → `compaction=on` で同じ品質条件と、実際の圧縮差分・対応要求の成功を確認する。履歴本文が見えない構成では圧縮成功を主張しない。
5. **反復と既存ホストの回帰** → 同条件で複数回成功を確認し、Claude／Codexも維持する。Codexの試験は引き続き `gpt-5.6-terra`／`low`。
6. **性能評価** → 品質合格後にのみ実行時間と使用量を比較する。子プロセス・再試行・キャッシュの範囲を明示し、単発の時間差を一般的な高速化と断定しない。

今回未実施なのは、3ホストの新しいライブ要求取得・製品修正・修正後の実試験。過去の障害解析、現コード、現地CLIの静的情報から確定できる範囲を整理した文書である。
