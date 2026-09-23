package proxy

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/compact"
	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

func TestCompactionHistoryInvariants(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		decisions   []compact.Decision
		wantCount   int
		unchanged   bool
	}{
		{
			name:      "system predecessor",
			input:     `[{"role":"assistant","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"a","name":"Read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"old"}]},{"role":"system","content":"boundary"}]`,
			decisions: []compact.Decision{{ID: "a", Action: compact.ActionDrop}},
			wantCount: 3, unchanged: true,
		},
		{
			name:      "responses conflicting pair decisions",
			input:     `[{"type":"function_call","call_id":"a","name":"Read","arguments":"{}"},{"type":"function_call_output","call_id":"a","output":"old"}]`,
			decisions: []compact.Decision{{ID: "a_r", Action: compact.ActionKeep}, {ID: "a", Action: compact.ActionDrop}},
			wantCount: 2, unchanged: true,
		},
		{
			name:      "custom result only drop",
			input:     `[{"type":"custom_tool_call","call_id":"a","name":"exec","input":"ls"},{"type":"custom_tool_call_output","call_id":"a","output":"old"}]`,
			decisions: []compact.Decision{{ID: "a_r", Action: compact.ActionDrop}},
			wantCount: 2, unchanged: true,
		},
		{
			name:      "pending call",
			input:     `[{"type":"function_call","call_id":"a","name":"Read","arguments":"{}"}]`,
			decisions: []compact.Decision{{ID: "a", Action: compact.ActionDrop}},
			wantCount: 1, unchanged: true,
		},
		{
			name:      "chat array text survives call deletion",
			input:     `[{"role":"assistant","content":[{"type":"text","text":"checking"}],"tool_calls":[{"id":"a","type":"function","function":{"name":"Read","arguments":"{}"}}]},{"role":"tool","tool_call_id":"a","content":"old"}]`,
			decisions: []compact.Decision{{ID: "a", Action: compact.ActionDrop}},
			wantCount: 1,
		},
		{
			name:      "responses valid deletion",
			input:     `[{"type":"function_call","call_id":"a","name":"Read","arguments":"{}"},{"type":"function_call_output","call_id":"a","output":"old"},{"role":"user","content":"continue"}]`,
			decisions: []compact.Decision{{ID: "a", Action: compact.ActionDrop}},
			wantCount: 1,
		},
		{
			name:      "claude valid deletion",
			input:     `[{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"Read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"old"}]},{"role":"user","content":"continue"}]`,
			decisions: []compact.Decision{{ID: "a", Action: compact.ActionDrop}},
			wantCount: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var msgs, before []any
			if err := json.Unmarshal([]byte(tc.input), &msgs); err != nil {
				t.Fatal(err)
			}
			_ = json.Unmarshal([]byte(tc.input), &before)
			got := applyCompactToMessages(msgs, compact.Result{Decisions: tc.decisions})
			if len(got) != tc.wantCount || (tc.unchanged && !reflect.DeepEqual(got, before)) {
				t.Fatalf("history invariant broken: got %#v, want count=%d unchanged=%v", got, tc.wantCount, tc.unchanged)
			}
			if !reflect.DeepEqual(msgs, before) {
				t.Fatal("compaction mutated the input history")
			}
		})
	}
}

func TestCustomToolCallInputBecomesActionInput(t *testing.T) {
	for _, input := range []string{"*** Begin Patch\n*** End Patch", ""} {
		items, _ := itemsFromMessages([]any{
			map[string]any{"type": "custom_tool_call", "call_id": "patch", "name": "apply_patch", "input": input},
			map[string]any{"type": "custom_tool_call_output", "call_id": "patch", "output": "ok"},
		})
		actions := actionsFromItems(items)
		if len(actions) != 1 || actions[0].Input != input || items[0].Body != input || items[0].Chars != len(input) {
			t.Fatalf("custom tool input lost: items=%+v actions=%+v", items, actions)
		}
	}
}

