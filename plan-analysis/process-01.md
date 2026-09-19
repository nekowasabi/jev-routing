# Process 01: Jev回答検証と不確実時の復帰

## goal

`askNextTool` が返す固定確信度を実回答の確信度へ置き換え、回答型・必須回答・有限な0〜1の確信度・候補内の一意なツール名を検証する。現在のSDK応答構造と既存の利用箇所を根拠に読み取りを実装する。
次ツールと「今ツールが必要か」を一括質問し、整合する回答だけを採用する。「今ツール不要」をタスク完了と同一視せず、完了判定が必要な既存経路には別の意味として維持する。
介入には選択回答・補助回答それぞれの確信度が0.8以上、補助回答の必要度が0.8以上であることを要求する。必要度0.2以下は今ツール不要、0.2超0.8未満は不確実として追加介入しない。必要度も有限な0〜1だけを受け付ける。境界値とその直前・直後をテストする。0.8は既存の介入閾値に揃える暫定値で、ローカル採点とJev確信度の校正済み同等性や正答率を意味しない。
ローカル判定が閾値を満たす場合の省略は維持する。ローカル確信度不足からJevに進んだ後、低確信度・不正・矛盾・通信失敗になった場合は低確信度のローカル候補に戻して限定せず、全候補で通常上流へ渡す。推論設定も不正回答で変更しない。
`ChoiceOf`・`NoulOf` の他用途、履歴圧縮、既存の候補限定契約を壊さない。`TestAnalysisDecision` に有効回答、型違い、欠落、範囲外、未知候補、重複名、補助回答との矛盾、Jev失敗、ローカル省略の回帰検証をまとめる。

## files

- `internal/jev/jev.go`
- `internal/jev/jev_test.go`
- `internal/proxy/rewrite.go`
- `internal/proxy/rewrite_test.go`

## verify

この工程で追加するテストを存在確認後に実行する。外部APIは使わない。

```sh
go test ./internal/proxy -list '^TestAnalysisDecision$' | rg '^TestAnalysisDecision$'
env -u TYPESAFE_API_KEY -u JEV_API_KEY go test -race -count=1 ./internal/jev ./internal/proxy ./internal/plan ./internal/compact
```

## depends_on

なし
