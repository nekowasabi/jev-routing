# Claude／Codex のツール選択・履歴圧縮の修正と検証

対象: [障害解析](test-x-cell-failure-analysis.md) の Claude、Codex、および両者の受け入れ判定。Cursor／Devin／Grok の追加統合は対象外。

## 確認した原因と修正

- Codex の実要求には最上位の `tools` がなく、`input` の `additional_tools` にカタログがあった。抽出と書き戻しをこの形式へ対応し、名前空間と元の配置を維持する。
- Codex の `exec` 後に `-c` を追加すると、手前のプロキシ用設定が失われるケースを実CLIで再現した。同一バイナリで推論量 `low` を `exec` 前へ置くと `POST /v1/responses` と Jev 選択が復帰した。起動時に設定引数を同じ階層へ集約する。
- Jev の Noul に存在しない独立した `confidence` を要求していたため、実応答を常に不確実として拒否していた。Noul の確率と Choice の信頼度をそれぞれ評価する。欠損した数値をゼロと誤認しない。
- Claude の `thinking`、`redacted_thinking`、`tool_reference` を未知の履歴として拒否していた。これらを認識し、署名と参照を保持する。
- 圧縮による空メッセージ削除が `system` 境界や呼び出し・結果の対応を壊せた。保持が必要な組を保護し、その他の古い結果の削除・短縮は継続する。
- 圧縮適用がツール選択の成立に依存していた。安全な圧縮を選択から分離し、実際の適用差分を計測する。
- 次ツールの質問に Claude 固有の指示があり、候補説明も途中で切断していた。実際の候補の能力説明に基づく質問へ変更する。
- 自己申告の完了文字列でライブ試験を判定していた。外部の期待値照合、作業ツリー非変更、CLI 終了値、適用イベントと上流成功応答を合格条件にする。

## 受け入れ条件

1. Claude と Codex の両方が複数回のツール呼び出しを行い、正常終了する。
2. 既知の関数定義位置を外部で算出し、最終回答が全項目で一致する。
3. ファイルを変更しない。
4. ツール選択の適用と、圧縮の実差分がそれぞれ存在する。
5. それぞれの適用イベントに上流の HTTP 2xx と応答完了が対応する。
6. 条件未達はハーネスの非ゼロ終了値へ反映する。
7. Codex は `gpt-5.6-terra`、推論量 `low` で比較元と Jev 経由をそろえる。

実要求本文・引数・認証情報は計測しない。保存する構造情報はキー、型、件数、抽出位置のみ。選択元、適用方式、Jev 呼び出し目的、上流 HTTP 応答を実行ごとに保存する。

ハーネスは現在の作業ツリーからプロキシをビルドする。一方、エージェントが調査するコードは比較元・Jev 経由で同じ `HEAD` の一時作業ツリーを使い、起動前に期待回答を算出する。試験中に製品コードやハーネスは編集しない。

## 再実行

```sh
go test -race ./...
python3 -m unittest discover -s scripts -p test_summarize_x_cell.py
bash -n scripts/test-x-cell.sh
CODEX_MODEL=gpt-5.6-terra JEV_COMPACTION=on JEV_REASONING=preserve bash scripts/test-x-cell.sh claude codex
```

Codex CLI には `--effort` がないため、`--model gpt-5.6-terra -c 'model_reasoning_effort="low"'` を使う。Codex の試験では `JEV_REASONING=preserve` も指定し、プロキシが推論量を変更しないようにする。Claude の `legacy` は別の検証条件。圧縮を `off` にした合格は選択単独の証拠であり、両機能の完了とは扱わない。

## Claude 再検証で判明した追加不具合と修正

初回合格後の [再試験](../../artifacts/x-cell/20260919T205116-246181/claude/comparison.json) は不合格になった。Jev 経由は `Read → Bash → Bash` に絞られ、「検索ツールが使えない」と回答し、圧縮も適用されなかった。HTTP 200・CLI 終了値 0 だけでは成功としない外部検証が、この失敗を検出した。

追加修正:

- `ToolSearch`、提供側のツール検索、`defer_loading:true` の定義を通常候補と区別して保持する。実 Claude CLI の `DeferredToolPlaceholder` もこれで残る。
- 履歴の `tool_reference` が参照する非遅延ツールも保持し、名前空間の再構築時にも必要な定義を落とさない。
- 判断用の依頼から閉じた `system-reminder` を除外する。通知だけの後続メッセージで実依頼を上書きせず、上流へ送る原文は変更しない。

回帰テストで、修正前の発見用定義消失と補助通知による誤分類を再現した。修正後の実要求でも `ToolSearch` と `DeferredToolPlaceholder` の存在、補助通知の混在を本文非保存のメタデータで確認した。失敗ランの本文は保存していないため、過去の個別ブロックまで同定したという意味ではない。

同じビルド、`filter / compaction=on / reasoning=preserve` で、Claude は次の3回すべて合格した。各回、比較元・Jev 経由とも正答全件一致、作業ツリー非変更、CLI 終了値 0。Jev 経由の全12要求は HTTP 200・応答完了。

