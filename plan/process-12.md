# Process 12: 比較結果の集計と外部完了判定を実装

## goal

既存 scripts/test-x-cell.sh の集計を再利用可能な標準ライブラリの処理へ分離する。--summarize <fixture-dir> は外部CLI/ネットワーク/worktree作成なしで動く経路とする。比較は修正後filterを基準にforced/args/directを識別し、圧縮・推論・モデル等が揃わない比較を不成立として示す。
全ターンの使用量を集計し、失敗・中断・再試行を除外しない。キャッシュ/推論の内数を二重計上せず欠測を0にしない。観測範囲・課題ID・設定・ソース指紋を記録する。料金表がない限り費用と削減率はnullとし、使用量差を費用改善と呼ばない。
既存の「CHECK: PASS」文字列を完了判定にしない。比較用の固定小型Go課題について、成果物の意味/期待値・go testで独立判定する。指示文に正解を埋め込まず、ホスト自己申告だけの成功を認めない。
実行順を交替する反復設定と結果形式を用意するが、verifyでは偽CLI/固定フィクスチャだけを用いる。新たな危険な権限解除、ユーザーworktreeの削除、既存変更の破棄はしない。実API性能評価はこの計画の機械受入外で、未測定と出力する。
集計・比較不成立・欠測・失敗込み分母・虚偽PASS・意味の誤った成果物を unittest で検証する。READMEへ実測前に効率改善と断定しない採用条件を記載する。

## files

- `scripts/test-x-cell.sh`
- `scripts/summarize_x_cell.py（新規）`
- `scripts/test_summarize_x_cell.py（新規）`
- `scripts/testdata/x-cell/（新規固定フィクスチャ）`
- `README.md`

## verify

リポジトリ直下で実行する。以下の名前の回帰テストをこの工程で実装し、実APIへの接続なしで検証する。

```sh
python3 -m unittest discover -s scripts -p 'test_summarize_x_cell.py'
bash -n scripts/test-x-cell.sh
bash scripts/test-x-cell.sh --summarize scripts/testdata/x-cell
```

## depends_on

11

