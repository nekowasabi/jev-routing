# Jev効率改善の基礎修正・計測計画

## 目的

[最終改善案](docs/requirements/jev-efficiency-final-proposal.md) の①互換性・情報保持と②タスク全体の計測を実装する。
提案末尾の「まず①と②」を実行範囲とし、③個別比較・④処理省略・⑤候補選別の採用は今回得る測定結果を前提とする。
既存の `internal/host`・`plan`・`proxy`・`compact`・`jev` と比較スクリプトを拡張する。
根拠は実コードと [再現検証](docs/reviews/jev-grok-execution-verification.md)。隔離コピーに依存しない回帰テストへ整理する。

## 対象外

- ③〜⑤の最適化の採用、削減率・速度向上率の保証、ホスト全種への対応済み宣言。
- CursorのRPC実装、ホスト内部の履歴圧縮、待機・委譲制御、モデル変更、新しい記憶基盤。
- 専用計測基盤、ダッシュボード、新規の汎用抽象層。
- 既存の `PLAN.md`・`plan/` の実行や変更。本計画はそれらの実装を前提にしない。
- 実サービスの未確認仕様を推測した実装。未観測の使用量・費用・親子関係は欠測として明示する。

## 工程一覧

各工程を1回の実装単位とし、番号順に直列実行する。依存は記録用であり、並列実行しない。
各工程のテストはその工程に含め、品質・文書だけの独立工程を設けない。

| 番号 | 題 | 依存 |
|---|---|---|
| 01 | [呼出しID対応とResponses履歴](plan-efficiency/process-01.md) | なし |
| 02 | [Grokの実カタログ名と旧名互換](plan-efficiency/process-02.md) | 01 |
| 03 | [不確実な圧縮判定で履歴を保持](plan-efficiency/process-03.md) | 01 |
| 04 | [完全な評価入力に基づくキャッシュ](plan-efficiency/process-04.md) | 03 |
| 05 | [外部判定入力の予算と制約の保護](plan-efficiency/process-05.md) | 03、04 |
| 06 | [強制ツール指定と推論設定の契約保持](plan-efficiency/process-06.md) | 01、02、05 |
| 07 | [要求単位の観測と欠測の明示](plan-efficiency/process-07.md) | 01〜06 |
| 08 | [タスク単位の集計と外部完了判定](plan-efficiency/process-08.md) | 07 |

## 全体の受入コマンド

リポジトリ直下で実行する。外部サービスを使わず、模擬HTTP応答と固定入力で契約を検証する。

```sh
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./...
go vet ./...
bash -n scripts/test-x-cell.sh
python3 -m unittest discover -s scripts -p 'test_summarize_x_cell.py'
bash scripts/test-x-cell.sh --summarize scripts/testdata/x-cell
```

集計器・固定入力・`--summarize` は工程08の実装対象。受入条件は完了品質、観測範囲、欠測、実呼出しが機械判定できること。
実サービスでの性能改善は上記テストの合格だけでは主張しない。未実測ホストは未検証、素通し経路は最適化未適用と表示する。
タイムアウトや順序保証は具体的な期限・状態遷移のテストで扱い、単に遅いという理由で障害と判定しない。

計画の形式検証:

```sh
python3 /Users/takets/repos/private_dotfiles/agents/skills/make-plan/tests/check-process-fields.py . --prefix efficiency
```

実装開始コマンド: `/x @PLAN-efficiency.md`
