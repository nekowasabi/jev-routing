# test-x-cell の書換え失敗・比較不能の原因解析

調査日: 2026-09-19。対象は `20260919T192559-66286`、実行時コミット `04c441a`。現在の `main` (`a48435b`) と対象コミットの追跡ファイルには差分がない。保存ログ、実装、既存テスト、インストール済み CLI の通信実装を照合した。製品コードの修正と課金を伴う再計測は行っていない。

## 結論

失敗は一種類ではない。非 Claude は **Jev の選択処理に到達する前** に止まっており、全て `jevHTTP=0`。Claude は書換え後の会話履歴が API に拒否されている。さらにハーネスは「計測が終わった」と「受け入れ条件を満たした」を終了値で区別していない。

「どのエージェントもツールを使うので、同じ JSON フィルターで置き換えられる」という前提は成立しない。置換の可否は、ツール候補と選択要求が、介入できる通信区間にどの形式で存在するかに依存する。特に Cursor / Devin では、エージェントの RPC とモデルの推論要求を区別する必要がある。

| ホスト | 保存データで確定した状態 | 原因・到達限界 |
|---|---|---|
| Claude | 到達14、書換え14、Jev HTTP12、終了1 | 会話履歴の `system` 配置違反による HTTP 400。圧縮が同じ配置違反を作れることは再現済み。当該要求の変形前後は未保存 |
| Codex | 到達8、書換え0、Jev HTTP0 | 抽出後のツール名が0になり `no-catalog` で早期終了。元の `tools` が空だったかは未確定 |
| Grok | 到達13、書換え0、Jev HTTP0 | 8要求で認識済み26名に加えて未知形式を含み、カタログ全体を拒否。残りは `not_chat` 4、明示的なツール指定1 |
| Cursor | 到達41、書換え0、Jev HTTP0 | ログの22要求は制御用 JSON と整合する `not_chat`。実 CLI は protobuf を含む RPC を使用し、想定した `mcpTools` JSON と通信形式が異なる |
| Devin | 計測対象への到達0、書換え0、Jev HTTP0 | 実 CLI の推論 RPC パスが `looksLikeLLM()` の対象外。到達0だけでは、プロキシを迂回したとはいえない |

集計元: [対象ランの成果物](/Users/takets/repos/jev-routing-live-xcell-20260919/artifacts/x-cell/20260919T192559-66286)。各ホストの `jev/proxy.json` と `comparison.json` を再集計した。全ホストで `valid=false`。共有ログの件数は実行時刻とホストで照合しており、要求ごとの実行 ID での対応付けではない。

## 1. Claude: 圧縮後の会話履歴を壊す経路がある

直接の失敗理由は [raw.json](/Users/takets/repos/jev-routing-live-xcell-20260919/artifacts/x-cell/20260919T192559-66286/claude/jev/raw.json:1) に保存されている。

```text
api_error_status: 400
terminal_reason: api_error
messages.33: role 'system' must follow a 'user' message or an 'assistant' message ending in a server tool result
```

[applyCompactToMessages()](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:1190) は、削除判定されたツール呼出し・結果を消す。結果として `content` が空になったメッセージは [1273行](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:1273) で丸ごと消すが、残るメッセージの役割順序を検査しない。

診断用テストで、以下の変形を現行関数に対して再現した。

```text
入力: assistant(本文 + tool_use) → user(tool_result) → system
削除: tool_use と tool_result の組
出力: assistant(本文) → system
```

この変形は、ログのエラーが示す前置条件を壊す。関数が `system` を追加したわけではなく、既存の `system` の直前を変えている。

**確定:** API 拒否の理由、および現行圧縮処理が同じ違反を作れること。**未確定:** 実ランの33番目でこの削除が原因だったか。実際の要求本体と削除判定は保存されていないため、ここは有力仮説である。[共有ログ](/Users/takets/Library/Caches/jev-routing/run.log:5838) の14要求は全て `Bash` に絞られており、最終要求にも圧縮量が記録されている。

