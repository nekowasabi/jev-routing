package proxy

import (
	"github.com/nekowasabi/jev-routing/internal/compact"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

const (
	missingImage       = "image"
	missingOpaqueBlock = "opaque_block"
)

func collectMissingFlags(root map[string]any) []string {
	if root == nil {
		return nil
	}
	hist := asSlice(root["messages"])
	if hist == nil {
		hist = asSlice(root["input"])
	}
	seen := map[string]bool{}
	var flags []string
	add := func(flag string) {
		if flag == "" || seen[flag] {
			return
		}
		seen[flag] = true
		flags = append(flags, flag)
	}
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if _, ok := t["encrypted_content"]; ok {
				add(missingOpaqueBlock)
			}
			if typ, _ := t["type"].(string); typ == "agent_message" {
				if _, ok := t["content"].([]any); ok {
					add(missingOpaqueBlock)
				}
			}
			switch firstString(t, "type") {
			case "image", "image_url", "input_image", "image_file":
				add(missingImage)
			}
			for _, child := range t {
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(hist)
	return flags
}

func relatedHistory(items []compact.Item, limit int) []string {
	if limit <= 0 {
		limit = 6
	}
	var rev []string
	for i := len(items) - 1; i >= 0 && len(rev) < limit; i-- {
		if items[i].Kind != compact.KindText && items[i].Kind != compact.KindResult {
			continue
		}
		text := items[i].Preview
		if text == "" {
			continue
		}
		rev = append(rev, text)
	}
	out := make([]string, 0, len(rev))
	for i := len(rev) - 1; i >= 0; i-- {
		out = append(out, rev[i])
	}
	return out
}

func explicitConstraints(root map[string]any, user string) []string {
	var out []string
	if tc, ok := root["tool_choice"].(string); ok && tc != "" && tc != "auto" && tc != "none" {
		out = append(out, "tool_choice:"+tc)
	}
	if plan.SequentialLocate(user) {
		out = append(out, "sequential_locate")
	}
	return out
}

func stickyNames(tools []any) []string {
	var names []string
	seen := map[string]bool{}
	for _, raw := range tools {
		m, ok := raw.(map[string]any)
		if !ok || !isSticky(m) {
			continue
		}
		n := toolNameOf(m)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	return names
}

func judgmentExtras(user string, actions []plan.Action, items []compact.Item, names []string, root map[string]any, mustKeep, flags []string) map[string]any {
	extra := map[string]any{
		"available_candidates": names,
		"must_keep":            mustKeep,
		"missing_flags":        flags,
	}
	if rel := relatedHistory(items, 6); len(rel) > 0 {
		extra["related_history"] = rel
	}
	if cons := explicitConstraints(root, user); len(cons) > 0 {
		extra["explicit_constraints"] = cons
	}
	return extra
}
