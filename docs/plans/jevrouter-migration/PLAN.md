# JevRouterの能力選定・モデル自動選択の移植計画

## 目的

六分類（Model / Subagents / Skills / MCP Tools / CLIs / Plugins）を共通の判断契約で扱い、確定可能なローカル処理とJevの意味判断を分ける。各ホストの自動問い合わせから、本文供給・ツール呼出し・子起動・結果照合まで接続する。ateamのモデルと`effort`を項目別に固定／自動選択できるようにする。

[ダッシュボード仕様](../../requirements/jevrouter-dashboard.md)に従い、Jevが使われた場所と未適用の理由、Jev自身の使用量、総トークン・完了時間の比較差をグラフで可視化する。非専門家が一目で状態を読み取れる日本語表示にする。

根拠・仕様・未検証事項は[分析書](../../requirements/jevrouter-migration.md)を正本とする。`ponytail`に従い既存Goクライアント・CLI・統計を再利用する。実装は番号順。各工程の`verify`は実装後に実行する受入コマンドであり、現時点で実装済みとは扱わない。

[自動接続仕様](../../requirements/jevrouter-host-integration.md)の時点・適用先・結果証拠を満たすこと。判断CLI、選定ログ、観測モード、候補絞込みだけでは完了にしない。既存の五ホストそれぞれで適用まで確認する。未知の独自プロトコルは工程08で確認し、未対応が残る間は全体完了を宣言しない。

既存ルートの`PLAN.md`は別計画なので上書きしない。工程06のみ`private_dotfiles`側の独立変更とし、ateamの複製をこのリポジトリへ置かない。

## 対象外

- TypeScriptアプリ全体、別のMCPサーバー、別建ての管理サービス、探索木、AGMSG通信方式の移植。既存ダッシュボードの更新は対象に含む。
- プラグイン自動導入、権限拡大、任意シェル実行、親セッションのモデル自動変更。
- 未計測の節減率の保証、上流の候補全件→再選別方式、無条件の六分類一括照会。

## 工程一覧

| 番号 | 内容 | 依存 |
|---|---|---|
| [01](plan/process-01.md) | ツール判断のローカル確定／保留を分離する | なし |
| [02](plan/process-02.md) | 六分類の共通能力カタログを導入する | 01 |
| [03](plan/process-03.md) | Skills／MCP／CLI／Pluginsを自動選定し、本文供給・呼出し・結果照合へ接続する | 02 |
| [04](plan/process-04.md) | モデルとeffortの有効な組を選定する | 03 |
| [05](plan/process-05.md) | サブエージェントの判断・起動・結果回収を接続する | 03, 04 |
| [06](plan/process-06.md) | ateamの固定／自動選択・重複防止・中断後の結果回収を接続する | 04, 05 |
| [07](plan/process-07.md) | 予算付き一括判断と種類別の観測／適用を完成させる | 01–06 |
| [08](plan/process-08.md) | 五ホストの起動・独自通信・適用必須モードを接続する | 01–07 |
| [09](plan/process-09.md) | ダッシュボード向けの全使用量・適用状態・比較効果を集計する | 07, 08 |
| [10](plan/process-10.md) | Jevの稼働箇所・未適用理由・トークンと時間の効果をグラフ化する | 09 |

## 全体の受入コマンド

`jev-routing`ルートから実行する。将来追加するテスト名は各工程の契約とし、該当テストが存在することも検査する。比較集計用の追加データは工程内で作成する。

```sh
go test ./... -count=1
node --test internal/proxy/assets/dashboard.test.mjs
go test ./internal/proxy -list '^TestDashboardMetrics$' | rg '^TestDashboardMetrics$'
go test ./internal/proxy -list '^TestAutomaticApplicationLifecycle$' | rg '^TestAutomaticApplicationLifecycle$'
go test ./cmd/jev-routing -list '^TestRequiredApplicationHosts$' | rg '^TestRequiredApplicationHosts$'
python3 -m unittest discover -s scripts -p 'test_summarize_*.py'
python3 -m pytest /home/takets/repos/private_dotfiles/agents/skills/ateam/tests/test_ateam.py /home/takets/repos/private_dotfiles/agents/skills/agmsg-teams/tests/test_resolve.py
python3 scripts/summarize_selection_benchmark.py scripts/testdata/capability-routing
```

模擬テスト成功だけで実ホストへの自動適用を完成扱いしない。各ホストへ通常の依頼を渡し、手動の判断CLIなしで選定対象が適用される実環境試験を必須にする。完了証拠は自動接続仕様の表に従う。さらに種類別の反復比較で品質・候補保持率を落とさず総トークンと完了時間が改善した範囲を評価する。性能が未達ならその事実を残し、機能接続の完了と区別する。

## 計画自体の検証

```sh
python3 /home/takets/.codex/skills/make-plan/tests/check-process-fields.py docs/plans/jevrouter-migration
git diff --check
```

作成時点では実装未着手。計画の構造検証と現行Goテストを実行した結果を分析書と最終報告に区別して残す。
