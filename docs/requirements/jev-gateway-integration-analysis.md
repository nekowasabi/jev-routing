# jev-gateway の導入・ダッシュボード移植分析

調査日: 2026-09-19

## 結論

`jev-gateway` から部分的に導入する価値はある。優先すべきは、APIごとの互換性保護、応答からの使用量収集、判定理由の記録、ダッシュボードである。選択ロジック全体の置換や、LLMを省略する `direct` 経路の無条件導入は推奨しない。

画面は外部ライブラリ不要の HTML と JavaScript であり、Go の既存 HTTP サーバーに組み込める。ただし、画面ファイルだけをコピーしても動かない。履歴・使用量・判定理由を提供する API と、jev-routing の実態に合う表示項目が必要である。

実測による速度向上・費用削減は未確認。今回確認したのはコード上の成立条件、既存テスト、問題入力の再現であり、実サービス上の性能改善ではない。

## 対象と再現条件

| 対象 | 調査時点 |
|---|---|
| [jev-gateway](https://github.com/vinilana/jev-gateway) | `5dde23482c85dbdf273ffa026693426b1516be00` |
| 取得先 | `/tmp/jev-gateway-analysis.bfUbaW/jev-gateway` |
| jev-routing | `f6b9dcfd2d0164e525b6ff981e17e6df26932e29` |
| 作業対象 | ツール呼び出し経路、履歴処理、観測、ダッシュボード |

`git clone https://github.com/vinilana/jev-gateway.git` で取得。依存関係は取得先で `npx --yes pnpm install --frozen-lockfile --ignore-scripts` により導入した。製品コード・接続設定・実サービスの状態は変更していない。調査開始時から存在した `.agent-checkpoints/` と `.grok/` は変更していない。

## 既存実装との位置付け

両者ともクライアントと LLM の間に入る HTTP プロキシである。実ツールを実行するのはクライアント側であり、gateway の `direct` もツール実行そのものではなく、LLM が返す形式のツール呼び出し応答を合成する。

jev-routing はローカル工程選択、Jevによる選択、履歴圧縮、候補ツール限定、推論設定変更を組み合わせる。gateway はプロトコル別アダプターと、確信度に応じた `direct` / `forced` / `hint` / `none` / `passthrough` の切替えを中心とする。この違いにより、gateway のモードや評価値をそのまま jev-routing の統計へ割り当てることはできない。

既存資料の [最終改善案](jev-efficiency-final-proposal.md) と [再現検証](../reviews/jev-grok-execution-verification.md) も確認した。今回、後者の隔離コピーと現在の追跡対象 Go ソース・`go.mod` が一致することを確認し、診断テストを再実行した。過去の問題は現在も再現する。

## ツール呼び出し改善の導入候補

以下の `G/` は取得先の `jev-gateway/`、`R/` は本リポジトリを示す。行番号は上記コミットに対応する。

| 候補 | 確認した事実・根拠 | 判断 |
|---|---|---|
| 呼び出しIDによる結果対応 | `G/src/adapters/chat.ts:14`、`messages.ts:69`、`responses.ts:122` はID対応表を使用。`R/internal/proxy/rewrite.go:479` は結果本文を直前の行動へ代入するため並列結果を誤対応する | ⭐⭐⭐⭐⭐。既存 `compact.Item` のIDで結合する修正を優先。gateway の内部表現全体の移植は不要 |
| Responses の入力正規化 | `G/src/adapters/responses.ts:67,120,133` は `additional_tools`、名前空間、custom形式、呼び出し結果を扱う。`R/internal/proxy/rewrite.go:132,286` は対応が不足 | ⭐⭐⭐⭐⭐。形式別の読解とテスト例を移植。認識と介入可否は分離する |
| 明示指定・見えない履歴の保護 | `G/src/decide.ts:49` は既決 `tool_choice` 等を尊重。`G/src/adapters/responses.ts:104` は `previous_response_id` を伴う要求への介入を控える。`R/internal/proxy/rewrite.go:119` は候補限定時に `tool_choice` を削除 | ⭐⭐⭐⭐⭐。呼び出し禁止や指定済みツールを上書きしない条件を追加する |
| 確信度・回答検証 | `G/src/decide.ts:141,160,163,177` は型・確信度・独立質問との整合・未知ツールを検査。`R/internal/proxy/rewrite.go:179` は選択成功時に固定値 `0.8` を使う | ⭐⭐⭐⭐⭐。実確信度と回答の妥当性を使用する。固定値を成功率と解釈しない |
| 推論・キャッシュを保つヒント | `G/src/adapters/messages.ts:88,108` は思考有効時やキャッシュ利用時に末尾へヒントを追加。現行はカタログと推論設定を変更 | ⭐⭐⭐。比較実験候補。カタログ削減と同じ効果ではなく、モデルが提案を無視する可能性もある |
| 上流拒否時の復帰 | `G/src/app.ts:163` は書換要求が400/422の場合に元要求を再送。現行 `ReverseProxy` に同等処理なし | ⭐⭐⭐。対象を明確にし、追加要求と遅延を計測する。任意の障害への無条件再送には拡張しない |
| 大量カタログの候補抽出 | `G/src/questions.ts:91`、`decide.ts:126` は分割質問と最終選択の段階構成 | ⭐⭐⭐。候補数が原因の品質低下を確認してから。現行のローカル選択と入力予算処理を活かす |
| LLMを省略する `direct` | `G/src/decide.ts:183` は確定可能と判断した引数から呼び出し応答を合成 | ⭐。今回の再現で複合スキーマの必須引数を見落とした。完全なスキーマ検証・ホスト承認・継続履歴との整合を確認するまで保留 |

`forced` はツール種類を明示的に強制するが、現行の単一カタログ化は同じ意味ではない。異なるツールの同時選択が制約される一方、単一種類のツールを複数回呼ぶ並列処理まで必ず禁止されるわけではない。gateway にツール並列実行器や子エージェント管理機能があるわけでもない。

### 遅延評価で確認すべき具体的な前提

gateway は `G/src/config.ts:67` にJevのタイムアウト、`G/src/index.ts:14` に再試行設定を持つ。現行は `R/internal/jev/jev.go:51` にHTTPタイムアウトがあり、`R/internal/proxy/rewrite.go:61,75` で圧縮と選択の判定が逐次発生し得る。両者とも受信要求のキャンセルがJev判定へ伝播しない。

したがって「gateway の方が速そう」という推測で置換しない。クライアントの総期限、再試行を含む累積待ち時間、切断後にも残る外部要求を測定する。既存の判断軸「壁時計サイクル遅延の影響は『何が壊れるか』を具体的に特定してから評価する」に基づく評価であり、今回タイムアウト障害そのものを実証したわけではない。

## ダッシュボード移植の具体案

### 再利用できるものと追加が必要なもの

`G/src/dashboard.html` は外部スクリプト・CSS・画像依存がなく、`G/test/dashboard.test.ts:150` でもその性質を検証している。Go の `embed` と `net/http` で配信でき、Node/Hono を本体へ導入する必要はない。

通信は2秒間隔のJSONポーリングであり、ダッシュボード用SSEではない（`G/src/dashboard.html:154`）。上流LLMのSSE応答を解析する処理とは別である。

| 元の契約 | 移植先での扱い |
|---|---|
| `GET /dashboard` | HTML/CSSと表示構成を再利用し、表示名・分類・集計式を現行に合わせる |
| `GET /dashboard/events?since=<seq>` → `{router, events}` | 連番付きの限定履歴をGo側へ追加。`startedAt` の変更で再起動を検出する契約は再利用可能 |
| `POST /dashboard/routing?enabled=true\|false` | 初期移植では保留。比較用に実装する際は「全書換無効」と「ツール選択だけ無効」を区別する |
| ホスト別カード、状態、通過理由、直近一覧 | まず現在のプロセスだけを表示。複数接続先は明示指定で後から追加 |
| 有効／無効の使用量比較 | 同等の課題・開始状態で実行した記録だけを比較。平均の差を因果効果と断定しない |

APIの根拠は `G/src/dashboard.ts:26,28,37,57`。表示の根拠は `G/src/dashboard.html:243,319,350,373,381,417,430,454`。

Go側の既存 `/stats` は直近の `RewriteStats` と累積件数・文字数だけである（`R/internal/proxy/proxy.go:102`）。`ModifyResponse` は何も取得せず、応答完了前の書換時点で統計を更新する（同 `:127,145`）。このため、履歴・上流状態・終了時間・利用量の収集が追加必須となる。

移植元の `src/state.ts` はJevへ渡す会話構築であり、ダッシュボードの状態保存ではない。画面移植に含める必要はない。

### 表示上の意味を合わせる

- `Engine=live` は外部サービスが設定されている意味で、Jev実通信の証拠ではない。実呼び出し箇所で件数・時間を記録する（`R/internal/proxy/rewrite.go:95`）。
- `Done` は完了度であり、選択の確信度ではない。別の値として取得する。
- ツール選択が素通しでも、先に履歴圧縮が実施される場合がある。「素通し＝元要求を無変更」と表示しない（`R/internal/proxy/rewrite.go:60,102`）。
- モードは単一の名前に押し込めず、候補限定・圧縮・推論設定変更の有無、判定元、通過理由を記録する。
- `run` はポート競合時に任意ポートを使用する。gateway の固定ポート探索だけでは複数セッションを見つけられない（`R/cmd/jev-routing/main.go:191`）。

### 利用量の計測

`G/src/usage.ts:23` はプロバイダーごとの差を正規化する。OpenAIのキャッシュ分は入力総量の内数、Anthropicのキャッシュ読取・作成は別項目として合算し、推論分は出力総量の内数として扱う。この正規化規則とテスト例は移植価値が高い。

Goでは `Response.clone()` をそのまま移せない。既存転送を維持する応答本文の包装などにより逐次観測し、ストリーム全体を先に読み切ってから返す構成を避ける。分割された行、JSON応答、途中切断、EOFとCloseでの二重記録、読取上限を検証する。

取得不能な使用量はゼロと分ける。本文の文字数削減をトークン削減や料金削減と呼ばない。gateway の画面にはJev単価の固定値があるが、現行料金の根拠にはできない（`G/src/dashboard.html:158,369`）。初期は料金表示を省き、使用量と欠測を表示する。

### 情報保護・保存範囲

`G/src/events.ts:43` の許可フィールド方式と、`G/src/dashboard.html:211` の安全なDOM組立ては再利用する。プロンプト、結果本文、引数、認証情報は画面用履歴へ入れない。上流URLもユーザー情報・秘密クエリを除去して表示する。

gateway の既定メモリ保持は1,000件、ログ復元は末尾2MiB、ブラウザーの保持も限定される。これはログファイル自体の容量制限ではなく、画面の「All」も全期間保証ではない（`G/src/events.ts:36,110`、`dashboard.html:156`）。移植先でも表示対象期間と欠落を明示する。

初期案は同一オリジン・ループバックの読み取り専用画面。公開待受を許す際はアクセス制御を追加する。gateway の localhost 向けCORS制限は認証の代わりにはならず、`?key=` の認証方式をそのまま移す必要はない（`G/src/dashboard.ts:15`、`app.ts:184`）。

### ライセンス

取得した `G/LICENSE:1` はMITで、Vinicius Lanaの著作権表示と許諾文を含む。HTMLやコードの相当部分を移植する場合は、この表示・許諾文を移植先でも保持する。今回行ったのはライセンスファイルの確認であり、コード自体の移植はまだ行っていない。

## 推奨する実施順序と合格条件

1. **履歴と介入条件を修正する。** → 検証: ID対応、Responses、未知ツール、既決 `tool_choice`、見えない履歴のテストを通す。現行の入力予算・目的変更キャッシュの再現失敗も解消する。
2. **計測と読み取り専用画面を移植する。** → 検証: 履歴の連番・上限・再起動、秘密値の除外、JSON/SSEの使用量、途中切断、応答を遅延させない転送を確認する。
3. **基準条件との比較を追加する。** → 検証: 全書換を止めた条件と、選択・圧縮・推論設定を個別変更した条件で同じ課題を比較する。完了品質、総ターン数、再取得、親子合計の使用量、総所要時間を測る。
4. **ヒント・候補抽出等を限定導入する。** → 検証: 対象ホストごとに品質と総費用・時間の改善を確認した方式のみ採用する。`direct` はスキーマ・承認・継続履歴を別途検証してから判断する。

最小構成は、既存 `internal/proxy` に計測・履歴と画面配信を足し、`internal/jev` の実通信箇所に観測情報を追加するもの。別サービス、データベース、フロントエンドの構築基盤は初期段階では不要である。

## 検証記録

### jev-routing の既存テスト

実行場所: `/Users/takets/repos/jev-routing`

```sh
go test ./...
```

終了コード0。全パッケージ成功。代表出力:

```text
ok  	github.com/nekowasabi/jev-routing/internal/proxy	(cached)
```

### gateway の既存テストと型検査

実行場所: `/tmp/jev-gateway-analysis.bfUbaW/jev-gateway`

```sh
npm test
npm run typecheck
```

いずれも終了コード0。

```text
 Test Files  6 passed (6)
      Tests  54 passed (54)
```

### 既存問題の再現

実行場所: `/Users/takets/repos/jev-routing-review-verify-20260919`

```sh
go test -count=1 -json ./internal/plan ./internal/proxy ./internal/jev -run TestReviewVerify
```

追跡対象 Go ソース・`go.mod` の現行との差異は空。終了コード1、診断テスト13件が失敗した。既存テストとの違いは検査する入力・期待値にある。

再現した問題群:

- Grok のツール名対応と工程選択。
- Claude・Grok の並列結果対応、および未知の呼び出しIDの誤対応。
- Responses の `function_call` / `function_call_output` を行動履歴へ取り込めない。
- 入れ子の結果本文が入力予算に収まらない。
- 目的変更後も圧縮判定キャッシュが再利用される。

並列対応の問題は Jev へ渡す行動状態の誤りであり、この試験だけで上流LLMへ渡す全結果本文が交換されるとは結論しない。

### gateway の複合スキーマ見落とし

`G/src/questions.ts:54` は主に `properties` を調べるため、`allOf` 内に必須の文字列引数がある場合でも引数を確定済みと扱う。Jev回答を模擬した以下のコードで、`decide()` が `direct` と空の引数を返すことを確認した。HTTP応答生成・実ツール実行はこの再現の対象外。

```sh
cd /tmp/jev-gateway-analysis.bfUbaW/jev-gateway
node --import tsx --input-type=module <<'JS'
import assert from 'node:assert/strict';
import { planTool } from './src/questions.ts';
import { decide } from './src/decide.ts';
import { loadConfig } from './src/config.ts';

const tool = {
  kind: 'function', name: 'test',
  parameters: {
    type: 'object',
    allOf: [{ required: ['token'], properties: { token: { type: 'string' } } }],
  },
};
assert.deepEqual(planTool(tool).closedParams, []);
const result = await decide(
  { system: '', turns: [{ role: 'user', text: 'test' }], tools: [tool], toolChoice: 'auto' },
  { ...loadConfig(), directCalls: true },
  async () => ({
    answers: {
      tool: { type: 'choice', choice: 'test', confidence: 0.99, probabilities: { test: 0.99 } },
      needs_tool: { type: 'noul', noul: 0.99 },
    },
    usage: { input_tokens: 10 },
  }),
);
assert.equal(result.mode, 'direct');
assert.deepEqual(result.args, {});
console.log('REPRODUCED: allOf required token ignored; direct tool call emitted with {}');
JS
```

終了コード0。問題挙動を確認するアサーションが成功したものであり、正しい動作を保証するテスト成功ではない。

```text
REPRODUCED: allOf required token ignored; direct tool call emitted with {}
```

## 未確認事項

- 実サービス・各ホストでの完了品質、承認処理との接続、費用削減、総所要時間。
- ダッシュボード移植後のブラウザー表示と操作。今回は移植実装・画面起動を行っていない。
- Cursor の Connect RPC を含む、JSON以外の経路への適用。gateway のアダプターを追加するだけでは対応を保証しない。

## Completion Summary

- 追加ファイル: 本分析書。
- 実施: `/tmp` への取得、両実装比較、既存テスト・型検査、既存診断テストの再実行。
- 保留: 製品コードへの導入、ダッシュボード移植、実サービスでの比較評価。