推奨修正: ツールの組だけでなく、ホストのメッセージ順序制約を保存する。制約を壊す圧縮は適用しない。切り分け時は `JEV_COMPACTION=off` と推論設定維持を使い、カタログ絞り込み単独で完走するかを測る。環境変数の正式な値は [options.go](/Users/takets/repos/jev-routing/internal/proxy/options.go:43) に合わせる。

## 2. Codex: `no-catalog` を「元の tools が空」と読んだのは誤り

[共有ログ](/Users/takets/Library/Caches/jev-routing/run.log:5852) は8件とも `tools 0→0 chosen=passthrough:no-catalog`。これは [RewriteWith()](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:115) が抽出・展開・対象選別をした**後**、[131行](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:131) で名前が0件だったことを示す。

元配列の存在・長さ・型・名前空間の子フィールドを記録していないため、次を区別できない。

- 元のカタログが存在しない、または空。
- 抽出対象と異なるフィールド・型に定義されている。
- 名前空間の展開により定義が抽出されなかった。

診断用の別形式入力では、元の `tools` が非空でも同じ `no-catalog` になることを再現した。この入力はログの意味を検証するための人工例であり、実 Codex の通信形式を再現したものではない。

推奨修正前の確認: 実要求のパス、トップレベルのキー、`tools` の型・件数、各定義の `type` と子フィールド名、展開前後件数を記録する。その結果で [extractTools / flattenCatalog](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:676) の不足か、カタログを伴わない別要求を見ているのかを決める。保存ログだけで抽出フィールドを決め打ちする根拠はない。

## 3. Grok: 正常な26名を取得できても、未知の1項目で全体を拒否する

[共有ログ](/Users/takets/Library/Caches/jev-routing/run.log:5862) の `tools 26→26` は、[plan.ToolNames()](/Users/takets/repos/jev-routing/internal/plan/plan.go:386) で名前を抽出できた数である。「26個の名前を取れなかった」という以前の解釈は逆だった。

その後の [catalogReason()](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:456) は、非オブジェクトまたは名前を取れない項目が一つでもあると `unrecognized_format` を返す。これで正常な定義も含めて全体が無変更で通過する。

[isProviderExecuted()](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:494) は既知の型の列挙で、例えば `x_search` は含まれない。インストール済み Grok のバイナリには `x_search` に関する文字列があるが、今回の未知項目がそれだったとまでは確定できない。

[追加テスト](/Users/takets/repos/jev-routing/internal/proxy/rewrite_test.go:931) が確認しているのは、通常の関数と既知の `web_search` の混在であり、実ランで拒否された項目を含んでいない。

推奨修正: 実要求の未知項目の型を確認し、実行主体と保持条件が分かるものを適切に保存しつつ関数候補を絞る。名前のない項目を無条件に削除する修正は、提供側のツールを壊すため不適切。

## 4. Cursor: 通信方式の層が違う

インストール済み CLI の [index.js](/Users/takets/.local/share/cursor-agent/versions/2026.09.18-9a7762b/index.js:8) では、`RunSSE` へ要求 ID を送り、`aiserver.v1.BidiService/BidiAppend` に protobuf の内容を `dataBinary` または16進文字列の `data` として送る経路がある。現プロキシは HTTP 本体を直接 JSON の会話として解釈するため、外側が JSON でも内側のエージェント要求を解釈できない。

[JSON の入口](/Users/takets/repos/jev-routing/internal/proxy/proxy.go:272) と [会話判定](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:327) は、そのフレームや内包データを復号しない。これが `not_chat` と無変更通過に整合する。集計41件と文字ログ22件の差は、JSON として処理されず詳細文字ログを残さない経路でも生じる。19件全部が protobuf だったという断定はできない。

さらに、実 CLI の [AgentRunRequest](/Users/takets/.local/share/cursor-agent/versions/2026.09.18-9a7762b/index.js:414) の `mcp_tools` は MCP の定義であり、組込みツール全体の `tools[]` と同一である証拠はない。[現テスト](/Users/takets/repos/jev-routing/internal/proxy/rewrite_test.go:975) は MCP 配列に `Read/Grep/Shell/Write` が直接入る人工例である。

