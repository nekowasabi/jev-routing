// jev_stress sends frequent real requests to Jev for dashboard inspection.
//
// Usage:
//
//	TYPESAFE_API_KEY=... go run ./test-jev
//	JEV_BASE_URL=http://127.0.0.1:1 TYPESAFE_API_KEY=... go run ./test-jev # failures
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

const defaultURL = "https://api.typesafe.ai/v1/systemone"

func main() {
	duration := flag.Duration("duration", 5*time.Minute, "実行時間")
	interval := flag.Duration("interval", 3*time.Second, "リクエスト間隔")
	timeout := flag.Duration("timeout", 20*time.Second, "各リクエストのタイムアウト")
	failEvery := flag.Int("fail-every", 7, "この回数ごとに意図的に失敗させる。0で無効")
	flag.Parse()

	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		key = os.Getenv("JEV_API_KEY")
	}
	if key == "" {
		fmt.Fprintln(os.Stderr, "TYPESAFE_API_KEY または JEV_API_KEY が必要です")
		os.Exit(2)
	}
	url := os.Getenv("JEV_BASE_URL")
	if url == "" {
		url = defaultURL
	}
	model := os.Getenv("JEV_MODEL")
	if model == "" {
		model = "jev-latest"
	}

	client := &http.Client{}
	deadline := time.Now().Add(*duration)
	for n := 1; time.Now().Before(deadline); n++ {
		requestURL := url
		if *failEvery > 0 && n%*failEvery == 0 {
			requestURL = "http://127.0.0.1:1/intentional-dashboard-failure"
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		err := call(ctx, client, requestURL, key, model, n)
		cancel()
		if err != nil {
			fmt.Printf("%s #%d 失敗: %v\n", time.Now().Format(time.RFC3339), n, err)
		} else {
			fmt.Printf("%s #%d 成功\n", time.Now().Format(time.RFC3339), n)
		}
		if remaining := time.Until(deadline); remaining > 0 {
			time.Sleep(min(*interval, remaining))
		}
	}
}

func call(ctx context.Context, client *http.Client, url, key, model string, n int) error {
	scenarios := []map[string]string{
		{"incident": "決済 API の 500 応答が断続的に発生する", "goal": "根本原因を特定し、最小修正と回帰確認を行う"},
		{"incident": "設定変更後に一部の利用者だけ表示が壊れた", "goal": "影響範囲を調査し、安全に修正する"},
		{"incident": "CI でのみ統合テストが失敗する", "goal": "ログと履歴から再現条件を絞り込む"},
		{"incident": "依存 API の仕様変更が疑われる", "goal": "公式資料を確認し、互換性を維持する"},
	}
	scenario := scenarios[(n-1)%len(scenarios)]
	body, _ := json.Marshal(map[string]any{
		"model": model,
		"state": map[string]any{
			"purpose":     "未知の開発障害を調査し、修正を検証する",
			"sequence":    n,
			"incident":    scenario["incident"],
			"goal":        scenario["goal"],
			"constraints": []string{"最初に既存実装と利用箇所を確認する", "変更は最小限にする", "根拠のない外部操作はしない"},
		},
		"questions": map[string]any{
			"next_tool": map[string]any{
				"type": "choice", "instructions": "Choose the next tool for this investigation. Unknown tools may be selected when appropriate; do not default to shell execution.",
				"criteria": map[string]string{
					"search":       "Find relevant symbols, callers, configuration, and past incidents.",
					"read":         "Inspect the specific implementation, tests, logs, or documentation found by search.",
					"grep":         "Narrow a hypothesis across source, configuration, test, and log files.",
					"write":        "Make the smallest justified code, configuration, or test change after evidence is collected.",
					"agent":        "Delegate an independent investigation, review, or reproduction when parallel evidence would reduce uncertainty.",
					"mcp":          "Use an available connected service for project knowledge, issues, documentation, telemetry, or external API verification.",
					"terminal":     "Run a focused reproduction, test, formatter, or version-control inspection.",
					"browser":      "Inspect a web UI or reproduce an end-to-end user-visible failure when needed.",
					"unknown_tool": "Use a tool not listed here if it is a better fit for the evidence needed next.",
				},
			},
			"tool_plan": map[string]any{"type": "noul", "instructions": "Use multiple complementary tools where evidence requires it. State the next evidence needed, then select the tool that obtains it."},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+key)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", res.Status)
	}
	return nil
}
