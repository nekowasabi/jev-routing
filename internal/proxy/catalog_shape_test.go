package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

func TestCatalogFromRequestDoesNotLaunch(t *testing.T) {
	probe := &plan.LaunchProbe{}
	body := []byte(`{"tools":[{"name":"Grep","description":"search"},{"name":"mcp__slack__search","description":"slack"}]}`)
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	tools, _ := extractTools(root)
	specs := plan.SpecsFrom(asMaps(tools))
	cat, err := plan.BuildCatalog(plan.Inventory{Tools: specs}, host.Grok, probe)
	if err != nil {
		t.Fatal(err)
	}
	if probe.MCPStarts != 0 || probe.CLIRuns != 0 {
		t.Fatalf("request catalog launched processes %+v", probe)
	}
	if len(cat.Eligible()) == 0 || cat.Revision == "" {
		t.Fatalf("empty catalog %+v", cat)
	}
	raw := chatReq("find a symbol", []any{
		map[string]any{"type": "function", "function": map[string]any{"name": "grep", "description": "search"}},
	})
	_, stats, err := Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.CatalogRevision == "" {
		t.Fatalf("rewrite omitted catalog revision %+v", stats)
	}
}

func TestCatalogShapeContainsMetadataOnly(t *testing.T) {
	body := []byte(`{"input":[{"role":"user","content":"PRIVATE_PROMPT"}],"tools":[{"type":"namespace","name":"functions","description":"PRIVATE_DESCRIPTION","tools":[{"type":"function","name":"PRIVATE_NAME","parameters":{"type":"object","properties":{"PRIVATE_ARGUMENT":{"default":"PRIVATE_DEFAULT"}}}}]}],"authorization":"PRIVATE_AUTH"}`)
	shape := catalogShape(body)
	if shape == nil || shape.ToolsType != "array" || shape.RawCount != 1 || shape.FlatCount != 1 || shape.Candidates != 1 || len(shape.Definitions[0].Children) != 1 {
		t.Fatalf("unexpected shape: %+v", shape)
	}
	encoded, err := json.Marshal(shape)
	if err != nil || bytes.Contains(encoded, []byte("PRIVATE_")) {
		t.Fatalf("private content in metadata: %s, err=%v", encoded, err)
	}
	for _, invalid := range []string{"not json", "null", "[]"} {
		if catalogShape([]byte(invalid)) != nil {
			t.Fatalf("accepted invalid root %q", invalid)
		}
	}
}

