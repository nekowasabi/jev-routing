package proxy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/compact"
	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestClaudeSignedHistoryCompaction(t *testing.T) {
	for _, thinking := range []map[string]any{
		{"type": "thinking", "thinking": "synthetic reasoning", "signature": "synthetic-signature"},
		{"type": "redacted_thinking", "data": "synthetic-encrypted-data"},
	} {
		for _, withText := range []bool{false, true} {
			content := []any{thinking}
			if withText {
				content = append(content, map[string]any{"type": "text", "text": "done"})
			}
			content = append(content, map[string]any{"type": "tool_use", "id": "a", "name": "Read", "input": map[string]any{}})
			msgs := []any{
				map[string]any{"role": "assistant", "content": content},
				map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "a", "content": "old"}}},
			}
			if reason := historyReason(msgs); reason != "" {
				t.Fatalf("standard Claude thinking rejected: %s", reason)
			}
			got := applyCompactToMessages(msgs, compact.Result{Decisions: []compact.Decision{{ID: "a", Action: compact.ActionDrop}}})
			blocks := asSlice(got[0].(map[string]any)["content"])
			if !reflect.DeepEqual(blocks[0], thinking) {
				t.Fatal("signed thinking block changed")
			}
			if withText {
				if len(got) != 1 || len(blocks) != 2 {
					t.Fatalf("safe deletion with text was blocked: %#v", got)
				}
			} else if !reflect.DeepEqual(got, msgs) {
				t.Fatalf("deletion left only thinking: %#v", got)
			}
		}
	}
}

func TestClaudeToolReferencePairCannotBeCompacted(t *testing.T) {
	msgs := []any{
		map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "search", "name": "ToolSearch", "input": map[string]any{}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "search", "content": []any{
			map[string]any{"type": "tool_reference", "tool_name": "mcp__example__read"},
			map[string]any{"type": "text", "text": "tool discovered"},
		}}}},
	}
	if reason := historyReason(msgs); reason != "" {
		t.Fatalf("standard tool reference rejected: %s", reason)
	}
	items, _ := itemsFromMessages(msgs)
	if !items[len(items)-1].Pinned {
		t.Fatal("tool definition reference must be pinned before compaction decisions")
	}
	for _, action := range []compact.Action{compact.ActionDrop, compact.ActionTruncate} {
		got := applyCompactToMessages(msgs, compact.Result{
			Decisions: []compact.Decision{{ID: "search", Action: action}, {ID: "search_r", Action: action}},
			Items:     []compact.Item{{ID: "search_r", Body: "short"}},
		})
		if !reflect.DeepEqual(got, msgs) {
			t.Fatalf("%s damaged tool reference pair: %#v", action, got)
		}
	}
}

func TestClaudeThinkingHistoryStillCompacts(t *testing.T) {
	thinking := map[string]any{"type": "thinking", "thinking": "synthetic", "signature": "synthetic"}
	msgs := []any{
		map[string]any{"role": "user", "content": "Run pwd using Bash."},
		map[string]any{"role": "assistant", "content": []any{thinking, map[string]any{"type": "tool_use", "id": "search", "name": "ToolSearch", "input": map[string]any{}}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "search", "content": []any{
			map[string]any{"type": "tool_reference", "tool_name": "Bash"},
		}}}},
	}
	for _, name := range []string{"Read", "Read", "Edit"} {
		id := name + string(rune('0'+len(msgs)))
		msgs = append(msgs,
			map[string]any{"role": "assistant", "content": []any{thinking, map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}}},
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": strings.Repeat("old output\n", 400)}}})
	}
	req := map[string]any{"messages": msgs, "tools": []any{map[string]any{"name": "Bash"}}, "thinking": map[string]any{"type": "enabled", "budget_tokens": 8000}}
	raw, _ := json.Marshal(req)
	opt := DefaultOptions()
	opt.Reasoning = ReasoningPreserve
	out, stats, err := RewriteWith(nil, raw, host.Claude, nil, opt)
	if err != nil || !stats.CompactApplied || stats.CharsAfter >= stats.CharsBefore {
		t.Fatalf("standard Claude history did not compact: %+v err=%v", stats, err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	for _, index := range []int{1, 3, 5, 7} {
		blocks := asSlice(asSlice(got["messages"])[index].(map[string]any)["content"])
		if !reflect.DeepEqual(blocks[0], thinking) || len(blocks) != 2 {
			t.Fatalf("thinking/call altered: %#v", blocks)
		}
	}
	if !reflect.DeepEqual(asSlice(got["messages"])[2], msgs[2]) {
		t.Fatal("rewrite changed embedded tool references")
	}
}