推奨修正: 実際のフレーム・要求型を識別し、組込みツールの候補が介入できる場所に存在するかを先に確認する。protobuf 対応だけで Claude と同じ選択制御が成立するとは限らない。サーバー側でカタログを作る構成なら、その選択点かホスト側の別の拡張点への統合が必要になる。

## 5. Devin: 到達カウンターの定義に盲点がある

[reached の加算](/Users/takets/repos/jev-routing/internal/proxy/proxy.go:242) は `POST && looksLikeLLM(path)` の内側だけ。プロキシが受けた全通信を数えていない。

インストール済み [Devin バイナリ](/opt/homebrew/Caskroom/devin-cli/3000.10.21/bin/devin) を `strings` で確認すると、以下の経路と形式が存在する。

```text
/exa.api_server_pb.ApiServerService/GetChatMessage
/exa.api_server_pb.ApiServerService/GetDevstralStream
application/proto
DEVIN_API_URL
```

これらのパスは [looksLikeLLM()](/Users/takets/repos/jev-routing/internal/proxy/proxy.go:374) の列挙に一致しない。そのため、当該 RPC がプロキシ経由でも、到達0・書換え0になり得る。

**確定:** 計測・書換えの対象パスがこの RPC を網羅しないこと。**未確定:** 対象ランで実際に通った全 URL。全要求のログがないため、別の迂回経路の有無までは排除できない。

推奨修正: 全通信の到達数と推論要求の到達数を分け、実 RPC のパスと形式を確認する。パスを追加しても protobuf は現行 JSON 処理では書き換えられないため、通信先設定だけの修正では足りない。

## 6. ハーネスの完走と、置換の成功を混同していた

[test-x-cell.sh:58](/Users/takets/repos/jev-routing/scripts/test-x-cell.sh:58) は `set +e` で子プロセスの失敗を許容し、終了値を保存した後も進む。[最後](/Users/takets/repos/jev-routing/scripts/test-x-cell.sh:165) は `echo` で終わるため、全比較が無効でも `make` は0で終了できる。

さらに [比較条件](/Users/takets/repos/jev-routing/scripts/test-x-cell.sh:141) は観測・書換え・`CHECK: PASS` の文字列を使い、`exit_code == 0` を直接要求しない。`CHECK: PASS` 自体は [プロンプト](/Users/takets/repos/jev-routing/scripts/test-x-cell.sh:33) がモデルに出力を要求した自己申告である。

[summarize_x_cell.py の external_quality()](/Users/takets/repos/jev-routing/scripts/summarize_x_cell.py:40) は終了値・作業ツリー非変更・期待解答を確認するが、ライブ用シェルの集計はこれを使用していない。フィクスチャの検証成功を、そのままライブの品質検証成功とみなすことはできない。

推奨修正: 計測完走と受け入れ合格を分ける。受け入れ用途では、終了値・外部検証・目的の書換え適用を合格条件にし、未達を終了値にも反映する。

## 7. 今回の実験は「直接置換」の試験ではない

各 `proxy.json` の実設定は `mode=filter / compaction=on / reasoning=legacy`。`filter` は上流へ渡すツール候補の絞り込みであり、上流モデルの引数生成・実行判断の全てを代替するものではない。

[forced の条件](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:245) と [direct の条件](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:281) は別であり、後者は限定された許可ツールなどの条件を満たす必要がある。また、[ローカル判定の信頼度が高い場合](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:178)、ツール選択に Jev API を呼ばない。

[engine=live](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:95) はクライアントが利用可能な設定という意味で、実際の選択を Jev が行った証明ではない。`jevHTTP` にも圧縮向けの呼出しが含まれるため、選択の置換を確認するには `source / apply / purpose` が必要。

## 次の修正順序