func TestCompactionIndependentOfSelection(t *testing.T) {
	for _, h := range []host.ID{host.Claude, host.Codex} {
		for _, scenario := range []string{"no-catalog", "respond", "error", "uncertain", "invalid", "local-passthrough", "baseline", "off", "ineligible"} {
			t.Run(string(h)+"/"+scenario, func(t *testing.T) {
				request := "Describe the current situation."
				if scenario == "local-passthrough" {
					request = ""
				}
				msgs := []any{map[string]any{"role": "user", "content": request}}
				for _, id := range []string{"a", "b", "c"} {
					name := "Read"
					if id == "c" {
						name = "Edit"
					}
					result := strings.Repeat("old output\n", 400)
					if h == host.Claude {
						msgs = append(msgs,
							map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}}},
							map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": result}}})
					} else {
						msgs = append(msgs,
							map[string]any{"type": "function_call", "call_id": id, "name": name, "arguments": "{}"},
							map[string]any{"type": "function_call_output", "call_id": id, "output": result})
					}
				}
				key := "messages"
				if h == host.Codex {
					key = "input"
				}
				req := map[string]any{key: msgs, "reasoning": map[string]any{"effort": "high"}}
				if scenario != "no-catalog" {
					req["tools"] = []any{map[string]any{"type": "function", "name": "mystery"}}
				}
				opt := DefaultOptions()
				if scenario == "baseline" {
					opt.Mode = ModeBaseline
				}
				if scenario == "off" {
					opt.Compaction = CompactionOff
				}
				if scenario == "ineligible" {
					req["tool_choice"] = "required"
				}
				client := jevAnswers(t, "", 0, 0, 0, func(w http.ResponseWriter, r *http.Request) {
					var in struct {
						Questions map[string]json.RawMessage `json:"questions"`
					}
					_ = json.NewDecoder(r.Body).Decode(&in)
					answers := map[string]any{}
					if _, selection := in.Questions["next_tool"]; selection {
						if scenario == "error" {
							http.Error(w, "selection failed", http.StatusInternalServerError)
							return
						}
						choice, confidence := plan.Respond, 0.9
						if scenario == "uncertain" {
							confidence = 0.5
						}
						if scenario == "invalid" {
							choice = "unknown_tool"
						}
						answers["next_tool"] = map[string]any{"type": "choice", "choice": choice, "confidence": confidence, "probabilities": adoptTestProbs(choice)}
						answers["needs_tool"] = map[string]any{"type": "noul", "noul": 0.1, "confidence": 0.9}
					} else {
						for key := range in.Questions {
							score := 0.1
							if strings.HasPrefix(key, "call_") {
								score = 0.9
							}
							answers[key] = map[string]any{"type": "noul", "noul": score, "confidence": 0.9}
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
				})
				if scenario == "no-catalog" || scenario == "local-passthrough" {
					client = nil
				}
				raw, _ := json.Marshal(req)
				out, stats, err := RewriteWith(nil, raw, h, client, opt)
				wantCompact := scenario != "baseline" && scenario != "off" && scenario != "ineligible" && h != host.Claude
				wantReason := map[string]string{
					"no-catalog": reasonNoCatalog, "respond": reasonNoToolNeeded, "error": reasonCallFailed,
					"uncertain": reasonNoToolNeeded, "invalid": reasonInvalidJev, "local-passthrough": reasonLocalPassthrough,
					"baseline": reasonBaseline, "off": reasonNoToolNeeded, "ineligible": reasonExplicitToolChoice,
				}[scenario]
				// Claude gets a respond reminder instead of a changed catalog.
				wantAdvise := h == host.Claude && wantReason == reasonNoToolNeeded
				wantApply := applyNone
				if wantAdvise {
					wantApply = applyAdvise
				}
				if err != nil || stats.CompactApplied != wantCompact || stats.Changed != (wantCompact || wantAdvise) || stats.Reason != wantReason {
					t.Fatalf("independent compaction failed: %+v err=%v", stats, err)
				}
				if !wantCompact && !wantAdvise && string(out) != string(raw) {
					t.Fatal("protected request changed")
				}
				var got map[string]any
				_ = json.Unmarshal(out, &got)
				if !reflect.DeepEqual(got["tools"], req["tools"]) || !reflect.DeepEqual(got["reasoning"], req["reasoning"]) || stats.ToolBefore != stats.ToolAfter || stats.Apply != wantApply {
					t.Fatalf("compaction changed selection or reasoning: %+v", stats)
				}
				if wantCompact && (stats.CompactDropped == 0 || stats.CharsAfter >= stats.CharsBefore) {
					t.Fatalf("no actual history reduction: %+v", stats)
				}
			})
		}
	}
}

