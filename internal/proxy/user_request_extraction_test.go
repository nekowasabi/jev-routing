package proxy

import (
	"encoding/json"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/compact"
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
