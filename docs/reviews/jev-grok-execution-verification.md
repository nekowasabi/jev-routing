# Grokによる再現検証

2026-09-19。`ateam single grok grok high` により、Grok Buildを `grok-4.6 / high` で起動して実行検証した。対象は `24c7276` と現在の未コミット変更。元の実装は修正していない。

## 結論

**前回の主要な指摘は再現した。Claude Code・Grok Build共通の並列結果対応と、Grok固有のツール名対応は、最適化を広げる前に修正・検証が必要である。**

Grokが隔離コピーに作成した、正しい動作を期待する追加テストは13件失敗した。親セッションもテスト内容を確認して再実行し、同じ失敗を確認した。これは13個の独立した不具合という意味ではなく、下記の問題を複数の入力と処理段階で検査した結果である。

## 再現した内容

| 問題 | 期待する動作 | 実際の動作・影響 |
|---|---|---|
| GrokのBash名 | `run_terminal_command` に対応 | `run_terminal_cmd` になる。`run the tests` のローカル選定は目的のツールを限定できず、全カタログを通過させる |
| GrokのAgent名 | `spawn_subagent` に工程表から対応 | 対応表は `task`。別の採点処理で `spawn_subagent` を選べても確信度が低く、外部判定を省ける経路から外れる |
| Claude形式の並列結果 | GrepとReadそれぞれに正しい結果を対応付ける | Grepの結果が空になり、ReadにGrepの本文が入る |
| Grok形式の並列結果 | `tool_call_id` ごとに結果を対応付ける | `grep` の結果が空になり、`read_file` にgrepの本文が入る |
| 未知の呼出しID | 既知の呼出しの結果として採用しない | 未知IDの本文が既知の呼出しへ紐づく |
| Codex Responses履歴 | `function_call` / `function_call_output` を認識する | 行動履歴が空になり、grep・read後も最初のgrepを次の候補として選ぶ |
| 目的変更時の圧縮キャッシュ | 目的に依存する判定を再評価する | 同じ質問IDの判定が再利用され、模擬Jevへの呼出し回数が増えない |
| Jevの入力予算 | 入れ子の結果を含め、設定予算内に収める | `actions_taken.Result` が十分に短縮されず、実装内の見積りで予算超過が残る |

並列結果対応で再現したのは、**Jevへ渡す行動状態の誤り**。この検証から、上流LLMへ転送するすべての結果本文まで交換されるとは結論しない。

Grokの名前不一致も「Grokが全面的に停止する」という意味ではない。全カタログ通過や別の採点・外部判定に回ることで動く場合があり、問題は期待する工程選択と削減経路が働かないことである。

## 検証ファイルと再実行方法

隔離コピー: `/Users/takets/repos/jev-routing-review-verify-20260919`

- [ツール名・工程選択のテスト](/Users/takets/repos/jev-routing-review-verify-20260919/internal/plan/review_verify_isolation_test.go)
- [並列結果・Responses・書き換えのテスト](/Users/takets/repos/jev-routing-review-verify-20260919/internal/proxy/review_verify_isolation_test.go)
- [入力予算・目的変更時のキャッシュのテスト](/Users/takets/repos/jev-routing-review-verify-20260919/internal/jev/review_verify_isolation_test.go)

```bash
cd /Users/takets/repos/jev-routing-review-verify-20260919
go test -count=1 -json ./internal/plan ./internal/proxy ./internal/jev -run TestReviewVerify
```

親セッションの再実行・集計出力:

```text
隔離コピーの既存Goソース/go.mod差異: []
exit: 1 失敗テスト: 13 成功テスト: 0
```

代表的な失敗出力:

```text
Native(Grok,Bash)="run_terminal_cmd" want run_terminal_command
actions=[] want grep_files=GREP_BODY read_file=READ_BODY
chosen="grep_files" tools 4→1 want apply_patch after grep_files+read_file
FitInput left joint=33427 > budget=32000 (nested actions_taken.Result not clipped)
goal change reused compact cache; Jev calls first=1 second=1 want second>first
```

隔離コピーの既存Goソースと `go.mod` は元の作業ツリーとバイト比較して一致した。失敗は修正前のコードで再現したもので、診断用コードを追加して単に成功させた結果ではない。ただし、工程選択の確信度に関する期待値は現在の設計を前提とする検査であり、上流サービスの保証ではない。

Grokは元リポジトリで `go test -count=1 ./...` も実行し、既存テストは終了コード0と報告した。追加テストの失敗との違いは、既存テストに今回の入力・期待値が含まれていないことによる。

## 既存測定の解釈で補足する点

Grokの旧比較記録は、プロキシ通過と本文削減を記録している一方、`valid=false` である。今回の確認で、出力本文に期待する完全な文字列 `CHECK: PASS` がなく、`CHECK:KB知見を注入 PASS` のような文字列になっていることも分かった。

したがって、正確な表現は **「ベンチマークの完了判定を満たしていない」** である。このフラグだけから、実際の調査成果がすべて誤っていた、またはその失敗が単一スキーマ化に起因したとまでは断定できない。成果物の正しさを別に検査する必要がある。

根拠: `artifacts/x-cell/20260919T121437-47149/grok/comparison.json` と `grok/jev/raw.json` の `text`。親セッションも元JSONを確認した。

また、`engine=live` はJevクライアントの設定状態を示し、実際のJev呼出し回数ではない。当該比較のJev実呼出し回数は未確認のままである。

## 利用可能といえる範囲と未確認事項

- Claude Code・Grok Build向けのJSON履歴処理と接続設定はある。過去の記録では両者のプロキシ通過を確認できる。
- 並列結果の行動状態は両者で誤りを再現した。GrokのBash・Agent名対応にも、現在確認したカタログとの不一致がある。
- 今回の新規実行はローカルテストと模擬HTTPサーバーによる検証。Grok/Claudeの実サービスで通常・Jev経由を再比較する試験は実施していない。
- 現行コードの実サービス上の完了品質、削減率、時間短縮、ホストの承認・待機・再試行フックとの接続は未検証。
- カタログ名は確認したGrok環境・既存ログに基づく。他のGrokバージョンや全ホストへの一般化はしない。

## Completion Summary

- 実施: 指定どおりGrok単体を `high` で起動し、隔離環境で再現検証。親セッションがテスト内容と既存ソースの一致を確認し、追加テストを追試した。
- 変更: 本報告と隔離環境の検証用ファイルのみ。元の実装・既存テスト・測定スクリプトは変更していない。検証開始時からのハッシュ一致も確認した。
- 終了: `ateam.sh close single` を実行し、`despawned single team=review` を確認。他のレビューチームには操作していない。
- 未実施: 不具合修正、実サービス再比較、コミット・プッシュ。
