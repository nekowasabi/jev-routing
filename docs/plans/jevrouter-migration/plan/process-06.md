# Process 06: ateamの自動選択・起動・結果回収を接続する

> 2026-09-23 更新: agmsg 1.4.0 の jev 対応により、ateam の model/effort 自動選択（auto）と `jev-routing route --json` を撤去した。本書の該当記述は履歴として残す。

## goal

この工程だけは`private_dotfiles`側に実装する。名簿と既存パターンに任意の`routing.model`／`routing.effort`（fixed/auto）、`routing.catalog`、`routing_timeout_ms`を追加する。未指定はfixed。項目別の優先順位は分析書の契約どおりにし、共有名簿の解決器から新規項目を渡す。`single`のautoと`spawn`の任意モード指定を実装し、`spawn_member`を呼ぶ前に判断CLIへ照会する。チーム対象は一括入力できる形にする。推論失敗時は従来値、設定構文エラーは明示エラー。実行状態に指定値・適用値・復帰理由を保存する。AGMSG本体・Grok既存baseline対策・役割・人数・ホストを維持する。

自動委譲入口は`dispatch_id`と確定済み分担を受け、既存begin_run/spawn_memberへ接続する。プロセス間排他と起動前の状態保存で同じIDの再送を既存runへ結び付ける。close後も終端記録は保持する。既読状態に依存せず既存の機械可読履歴から返信を回収し、メッセージIDと分担IDで照合する。進捗・最終結果・失敗を区別し、親へ結果を戻す。起動失敗した必須役割を除いて全体成功にしない。承認済み範囲と事前受入条件を引き継ぎ、子の返した任意コマンドを検証として実行しない。

## files

- `/home/takets/repos/private_dotfiles/agents/skills/ateam/scripts/ateam.py`
- `/home/takets/repos/private_dotfiles/agents/skills/ateam/patterns.yaml`
- `/home/takets/repos/private_dotfiles/agents/skills/ateam/tests/test_ateam.py`
- `/home/takets/repos/private_dotfiles/agents/skills/ateam/SKILL.md`
- `/home/takets/repos/private_dotfiles/agents/skills/agmsg-teams/scripts/resolve.py`
- `/home/takets/repos/private_dotfiles/agents/skills/agmsg-teams/tests/test_resolve.py`
- `/home/takets/repos/private_dotfiles/agents/skills/agmsg-teams/SKILL.md`

既存の実名簿は一律autoへ変更しない。任意設定例と偽カタログを既存テストへ入れ、実運用の適用は工程07の評価対象とする。

## verify

```sh
python3 -m pytest /home/takets/repos/private_dotfiles/agents/skills/ateam/tests/test_ateam.py /home/takets/repos/private_dotfiles/agents/skills/agmsg-teams/tests/test_resolve.py
```

偽の判断CLIとspawn.shで、固定時の要求数0、単体・チーム、片側固定、固定モデル未指定・固定effort未指定の双方での復帰、パターンへの復帰、名簿優先、相対カタログパス、欠落CLI・時間切れ・不正出力、実際の起動引数、汎用オプションとの二重指定防止を検査する。既存Grokテストも通す。スキル文書の変更は正本だけに行い、実装時にスキル編集手順の検証も行う。

同一dispatch_idの逐次・同時再送、同一IDの別入力、起動直後の中断、返信既読化後の再開、別実行の返信、partial起動、close後の再送を追加検証する。起動・返信受信・親への配達・成果検証を分け、起動回数と親へ配達した結果で確認する。

## depends_on

04、05。