| 実行記録 | 選択適用 | 圧縮適用 | Jev なし | Jev あり |
| --- | ---: | ---: | ---: | ---: |
| [210015-284094](../../artifacts/x-cell/20260919T210015-284094/claude/comparison.json) | 9 | 8 | 26.10秒 | 41.45秒 |
| [210134-293706](../../artifacts/x-cell/20260919T210134-293706/claude/comparison.json) | 9 | 8 | 27.20秒 | 59.69秒 |
| [210304-304049](../../artifacts/x-cell/20260919T210304-304049/claude/comparison.json) | 9 | 8 | 37.87秒 | 31.81秒 |

機能面の反復検証は合格。実行時間は2回増加・1回短縮で、一貫した高速化は確認していない。これは試験条件内の証拠であり、すべての依頼での成功保証ではない。

`go test -race ./...` は全パッケージ成功。追加の `claude_discovery_test.go`、`work_request_test.go`、`user_request_extraction_test.go` で、発見用定義・参照定義・名前空間・判断対象の抽出・上流原文不変を確認した。

```text
ok  	github.com/nekowasabi/jev-routing/internal/proxy	1.338s
```

現ビルドの SHA-256: `f430a7ba9753d90920dd7948e487cb71e46e40cdf6ae18a17b50276580ce94c2`。共通処理変更後の [Codex 回帰試験](../../artifacts/x-cell/20260919T210426-313126/codex/comparison.json) も `gpt-5.6-terra`／`low` で合格した。比較元・Jev 経由とも CLI 終了値 0、全回答一致、作業ツリー非変更。選択と圧縮が適用され、全推論要求が HTTP 200・応答完了。

## 初回の実行結果（上記の追加修正前）

2026-09-19、既定の `filter` モード・圧縮有効で、以下のホスト別受け入れ条件をすべて確認した。

| ホスト | 推論要求数 | 選択適用数 | 圧縮適用数 | 上流応答 | 外部品質 |
| --- | ---: | ---: | ---: | --- | --- |
| Claude | 13 | 2（ローカル判定） | 9 | 全13件 HTTP 200・応答完了 | 比較元・Jev 経由とも合格 |
| Codex | 16 | 15（Jev 判定） | 14 | 全16件 HTTP 200・応答完了 | 比較元・Jev 経由とも合格 |

両ホストとも、比較元・Jev 経由の CLI 終了値は 0、期待回答全件一致、作業ツリー非変更。Jev API の失敗は両者とも 0。Codex は両条件とも `gpt-5.6-terra`／`low` で、全要求の送信モデルも同一、推論設定は `preserve`。

根拠:

- [Claude の比較結果](../../artifacts/x-cell/20260919T203534-166349/claude/comparison.json)、[要求別イベント](../../artifacts/x-cell/20260919T203534-166349/claude/jev/proxy.json)。この実行の修正前 Codex は不合格だったため、次の Codex 単独試験を最終証拠とする。その後の製品変更は Codex の起動引数と要求経路の計測で、Claude の圧縮処理は同一。
- [Codex の最終比較結果](../../artifacts/x-cell/20260919T204201-209399/codex/comparison.json)、[要求別イベント](../../artifacts/x-cell/20260919T204201-209399/codex/jev/proxy.json)。`CODEX_MODEL=gpt-5.6-terra JEV_COMPACTION=on JEV_REASONING=preserve bash scripts/test-x-cell.sh codex` は終了値 0。
- 当時のバイナリ `bin/jev-routing` の SHA-256: `01bde8160a3c4fcbcb243fedbccc9cc3a2266f4144985cc7122fa56b092bd9c9`。

全変更後に `go test -race ./...`、Python のハーネス回帰テスト、`bash -n scripts/test-x-cell.sh`、`git diff --check` を実行し、すべて終了値 0。

```text
ok  	github.com/nekowasabi/jev-routing/cmd/jev-routing	1.021s
Ran 12 tests in 1.282s
OK
```

回帰テストは、Claude の system 前後境界・署名付き思考・ツール参照、呼び出しと結果の対応、Codex の埋め込みカタログ・独自呼び出し入力・配列結果、Noul の正式応答形式・数値欠損、選択不成立時の圧縮、起動引数の位置、計測への本文非混入、上流拒否の誤合格防止を検証する。

これはツール候補を絞る `filter` モードの動作確認であり、`forced`／`direct` のライブ動作や全ホストの性能改善を主張するものではない。

## 仕様の根拠

- [Codex 公式 Responses Lite テスト](https://github.com/openai/codex/blob/main/codex-rs/core/tests/suite/responses_lite.rs): `input[0].additional_tools` と最上位 `tools` の省略。
- [TypeSafe Noul](https://docs.typesafe.ai/primitives/noul): 独立した信頼度を返さず、真である確率を返す。
- [TypeSafe Choice](https://docs.typesafe.ai/primitives/choice): 候補と信頼度を返す。
- [Claude の途中 system メッセージ](https://platform.claude.com/docs/en/build-with-claude/mid-conversation-system-messages): 配置制約。
