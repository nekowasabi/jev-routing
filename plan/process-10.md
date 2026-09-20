# Process 10: 比較の妥当性検証と採用判定

## goal

採点契約・選定元・集計の境界を自動検証し、各ホストで比較可能な結果だけを採用判定へ渡す。結果表には分母、欠測、除外理由を必ず表示し、規則変更はこの結果を確認してから別作業として決める。

## files

- `scripts/test_summarize_selection_benchmark.py`
- `scripts/test_summarize_x_cell.py`
- `internal/proxy/gateway_decision_test.go`

## verify

```sh
go test ./internal/proxy -count=1
python3 -m unittest discover -s scripts -p 'test_summarize_selection_benchmark.py'
make test-selection-benchmark claude
```

## depends_on

01, 02, 03

## QA観点表

| 技法 | 適用 | テストケース |
|---|---|---|
| 同値分割 | 4条件と比較不能ホスト | `baseline` / `local` / `jev` / `hybrid` / 未対応形式 |
| 境界値 | `0.85` の選定確信度 | `0.849` は Jev、`0.85` はローカル（`hybrid`） |
| デシジョンテーブル | 選定元 × Jev 可用性 × 応答妥当性 | ローカル選定、Jev選定、Jev失敗・不正・不確実で全候補通過 |
| 状態遷移 | 実行前→書換え確認→外部品質確認→比較可能 | 到達なし・書換えなし・品質不合格を採点から除外 |
| エラー推測 | 欠落ラベル、候補外ラベル、Jev使用量欠測、途中取消 | 分母を変えず欠測・除外理由を出力 |
| チェックリスト | 入力・分岐・状態・運用 | 同一コミット・設定・要求・反復番号の不一致を比較不能にする |