func TestGrokLiveHistoryCompactsWithObservedTypes(t *testing.T) {
	fn := func(name string) any {
		return map[string]any{
			"type": "function", "name": name, "description": "tool",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
		}
	}
	stale := strings.Repeat("old output\n", 400)
	input := []any{
		map[string]any{"type": "message", "role": "user", "content": "Find the failing auth middleware test, fix it, and re-run.", "extra_keep": "user"},
		map[string]any{"type": "reasoning", "summary": []any{}, "extra_keep": "reasoning"},
		map[string]any{"type": "function_call", "call_id": "c1", "name": "grep", "arguments": `{"pattern":"auth"}`, "extra_keep": "c1"},
		map[string]any{"type": "function_call_output", "call_id": "c1", "output": stale, "extra_keep": "c1r"},
		map[string]any{"type": "function_call", "call_id": "c2", "name": "read_file", "arguments": `{"path":"x.go"}`, "extra_keep": "c2"},
		map[string]any{"type": "function_call_output", "call_id": "c2", "output": stale, "extra_keep": "c2r"},
		map[string]any{"type": "message", "role": "user", "content": "Continue with the fix."},
	}
	tools := []any{
		fn("grep"), fn("read_file"), fn("search_replace"), fn("run_terminal_command"),
		map[string]any{"type": "web_search"},
		map[string]any{"type": "x_search"},
	}
	for _, scenario := range []string{"filter", "uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			req := map[string]any{"model": "grok-4.6", "input": input, "tools": tools}
			choice, conf, needs := "grep", 0.95, 0.95
			if scenario == "uncertain" {
				choice, conf, needs = plan.Respond, 0.5, 0.1
				req["input"] = []any{
					map[string]any{"type": "message", "role": "user", "content": "Describe the current situation.", "extra_keep": "user"},
					map[string]any{"type": "reasoning", "summary": []any{}, "extra_keep": "reasoning"},
					map[string]any{"type": "function_call", "call_id": "c1", "name": "grep", "arguments": `{"pattern":"auth"}`, "extra_keep": "c1"},
					map[string]any{"type": "function_call_output", "call_id": "c1", "output": stale, "extra_keep": "c1r"},
					map[string]any{"type": "function_call", "call_id": "c2", "name": "read_file", "arguments": `{"path":"x.go"}`, "extra_keep": "c2"},
					map[string]any{"type": "function_call_output", "call_id": "c2", "output": stale, "extra_keep": "c2r"},
					map[string]any{"type": "message", "role": "user", "content": "What now?"},
				}
			}
			client := jevAnswers(t, "", 0, 0, 0, func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Questions map[string]json.RawMessage `json:"questions"`
				}
				_ = json.NewDecoder(r.Body).Decode(&in)
				answers := map[string]any{}
				if _, ok := in.Questions["next_tool"]; ok {
					answers["next_tool"] = map[string]any{"type": "choice", "choice": choice, "confidence": conf, "probabilities": adoptTestProbs(choice)}
					answers["needs_tool"] = map[string]any{"type": "noul", "noul": needs, "confidence": 0.9}
				} else {
					for key := range in.Questions {
						score := 0.1
						if strings.HasPrefix(key, "call_") {
							score = 0.9
						}
						answers[key] = map[string]any{"type": "noul", "noul": score, "confidence": 0.9}
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
			})
			raw, _ := json.Marshal(req)
			out, stats, err := RewriteWith(nil, raw, host.Grok, client, DefaultOptions())
			if err != nil {
				t.Fatal(err)
			}
			if stats.Reason == reasonUnrecognizedFormat || stats.Reason == reasonUnknownHistory {
				t.Fatalf("observed grok history/catalog ineligible: %+v", stats)
			}
			if !stats.CompactApplied || stats.CompactDropped == 0 || stats.CharsAfter >= stats.CharsBefore {
				t.Fatalf("compaction did not rewrite history: %+v", stats)
			}
			var got map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			hist := asSlice(got["input"])
			calls, results, extras := map[string]bool{}, map[string]bool{}, map[string]bool{}
			for _, rawItem := range hist {
				m, _ := rawItem.(map[string]any)
				typ, _ := m["type"].(string)
				switch typ {
				case "function_call":
					calls[firstString(m, "call_id", "id")] = true
				case "function_call_output":
					results[firstString(m, "call_id", "id")] = true
				}
				if v, _ := m["extra_keep"].(string); v != "" {
					extras[v] = true
				}
			}
			for id := range calls {
				if !results[id] {
					t.Fatalf("call %s missing result after compact", id)
				}
			}
			for id := range results {
				if !calls[id] {
					t.Fatalf("result %s missing call after compact", id)
				}
			}
			if !extras["user"] || !extras["reasoning"] {
				t.Fatalf("unknown fields on kept items dropped: %v", extras)
			}
			if scenario == "filter" && (stats.Apply != applyFilter || stats.ToolAfter >= stats.ToolBefore) {
				t.Fatalf("want filter shrink with compact, got %+v", stats)
			}
			if scenario == "uncertain" && stats.Apply != applyNone {
				t.Fatalf("uncertain next tool should skip selection, got %+v", stats)
			}
		})
	}
}

