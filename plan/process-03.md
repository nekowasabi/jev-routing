# Process 03: 四条件の反復実行と集計

## goal

既存の独立 worktree と `JEV_RUN_STATS` を再利用し、`baseline`、`local`、`jev`、`hybrid` を同一要求で反復実行して、正解ツール保持率、外部成功率、待ち時間、上流トークン、Jev 選定トークン（または欠測）を条件別に集計する。書換え不能なホストは採点せず、理由を結果に残す。

## files

- `scripts/test-x-cell.sh`
- `scripts/summarize_x_cell.py`
- `scripts/summarize_selection_benchmark.py`
- `scripts/test_summarize_x_cell.py`
- `scripts/test_summarize_selection_benchmark.py`
- `Makefile`
- `README.md`

## verify

```sh
python3 -m unittest discover -s scripts -p 'test_summarize_x_cell.py'
python3 -m unittest discover -s scripts -p 'test_summarize_selection_benchmark.py'
```

## depends_on

01, 02
