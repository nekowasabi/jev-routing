# Process 01: 呼出しID対応とResponses履歴

## goal

`itemsFromMessages` → `actionsFromItems` → 工程選択・圧縮反映の経路で、呼出しと結果をIDで結合する。
`actionsFromItems` の直前行動への結果代入を除き、並列・順不同でも対応する行動だけを更新する。
未知ID・空IDは既知の呼出しに補完しない。結果欠損は保留、対応IDのある空本文は完了として区別する。
Responsesの `function_call` / `function_call_output` を `call_id` に基づく既存の `compact.Item` へ変換し、引数・本文を保持する。
`applyCompactToMessages` もResponses形式に対応させ、呼出しと結果の組を保った削除・結果短縮を行う。未対応の要素やフィールドは保持する。
Claude/Grokの並列・逆順・未知ID・欠損・空本文・同名ツール複数回、Responsesの検索→読取→編集への移行と圧縮後の整合を回帰テストにする。
過去の隔離再現テストは必要な入力と期待動作だけを既存テストへ整理し、隔離コピーへの依存を残さない。

## files

- `internal/proxy/rewrite.go`
- `internal/proxy/rewrite_test.go`

## verify

```sh
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./internal/proxy ./internal/plan
```

## depends_on

なし
