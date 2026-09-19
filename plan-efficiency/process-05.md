# Process 05: 外部判定入力の予算と制約の保護

## goal

`FitInput`・`FitState`・`BatchCandidates` と `Ask` の送信直前検査を整合させ、入れ子の `actions_taken.Result` や履歴を含めた実際の直列化内容を評価する。
既存の予算定義 `JointTokens` を維持し、送信する状態と質問を短縮後に再計算する。計数は既存の推定であり、外部サービスの厳密なトークン数を保証するとは扱わない。
目的・ユーザー制約・質問の判定規則・必須IDを削らず、結果本文など短縮可能な部分だけをコピー上で短縮する。質問の規則を無条件に切る現行経路を除く。
配列・構造体を含む状態も実際のJSON構造で扱い、入力値を破壊しない。UTF-8を壊さない。
保護対象だけで予算超過する場合、または一候補でも収まらない場合は送信せず、識別可能なエラー・理由を返す。次ツール判定は既存のローカル経路、圧縮は工程03の保持経路へ戻す。
模擬サーバーで入れ子・多言語・境界値・過大な単一候補・過大な保護対象を検証する。送信した要求は予算内で、収容不能時は外部呼出しゼロ、制約と入力原本は保持されることを確認する。

## files

- `internal/jev/jev.go`
- `internal/jev/jev_test.go`
- `internal/compact/state.go`
- `internal/compact/state_test.go`
- `internal/proxy/rewrite.go`
- `internal/proxy/rewrite_test.go`

## verify

```sh
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./internal/compact ./internal/jev ./internal/proxy
```

## depends_on

03、04
