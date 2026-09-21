package proxy

import (
	"encoding/json"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestOpaqueAgentMessageAndImagesStayEligible(t *testing.T) {
	encrypted := []any{
		map[string]any{"role": "user", "content": "調べて"},
		map[string]any{"type": "agent_message", "encrypted_content": "blob"},
	}
	if reason := historyReason(encrypted); reason != "" {
		t.Fatalf("encrypted agent_message rejected: %s", reason)
	}
	contentArray := []any{
		map[string]any{"type": "agent_message", "content": []any{map[string]any{"type": "text", "text": "hi"}}},
	}
	if reason := historyReason(contentArray); reason != "" {
		t.Fatalf("content-array agent_message rejected: %s", reason)
	}
	if reason := historyReason([]any{map[string]any{"type": "agent_message", "text": 1}}); reason != reasonUnknownHistory {
		t.Fatalf("non-string text must stay rejected: %s", reason)
	}

	images := []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": "what is this"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/a.png"}},
	}}}
	if reason := historyReason(images); reason != "" {
		t.Fatalf("image request rejected: %s", reason)
	}

	req := map[string]any{
		"model": "grok-4",
		"input": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "read the screenshot and grep the repo"},
				map[string]any{"type": "input_image", "image_url": "https://example.com/a.png"},
			}},
			map[string]any{"type": "agent_message", "encrypted_content": "secret-blob"},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "grep"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}},
			map[string]any{"type": "x_search"},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := RewriteWith(nil, raw, host.Codex, nil, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reason == reasonImages || stats.Reason == reasonUnknownHistory || stats.Reason == reasonUnrecognizedFormat {
		t.Fatalf("opaque/image/unknown catalog should stay eligible: %+v", stats)
	}
	if !containsName(stats.MissingFlags, missingImage) || !containsName(stats.MissingFlags, missingOpaqueBlock) {
		t.Fatalf("missing flags=%v", stats.MissingFlags)
	}
	if !containsName(stats.MustKeep, "") && stats.CandidateCount < 2 {
		t.Fatalf("named candidates dropped: %+v", stats)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	hist := asSlice(got["input"])
	if len(hist) != 2 {
		t.Fatalf("history order changed: %#v", hist)
	}
	agent, _ := hist[1].(map[string]any)
	if agent["encrypted_content"] != "secret-blob" {
		t.Fatalf("opaque block mutated: %#v", agent)
	}
	before, _ := json.Marshal(req["input"])
	after, _ := json.Marshal(hist)
	if string(after) != string(before) && agent["encrypted_content"] != "secret-blob" {
		t.Fatalf("encrypted content lost")
	}
}

func TestUnknownCatalogItemStaysSticky(t *testing.T) {
	tools := []any{
		map[string]any{"type": "function", "function": map[string]any{"name": "grep"}},
		map[string]any{"type": "mystery_hosted"},
		map[string]any{"type": "function"},
	}
	if reason := catalogReason(tools); reason != "" {
		t.Fatalf("unknown catalog item failed the catalog: %s", reason)
	}
	if !isSticky(tools[1].(map[string]any)) || !isSticky(tools[2].(map[string]any)) {
		t.Fatal("unknown items must be sticky")
	}
	kept := reconstructCatalog(tools, []any{tools[0]})
	if len(kept) != 3 {
		t.Fatalf("sticky unknown items dropped: %#v", kept)
	}
}