func TestCatalogShapeHistoryTypesWithoutContent(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"PRIVATE_PROMPT"},{"role":"assistant","content":[{"type":"thinking","thinking":"PRIVATE_THINKING","signature":"PRIVATE_SIGNATURE"},{"type":"tool_use","id":"PRIVATE_ID","input":{"token":"PRIVATE_ARGUMENT"}}]},{"role":"user","content":[{"type":"tool_result","content":[{"type":"tool_reference","tool_name":"PRIVATE_TOOL"},{"type":"text","text":"PRIVATE_RESULT"}]}]}],"input":[{"type":"function_call","arguments":"PRIVATE_ARGUMENT"},{"type":"function_call_output","output":"PRIVATE_OUTPUT"},{"type":"message","content":[{"type":"input_text","text":"PRIVATE_TEXT"}]}]}`)
	shape := catalogShape(body)
	wantHistory := map[string]int{"messages.object": 3, "input.function_call": 1, "input.function_call_output": 1, "input.message": 1}
	wantContent := map[string]int{"string": 1, "thinking": 1, "tool_use": 1, "tool_result": 1, "tool_reference": 1, "text": 1, "input_text": 1}
	if !reflect.DeepEqual(shape.HistoryTypes, wantHistory) || !reflect.DeepEqual(shape.ContentTypes, wantContent) {
		t.Fatalf("history=%v content=%v", shape.HistoryTypes, shape.ContentTypes)
	}
	encoded, err := json.Marshal(shape)
	if err != nil || bytes.Contains(encoded, []byte("PRIVATE_")) {
		t.Fatalf("content leaked into shape: %s err=%v", encoded, err)
	}
}

func TestRunStatsApplicationCountersAndPrivacy(t *testing.T) {
	for _, h := range []host.ID{host.Claude, host.Codex} {
		t.Run(string(h), func(t *testing.T) {
			s, _ := testProxy(t, h, nil, localOpt())
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"PRIVATE_UPSTREAM_BODY"}`))
			}))
			defer up.Close()
			s.Upstream, _ = url.Parse(up.URL)
			msgs := []any{map[string]any{"role": "user", "content": "Run pwd using Bash. PRIVATE_PROMPT"}}
			for _, id := range []string{"a", "b", "c"} {
				name := "Read"
				if id == "c" {
					name = "Edit"
				}
				if h == host.Claude {
					msgs = append(msgs,
						map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{"path": "PRIVATE_ARGUMENT"}}}},
						map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": strings.Repeat("PRIVATE_RESULT\n", 300)}}})
				} else {
					msgs = append(msgs,
						map[string]any{"type": "function_call", "call_id": id, "name": name, "arguments": `{"path":"PRIVATE_ARGUMENT"}`},
						map[string]any{"type": "function_call_output", "call_id": id, "output": strings.Repeat("PRIVATE_RESULT\n", 300)})
				}
			}
			key, path := "messages", "/v1/messages"
			tool := map[string]any{"name": "Bash", "input_schema": map[string]any{"type": "object"}}
			if h == host.Codex {
				key, path = "input", "/v1/responses"
				tool = map[string]any{"type": "function", "name": "Bash", "parameters": map[string]any{"type": "object"}}
			}
			raw, _ := json.Marshal(map[string]any{key: msgs, "tools": []any{tool}})
			req := httptest.NewRequest(http.MethodPost, path+"?api_key=PRIVATE_QUERY", bytes.NewReader(raw))
			req.Header.Set("Authorization", "Bearer PRIVATE_AUTH")
			handler := s.Handler()
			handler.ServeHTTP(httptest.NewRecorder(), req)
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/unrecognized-rpc", nil))
			ctrl := httptest.NewRequest(http.MethodPost, "/unrecognized-rpc", bytes.NewReader([]byte("PRIVATE_BODY")))
			ctrl.Header.Set("Content-Type", "application/json")
			ctrl.Header.Set("Authorization", "Bearer PRIVATE_AUTH")
			ctrl.Header.Set("Cookie", "session=PRIVATE_COOKIE")
			handler.ServeHTTP(httptest.NewRecorder(), ctrl)
			stats := s.RunStats()
			// Claude history is compacted only on Claude Code's compaction request.
			compacted := h != host.Claude
			if stats["totalRequests"] != 3 || stats["requests"] != 1 || stats["selectionApplied"] != 1 || stats["compactionApplied"] != map[bool]int{false: 0, true: 1}[compacted] {
				t.Fatalf("wrong counters: %+v", stats)
			}
			routes := stats["requestRoutes"].(map[string]int)
			if routes["POST "+path] != 1 || routes["GET /unrecognized-rpc"] != 1 || routes["POST /unrecognized-rpc"] != 1 || len(routes) != 3 {
				t.Fatalf("missing transport evidence: %+v", routes)
			}
			events := stats["events"].([]Event)
			if len(events) != 2 || events[0].UpstreamStatus == nil || *events[0].UpstreamStatus != 400 || events[0].CompactApplied != compacted || events[0].RequestPath != path {
				t.Fatalf("missing request evidence: %+v", events)
			}
			if events[0].Method != http.MethodPost || events[0].JsonValid == nil || !*events[0].JsonValid {
				t.Fatalf("llm observation %+v", events[0])
			}
			if events[1].Reason != reasonNotLLMPath || events[1].ContentType != "application/json" || events[1].Method != http.MethodPost || events[1].JsonValid != nil {
				t.Fatalf("non-llm observation %+v", events[1])
			}
			if stats["selectionSources"].(map[string]int)[events[0].Source] != 1 || stats["applicationModes"].(map[string]int)[events[0].Apply] != 1 {
				t.Fatalf("source/mode counters differ from event: %+v", stats)
			}
			encoded, _ := json.Marshal(stats)
			if bytes.Contains(encoded, []byte("PRIVATE_")) {
				t.Fatalf("private data in RunStats: %s", encoded)
			}
			stats["selectionSources"].(map[string]int)[events[0].Source] = 99
			if s.RunStats()["selectionSources"].(map[string]int)[events[0].Source] != 1 {
				t.Fatal("snapshot exposes mutable aggregate map")
			}
		})
	}
}

func TestRunStatsPreservesJevAttempts(t *testing.T) {
	c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, nil)
	s, _ := testProxy(t, host.Grok, c, DefaultOptions())
	raw := chatReq("summarize this repo's architecture for me", workTools())
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw)))
	stats := s.RunStats()
	events := stats["events"].([]Event)
	if len(events) != 1 || len(events[0].JevAttempts) != 1 || events[0].Source != sourceJev {
		t.Fatalf("missing Jev observation: %+v", events)
	}
	a := events[0].JevAttempts[0]
	if a.Purpose == "" || !a.OK || a.Cached || a.Status != 200 || stats["jevHTTP"] != 1 || stats["jevOK"] != 1 || stats["jevFail"] != 0 {
		t.Fatalf("attempt and counters disagree: %+v %+v", a, stats)
	}
}
