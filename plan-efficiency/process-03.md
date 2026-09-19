# Process 03: 不確実な圧縮判定で履歴を保持

## goal

`NoulOf`・`DecideCall`・`AskCompact` と呼出し側を通じて、有効な低評価と欠測・不正評価・通信失敗を区別する。
評価の型、必要フィールド、有限値、範囲を検証し、数値ゼロを欠測扱いしない。判定に必要な回答が揃わない候補は呼出し・結果とも保持する。
部分回答、応答不正、HTTP失敗、タイムアウト時の不確実な評価を削除判定としてキャッシュしない。
`RewriteWithClient` がエラー時に先に計算したローカル削除へ戻る経路も修正し、外部判定失敗で必要な履歴が消えないようにする。
明示的なローカル運用と外部判定失敗を区別し、既存の正常な低評価による削除・切詰めと固定保持は維持する。
既存の「失敗ならローカル圧縮」というテストは、新しい情報保持契約を検証する内容へ変更する。
模擬サーバーで全欠損・部分回答・不正型・範囲外・有効なゼロ・複数バッチの一部失敗を検証し、再送で不正回答が再利用されないことも確認する。

## files

- `internal/compact/compact.go`
- `internal/compact/compact_test.go`
- `internal/jev/jev.go`
- `internal/jev/jev_test.go`
- `internal/proxy/rewrite.go`
- `internal/proxy/rewrite_test.go`

## verify

```sh
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./internal/compact ./internal/jev ./internal/proxy
```

## depends_on

01
