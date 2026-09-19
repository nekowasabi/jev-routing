# Jevツール選択効率の実装・比較計画

## 目的

[要件分析](docs/requirements/jev-tool-selection-efficiency-analysis.md)の最小案を実装し、現行の候補限定方式と比較できる状態にする。
安全なローカル判定を残し、不確実な場合だけJevへ問い合わせ、回答検証・強制選択・引数生成モデル分離・限定直接生成を段階的に追加する。
各工程は `/x @PLAN-analysis.md` の1サイクルで実装と検証を完結させる切片。番号順に直列実行する。
既存の `PLAN.md`・`plan/` や他の接頭辞の計画は前提にせず、同じ製品ファイルへの変更が先に入っていれば再利用して重複実装を避ける。

契約:
- 既定は現行の候補限定。回答不正時の誤介入は修正し、旧挙動の比較には固定された基準リビジョンを使う。
- 新しい強制選択・モデル分離・直接生成は明示設定でのみ有効化する。
- 初期介入対象は、完全なテキスト履歴と通常のfunction定義を持つ非ストリーミングChat JSON要求。通信形式で判定し、ホスト名だけで互換性を推定しない。
- 明示 `tool_choice`（`auto` 以外）、未知の選択指定、名前空間、custom、legacy functions、提供元実行ツール、画像、未知履歴、Responses、Anthropic、バイナリ形式には新方式を適用しない。
- 候補限定を再利用し、比較間で履歴圧縮・推論設定を揃える。選択処理と履歴圧縮のJev問い合わせは別目的として計測する。
- 引数生成モデルは設定された識別子だけを使う。未指定なら元モデル。価格・品質・互換性を推定しない。
- 直接生成は無引数またはスカラー定数引数だけ。実ツール実行・承認・結果継続はクライアントに残す。
- 不適合時は既存上流処理へ戻す。上流拒否後の自動再送を追加しない。
- 実測前に費用削減・高速化・実ホストの承認互換性を保証しない。模擬検証と実測結果を明確に区別する。

## 対象外

- gateway全体の移植、Jev常用化、全カタログ保持への置換、全候補への引数先行質問、部分引数委譲。
- 自由記述・列挙値・真偽値の推測による直接生成、SSE応答合成、未検証の通信形式への介入。
- 今回の計画作成での製品実装、有料API評価、モデルの自動選定、既定方式の自動昇格。
- 永続イベントDB、公開ダッシュボード、画面からの設定変更、独立した品質・文書・回顧工程。

## 工程一覧

| 番号 | 題 | 依存 |
|---|---|---|
| 01 | [Jev回答検証と不確実時の復帰](plan-analysis/process-01.md) | なし |
| 02 | [実通信・上流使用量・要求結果の計測](plan-analysis/process-02.md) | 01 |
| 03 | [比較条件と適合要求への強制選択](plan-analysis/process-03.md) | 01, 02 |
| 04 | [許可ツールの引数生成モデル分離](plan-analysis/process-04.md) | 03 |
| 05 | [無引数・定数引数の限定直接生成](plan-analysis/process-05.md) | 03, 04 |
| 06 | [タスク単位の比較と外部成否判定](plan-analysis/process-06.md) | 02, 03, 04, 05 |
| 07 | [比較判断用の読み取り専用画面](plan-analysis/process-07.md) | 02, 06 |

工程05は工程04のモデル変更を前提にせず、同一モデルの強制選択を対照群にする。依存欄は記録であり並列実行を指示しない。

## 全体の受入コマンド

リポジトリ直下で実行する。以下は実装後の受入条件であり、計画作成時の通過を意味しない。

```sh
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./...
go vet ./...
node --test internal/proxy/assets/dashboard.test.mjs
python3 -m unittest discover -s scripts -p 'test_summarize_selection_comparison.py'
python3 scripts/summarize_selection_comparison.py scripts/testdata/selection-comparison
bash -n scripts/test-x-cell.sh
```

各工程の新規テストは `go test -list` でも存在確認し、対象なしでの見かけの成功を防ぐ。
実サービスの品質・総費用・総時間は別途実測事項。費用情報欠落時にゼロや節約率へ変換しない。
時間依存の評価は、判断軸「壁時計サイクル遅延の影響」に基づき、総期限・再試行・切断後の残存要求を具体的に確認する。

計画形式の検証:

```sh
python3 /Users/takets/repos/private_dotfiles/agents/skills/make-plan/tests/check-process-fields.py . --prefix analysis
```
