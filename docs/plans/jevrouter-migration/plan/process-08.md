# Process 08: 各ホストの起動と自動適用を完成させる

## goal

`run`の起動準備から、通常要求を受け取った際の自動判断・適用・結果照合までを五ホストへ接続する。能力一覧と実行器の対応を確認し、手動route呼出しを不要にする。`application_policy=required|fallback`を追加し、requiredでは未配達・未対応・選定不消費を明示エラーにして成功終了を防ぐ。終了時の未回収も表示する。Jev障害時の従来設定への復帰と、採用結果の適用失敗は区別する。

自動適用を新たに有効にした場合はrequiredを既定にし、fallbackは明示指定時だけ使う。新規設定なしの既存起動は保持する。導入例は自動適用有効の設定にし、実際にその設定でホストが到達したことを確認する。

Cursor／Devinは既存本文への追加コンテキストの書戻し、実際のツール呼出し・結果フレームの相関と適用を実装する。実機で取得した機密除去済み通信と対応するホスト版を使って仕様を確認し、存在しないtool_choiceやフックを仮定しない。Claudeの思考・キャッシュ併用、Codexのprevious_response_idなど既存適用制限も契約試験へ入れる。非対応の要求を黙って絞込みだけへ落として完了にしない。実適用を実現できないホストが残れば全体未完了とする。

## files

- `cmd/jev-routing/main.go`、`cmd/jev-routing/main_test.go`
- `internal/host/host.go`、`internal/host/host_test.go`
- `internal/proxy/options.go`、`internal/proxy/proxy.go`、`internal/proxy/rewrite.go`
- `internal/proxy/application.go`、`internal/proxy/application_test.go`
- `internal/proxy/connect_cursor.go`、`internal/proxy/connect_cursor_test.go`
- `internal/proxy/connect_devin.go`、`internal/proxy/connect_devin_test.go`
- `internal/proxy/testdata/application/`（ホスト別の機密除去済み通信、新規）
- `examples/claude.sh`、`examples/codex.sh`、`examples/grok.sh`、`README.md`

## verify

```sh
go test ./cmd/jev-routing -list '^TestRequiredApplicationHosts$' | rg '^TestRequiredApplicationHosts$'
go test ./internal/proxy -list '^TestAutomaticApplicationLifecycle$' | rg '^TestAutomaticApplicationLifecycle$'
go test ./... -count=1
python3 -m pytest /home/takets/repos/private_dotfiles/agents/skills/ateam/tests/test_ateam.py /home/takets/repos/private_dotfiles/agents/skills/agmsg-teams/tests/test_resolve.py
```

上記は模擬契約の受入。加えて各ホストを通常のrun経路で起動し、自動接続仕様の分類別受入表を無害なタスクで実行する。本文供給・実呼出し・終了結果・子の最終返信・親への配達を同じ判断IDで記録する。手動route・選定ログのみ・未対応ホストの除外では合格にしない。ホスト認証等で実機試験ができなければ未検証を残し、対応完了とは報告しない。

## depends_on

01〜07。