func TestCompactionBoundaryStillAllowsOtherPairsAndTruncation(t *testing.T) {
	var msgs []any
	_ = json.Unmarshal([]byte(`[{"role":"system","content":"initial"},{"role":"assistant","content":[{"type":"tool_use","id":"old","name":"Read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"old","content":"old"}]},{"role":"assistant","content":[{"type":"tool_use","id":"boundary","name":"Read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"boundary","content":"long body"}]},{"role":"system","content":"boundary"}]`), &msgs)
	res := compact.Result{
		Decisions: []compact.Decision{{ID: "old", Action: compact.ActionDrop}, {ID: "boundary", Action: compact.ActionKeep}, {ID: "boundary_r", Action: compact.ActionTruncate}},
		Items:     []compact.Item{{ID: "boundary_r", Body: "short"}},
	}
	got := applyCompactToMessages(msgs, res)
	if len(got) != 4 || got[2].(map[string]any)["role"] != "user" {
		t.Fatalf("valid compaction failed: %#v", got)
	}
	results := toolResults(got[2].(map[string]any))
	if len(results) != 1 || results[0].Body != "short" {
		t.Fatalf("boundary result was not truncated: %#v", results)
	}
	if toolResults(msgs[4].(map[string]any))[0].Body != "long body" {
		t.Fatal("truncation mutated source history")
	}
}

func TestCompactionPreservesSystemSuccessor(t *testing.T) {
	var msgs []any
	if err := json.Unmarshal([]byte(`[
		{"role":"assistant","content":[{"type":"tool_use","id":"old","name":"Read","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"old","content":"old"}]},
		{"role":"user","content":"start"},
		{"role":"system","content":"boundary"},
		{"role":"assistant","content":[{"type":"tool_use","id":"anchor","name":"Read","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"anchor","content":"long result"}]},
		{"role":"user","content":"continue"}
	]`), &msgs); err != nil {
		t.Fatal(err)
	}
	got := applyCompactToMessages(msgs, compact.Result{Decisions: []compact.Decision{
		{ID: "old", Action: compact.ActionDrop}, {ID: "anchor", Action: compact.ActionDrop},
	}})
	if !reflect.DeepEqual(got, msgs[2:]) {
		t.Fatalf("system successor removed or unrelated compaction blocked: %#v", got)
	}
	got = applyCompactToMessages(msgs, compact.Result{
		Decisions: []compact.Decision{{ID: "old", Action: compact.ActionDrop}, {ID: "anchor_r", Action: compact.ActionTruncate}},
		Items:     []compact.Item{{ID: "anchor_r", Body: "short"}},
	})
	if len(got) != 5 || got[2].(map[string]any)["role"] != "assistant" || toolResults(got[3].(map[string]any))[0].Body != "short" {
		t.Fatalf("system successor protection blocked valid truncation: %#v", got)
	}
}

func TestResponsesArrayToolResultsRetainText(t *testing.T) {
	for _, typ := range []string{"function_call_output", "custom_tool_call_output"} {
		result := map[string]any{"type": typ, "call_id": "a", "output": []any{
			map[string]any{"type": "input_text", "text": "first\n"},
			map[string]any{"type": "input_text", "text": strings.Repeat("second\n", 400)},
		}}
		msgs := []any{
			map[string]any{"type": "function_call", "call_id": "a", "name": "Read", "arguments": "{}"},
			result,
		}
		if typ == "custom_tool_call_output" {
			msgs[0] = map[string]any{"type": "custom_tool_call", "call_id": "a", "name": "Read", "input": "file"}
		}
		if reason := historyReason(msgs); reason != "" {
			t.Fatalf("text result array rejected: %s", reason)
		}
		items, _ := itemsFromMessages(msgs)
		want := "first\n" + strings.Repeat("second\n", 400)
		if items[1].Body != want || items[1].Chars != len(want) || actionsFromItems(items)[0].Result != want {
			t.Fatalf("array result lost: %+v", items)
		}
		decisions := []compact.Decision{{ID: "a_r", Action: compact.ActionTruncate}}
		got := applyCompactToMessages(msgs, compact.Result{Decisions: decisions, Items: compact.Apply(items, decisions, 20)})
		body, ok := got[1].(map[string]any)["output"].(string)
		if !ok || !strings.HasPrefix(body, "first\nsecond") || !strings.Contains(body, "jev-compaction truncated") || len(body) >= len(want) {
			t.Fatalf("array result was not safely truncated: %q", body)
		}
		for _, image := range []string{"image", "image_url", "input_image", "image_file"} {
			result["output"] = []any{map[string]any{"type": image}}
			if reason := historyReason(msgs); reason != "" {
				t.Fatalf("image result should stay eligible: %s", reason)
			}
		}
	}
}