1. **計測の意味を正す。** 全要求と推論要求の到達数、拒否理由、選択元、適用方式、HTTP 応答を実行ごとに保存し、受け入れ判定を外部検証へ接続する。
2. **Claude の履歴制約を守る。** 再現した圧縮の削除ケースを防ぎ、絞り込み単独と圧縮併用を分けて確認する。
3. **Codex / Grok の実形式に合わせる。** キー・型・件数だけを最小限記録して実フィクスチャ化する。本文、引数、認証情報は記録しない。
4. **Cursor / Devin の統合点を確定する。** RPC を復号しても候補一覧が得られるとは限らないため、カタログと選択処理の所在を確認してから対応する。
5. **ホストごとの最小事例が通ってから再計測する。** 全ホストの長時間再実行だけでは、未対応分岐の原因は増えて見えない。

## 補足: エージェントごとに Jev API を変える必要があるか

今回の証拠から API の接続先やモデル自体を分ける必要性は導けない。必要なのは、各ホストの要求を読み取って共通の判断入力へ変換し、選ばれたツールを各ホストの定義へ戻す処理である。候補の意味・権限・実行可能性は保持する必要があり、似た名前のツールを無条件に同一視してはいけない。公式仕様でも、[state は判断対象の情報](https://docs.typesafe.ai/concepts/state)、[Choice は提示された候補からの選択](https://docs.typesafe.ai/primitives/choice) とされる。

[askNextTool()](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:887) は現在も共通 API を呼び、候補名と説明を `criteria`、ユーザーの依頼と実行履歴を `state` に渡す。ホストが変わると候補や説明、履歴も変わり得るため、同じ API でも結果が変わる可能性はある。ただし今回の非 Claude は API 呼出し前に止まっており、分類品質のホスト間差を測れていない。

入力設計の別の問題として、[共通の質問文](/Users/takets/repos/jev-routing/internal/proxy/rewrite.go:904) に Claude Code の `Agent/Task` を広範な探索・並列作業で選ぶ指示が固定されている。ホスト固有の能力説明を共通判断へ混ぜているため、API を分けるより、実際の候補の説明から能力を伝えるか、利用可能なホストでのみその補足を付ける方が筋が通る。この指示の実際の精度影響は未測定であり、今回の書換え0の原因ではない。

設計方針は **共通の Jev API ＋ ホスト別の入出力変換**。その上で、同じ作業目的を満たす候補を選べているかをホスト別に評価し、必要な質問文や閾値の調整を実測から決める。

## 検証と限界

実行した既存テスト:

```sh
go test ./internal/proxy ./internal/host ./internal/plan ./internal/compact
python3 -m unittest discover -s scripts -p test_summarize_x_cell.py
```

出力からの引用:

```text
ok  	github.com/nekowasabi/jev-routing/internal/proxy	0.779s
Ran 8 tests in 0.001s
```

診断用コードは製品ツリーへ加えず、Go のオーバーレイで現行関数を検査した。

```sh
go test -overlay /tmp/jev-xcell-analysis.NzkF1W/overlay.json ./internal/proxy -run TestDiagnostic -v
```

```text
REPRODUCED: compaction removes the user predecessor and leaves assistant(text) -> system
REPRODUCED: no-catalog can occur despite a nonempty raw tools array
```

[診断コード](/tmp/jev-xcell-analysis.NzkF1W/diagnostic_test.go)。このテストの成功は「障害になり得る挙動を再現できた」という意味であり、修正済みの意味ではない。

生リクエストは保存されていない。したがって、Codex の実フィールド、Grok の未知項目、Claude の失敗要求の変形前後、Cursor / Devin の当該実行の全経路は未確定。ログだけで断定できない点を、モデル性能や単なる遅延に帰属させてはいけない。

## Completion Summary

- 変更ファイル: 本解析文書のみ。製品コードは未変更。
- 実施: 保存ログの再集計、実行時コードとの照合、CLI 通信実装の確認、既存テストとオフライン再現。
- 未完了: 製品修正、実形式の最小キャプチャ、修正後のライブ再計測。
