# Process 01: 代表要求と採点契約の固定

## goal

ホスト別の代表要求、候補カタログ、正解ツール名、外部成功条件を機械可読なフィクスチャに固定し、正解ツール保持率の分母を実行前に検証できるようにする。

## files

- `scripts/testdata/selection-benchmark/input.json`
- `scripts/summarize_selection_benchmark.py`
- `scripts/test_summarize_selection_benchmark.py`
- `docs/requirements/selection-benchmark.md`

## verify

```sh
python3 -m unittest discover -s scripts -p 'test_summarize_selection_benchmark.py'
```

## depends_on

なし
