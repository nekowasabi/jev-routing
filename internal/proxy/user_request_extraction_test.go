package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/compact"
	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

func TestUserRequestSkipsReminderOnlyTurns(t *testing.T) {
	reminder := "<system-reminder>Skills: E2E checks, pull requests, codebase exploration.</system-reminder>"
	msgs := []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": reminder},
			map[string]any{"type": "text", "text": "定義を検索してください"},
		}},
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "a", "name": "Read", "input": map[string]any{}}}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "a", "content": "tool output"},
			map[string]any{"type": "text", "text": "\n" + reminder + "\n"},
		}},
	}
	before, _ := json.Marshal(msgs)
	items, user := itemsFromMessages(msgs)
	if user != "定義を検索してください" {
		t.Fatalf("reminder-only tool result turn replaced the request: %q", user)
	}
	if items[0].Body != reminder+user {
		t.Fatal("raw history item changed instead of only routing text")
	}
	actions := actionsFromItems(items)
	if len(actions) != 1 || actions[0].Result != "tool output" {
		t.Fatalf("tool result extraction changed: %+v", actions)
	}
	after, _ := json.Marshal(applyCompactToMessages(msgs, compact.Result{}))
	if string(before) != string(after) {
		t.Fatal("upstream history changed while extracting routing request")
	}
	msgs = append(msgs, map[string]any{"role": "user", "content": reminder + "別の定義を検索してください"})
	_, user = itemsFromMessages(msgs)
	if user != "別の定義を検索してください" {
		t.Fatalf("latest real request ignored: %q", user)
	}
}

func TestItemsFromMessagesKeepsXCellAskOverCheckout(t *testing.T) {
	ask := "ファイルを変更せず、RewriteWith、extractTools、applyCompactToMessages、DefaultOptions、DefaultUpstream の定義を調べてください。各関数について個別のツール呼び出しで定義を検索し、別のツール呼び出しで本文を読んで確認してください（合計10回以上、並列化せず順に実行）。最終回答は関数名をキー、リポジトリ相対パス:定義行番号を値にしたJSONオブジェクトだけにしてください。説明文や完了マーカーは不要です。"
	checkout := "checkout the workspace and continue"
	specs := []plan.Spec{
		{Name: "read", Desc: "Read a file from the workspace."},
		{Name: "grep", Desc: "Search file contents."},
		{Name: "edit", Desc: "Edit a file."},
		{Name: "run_subagent", Desc: "Launch a subagent that can read files, search the codebase, and handle complex multi-step tasks autonomously. Use this to explore thoroughly or delegate."},
		{Name: "exec", Desc: "Run a shell command."},
	}
	_, user := itemsFromMessages([]any{
		map[string]any{"role": "user", "content": ask},
		map[string]any{"role": "user", "content": checkout},
	})
	if !strings.Contains(user, "定義を調べ") {
		t.Fatalf("checkout stole sequentialLocate ask: %q", user)
	}
	d := plan.DecideSpecs(user, nil, specs, host.Devin)
	if d.Tool == "exec" || d.Tool == "run_subagent" || d.Passthrough {
		t.Fatalf("DecideSpecs on kept ask = %+v, want grep or read", d)
	}
	if d.Tool != "grep" && d.Tool != "read" {
		t.Fatalf("want grep or read, got %+v", d)
	}

	_, only := itemsFromMessages([]any{
		map[string]any{"role": "user", "content": checkout},
	})
	if strings.Contains(only, "定義を調べ") || strings.Contains(only, "ファイルを変更せず") {
		t.Fatalf("checkout-only claimed x-cell ask: %q", only)
	}

	echo := "Run this exact shell command now: echo jev-live-cli-ok. Use a shell or exec tool."
	_, user = itemsFromMessages([]any{
		map[string]any{"role": "user", "content": ask},
		map[string]any{"role": "user", "content": echo},
	})
	if !strings.Contains(user, "echo jev-live-cli-ok") {
		t.Fatalf("explicit shell ask lost to locate history: %q", user)
	}
	d = plan.DecideSpecs(user, nil, specs, host.Grok)
	if d.Tool != "exec" {
		t.Fatalf("shell ask on locate history = %+v, want exec", d)
	}
}
