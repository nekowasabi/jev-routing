# Process 06: タスク単位の比較と外部成否判定

## goal

既存 `scripts/test-x-cell.sh` の固定課題・実行結果出力を再利用し、選択効率の比較記録を集計する最小のPython標準ライブラリ処理を追加する。旧キーは維持し、タスクID・試行ID・リビジョン・実効設定・終了状態を記録する。旧ログで未取得の項目は欠落扱いとする。
比較群は要件分析の基準リビジョン `f6b9dcfd2d0164e525b6ff981e17e6df26932e29` の現行方式、同一モデルの回答検証＋強制選択、許可引数モデル、同一モデルでの独立directの4群。基準結果にはリビジョンを必須とし、未取得なら基準欠落と表示して改善率を出さない。新旧コードを同じ作業ツリーで切り替える仕組みは追加しない。
課題・入力履歴・圧縮・推論条件が一致しない群は比較不能とする。ローカル/Jev比率、実通信、入力量、選択適用率、強制選択受理、direct適用率、キャッシュ、再試行・修復、使用量、タスク総時間、外部成否を集計する。read/searchと自由記述課題を区別する。
タスク成功はLLM自己申告や終了コードだけでなく、固定成果物・引数妥当性・実行結果を外部検査した記録で判定する。失敗・期限超過・修復要求を母集団から除外しない。費用は全Jev・全LLM・修復要求を含め、明示された単価または実費と出典・単位が揃う場合だけ算出する。欠落費用は未知、品質低下時は採用不可とする。
既存スクリプトの最後の `turn.completed` による使用量上書き、欠落のゼロ扱い、`CHECK: PASS` 自己申告だけの判定を置き換える。全ターンを重複なく合算し、非0終了や外部検査失敗では成功扱いにしない。推論・キャッシュが総使用量の内数である場合は二重加算しない。
総期限・再試行・切断後の要求を個別に記録し、遅延だけから障害を推定しない。模擬比較を実測と表示しない。
`test_summarize_selection_comparison.py` に、失敗後修復、費用欠落、品質低下、条件不一致、基準欠落、4群正常比較を含める。固定データと期待値を照合し、READMEへ実測の入力条件を記載する。有料APIや実エージェントを受入検証で起動しない。

## files

- `scripts/test-x-cell.sh`
- `scripts/summarize_selection_comparison.py`（新規）
- `scripts/test_summarize_selection_comparison.py`（新規）
- `scripts/testdata/selection-comparison/`（固定比較入力・期待値、新規）
- `README.md`

## verify

```sh
python3 -m unittest discover -s scripts -p 'test_summarize_selection_comparison.py'
python3 scripts/summarize_selection_comparison.py scripts/testdata/selection-comparison
bash -n scripts/test-x-cell.sh
```

## depends_on

02, 03, 04, 05