func TestCompactionPreservesEmbeddedAdditionalTools(t *testing.T) {
	var msgs []any
	if err := json.Unmarshal([]byte(`[
		{"type":"additional_tools","tools":[{"type":"namespace","name":"functions","tools":[{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"command":{"type":"string"}}}},{"type":"custom","name":"apply_patch","format":{"type":"text"}}]}]},
		{"role":"user","content":"continue"},
		{"type":"function_call","call_id":"old","name":"exec_command","arguments":"{}"},
		{"type":"function_call_output","call_id":"old","output":"old output"},
		{"type":"custom_tool_call","call_id":"recent","name":"apply_patch","input":"patch"},
		{"type":"custom_tool_call_output","call_id":"recent","output":"long output"}
	]`), &msgs); err != nil {
		t.Fatal(err)
	}
	items, user := itemsFromMessages(msgs)
	if len(items) != 5 || user != "continue" || len(actionsFromItems(items)) != 2 {
		t.Fatalf("tool definitions became history: items=%+v user=%q", items, user)
	}
	catalog, _ := json.Marshal(msgs[0])
	got := applyCompactToMessages(msgs, compact.Result{
		Decisions: []compact.Decision{{ID: "old", Action: compact.ActionDrop}, {ID: "recent_r", Action: compact.ActionTruncate}},
		Items:     []compact.Item{{ID: "recent_r", Body: "short"}},
	})
	if len(got) != 4 || !reflect.DeepEqual(got[0], msgs[0]) || got[3].(map[string]any)["output"] != "short" {
		t.Fatalf("catalog was lost or valid compaction failed: %#v", got)
	}
	afterCatalog, _ := json.Marshal(got[0])
	if string(afterCatalog) != string(catalog) {
		t.Fatal("embedded tool definitions changed during compaction")
	}
}

func TestCompactionStatsMeasureAppliedHistory(t *testing.T) {
	for _, h := range []host.ID{host.Codex} {
		for _, stale := range []bool{false, true} {
			t.Run(string(h)+"/"+map[bool]string{false: "unchanged", true: "compacted"}[stale], func(t *testing.T) {
				msgs := []any{map[string]any{"role": "user", "content": "Run pwd using Bash."}}
				if stale {
					for _, id := range []string{"a", "b", "c"} {
						name := "Read"
						if id == "c" {
							name = "Edit"
						}
						if h == host.Claude {
							msgs = append(msgs,
								map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{"path": "old.txt"}}}},
								map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": strings.Repeat("old grep output\n", 300)}}})
						} else {
							msgs = append(msgs,
								map[string]any{"type": "function_call", "call_id": id, "name": name, "arguments": `{"path":"old.txt"}`},
								map[string]any{"type": "function_call_output", "call_id": id, "output": strings.Repeat("old grep output\n", 300)})
						}
					}
				}
				key := "messages"
				tool := map[string]any{"name": "Bash", "input_schema": map[string]any{"type": "object"}}
				if h == host.Codex {
					key = "input"
					tool = map[string]any{"type": "function", "name": "Bash", "parameters": map[string]any{"type": "object"}}
				}
				req := map[string]any{key: msgs, "tools": []any{tool}}
				raw, _ := json.Marshal(req)
				out, stats, err := RewriteWith(nil, raw, h, nil, localOpt())
				if err != nil || !stats.Changed || stats.CompactApplied != stale {
					t.Fatalf("unexpected application: stats=%+v err=%v", stats, err)
				}
				var got map[string]any
				_ = json.Unmarshal(out, &got)
				before, _ := json.Marshal(msgs)
				after, _ := json.Marshal(got[key])
				if stats.CharsBefore != len(before) || stats.CharsAfter != len(after) || (stats.CompactDropped > 0) != stale {
					t.Fatalf("stats do not describe applied history: %+v bytes=%d->%d", stats, len(before), len(after))
				}
			})
		}
	}
}
