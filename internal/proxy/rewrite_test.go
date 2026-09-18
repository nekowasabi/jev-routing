package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/compact"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
)

func TestCodexChatGPTBackendStripsV1Prefix(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	u, err := url.Parse(upstream.URL + "/backend-api/codex")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New("", host.Codex, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	s.Upstream = u
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-terra","input":[]}`))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if gotPath != "/backend-api/codex/responses" {
		t.Fatalf("path=%q", gotPath)
	}
}

func TestCodexReasoningKeepsAllTurnsContext(t *testing.T) {
	root := map[string]any{"reasoning": map[string]any{"effort": "high"}}
	disableThinking(root, host.Codex)

	reasoning := root["reasoning"].(map[string]any)
	if reasoning["effort"] != "none" || reasoning["context"] != "all_turns" {
		t.Fatalf("reasoning=%v", reasoning)
	}
}

func TestGrokRewriteStripsCatalog(t *testing.T) {
	req := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "The auth middleware test is failing. Find it, fix the assertion in place, and re-run the tests."},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "grep"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "search_replace"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "run_terminal_cmd"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "mcp_edit_file"}},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolAfter != 1 {
		t.Fatalf("tools after=%d chosen=%s", stats.ToolAfter, stats.Chosen)
	}
	if stats.Chosen != "grep" {
		t.Fatalf("chosen %s", stats.Chosen)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	tools := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("kept %d tools", len(tools))
	}
}

func TestGrokRewriteDoesNotSendReasoningNone(t *testing.T) {
	req := map[string]any{
		"model": "grok-4.6",
		"messages": []any{
			map[string]any{"role": "user", "content": "hello"},
		},
		"reasoning":        map[string]any{"effort": "high"},
		"reasoning_effort": "high",
	}
	raw, _ := json.Marshal(req)
	out, _, err := Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"none"`) {
		t.Fatalf("xAI rejects effort none: %s", out)
	}
	r, ok := got["reasoning"].(map[string]any)
	if !ok || r["effort"] != "low" {
		t.Fatalf("reasoning=%v", got["reasoning"])
	}
	if got["reasoning_effort"] != "low" {
		t.Fatalf("reasoning_effort=%v", got["reasoning_effort"])
	}
}

func TestClaudeRespondStripsAll(t *testing.T) {
	req := map[string]any{
		"model":  "claude-opus-4-6",
		"system": "you are claude",
		"messages": []any{
			map[string]any{"role": "user", "content": "thanks, that's all"},
		},
		"tools": []any{
			map[string]any{"name": "Read"},
			map[string]any{"name": "Bash"},
		},
		"thinking": map[string]any{"type": "enabled", "budget_tokens": 8000},
	}
	raw, _ := json.Marshal(req)
	_, stats, err := Rewrite(raw, host.Claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolAfter == 0 {
		t.Fatalf("unknown first turn must not strip the catalog %+v", stats)
	}
}

func TestExploreKeepsAgent(t *testing.T) {
	req := map[string]any{
		"model":  "claude-opus-4-6",
		"system": "you are claude",
		"messages": []any{
			map[string]any{"role": "user", "content": "Explore the auth package thoroughly and report how sessions are stored."},
		},
		"tools": []any{
			map[string]any{"name": "Read", "description": "Read a file"},
			map[string]any{"name": "Grep", "description": "Search file contents"},
			map[string]any{"name": "Agent", "description": "Launch a new agent to handle complex multi-step tasks autonomously"},
			map[string]any{"name": "Bash", "description": "Run a shell command"},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chosen != "Agent" {
		t.Fatalf("chosen %s %+v", stats.Chosen, stats)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	tools := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("kept %d", len(tools))
	}
	if tools[0].(map[string]any)["name"] != "Agent" {
		t.Fatalf("kept %+v", tools[0])
	}
}

func TestUnknownToolNameFailsOpen(t *testing.T) {
	req := map[string]any{
		"model":  "claude-opus-4-6",
		"system": "x",
		"messages": []any{
			map[string]any{"role": "user", "content": "Explore the repo."},
		},
		"tools": []any{
			map[string]any{"name": "Read"},
			map[string]any{"name": "Task", "description": "Launch a new agent"},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolAfter == 0 {
		t.Fatalf("must not empty the catalog %+v", stats)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if len(got["tools"].([]any)) == 0 {
		t.Fatal("empty tools")
	}
}

func TestCompactTruncatesStaleToolResult(t *testing.T) {
	fat := strings.Repeat("line of grep output\n", 200)
	req := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "The auth middleware test is failing. Find it and fix it in place."},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c1", "type": "function", "function": map[string]any{"name": "grep", "arguments": `{"pattern":"auth"}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c1", "name": "grep", "content": fat},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c2", "type": "function", "function": map[string]any{"name": "read_file", "arguments": `{"path":"x"}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c2", "name": "read_file", "content": "file body"},
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"id": "c3", "type": "function", "function": map[string]any{"name": "search_replace", "arguments": `{}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c3", "name": "search_replace", "content": "ok"},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "run_terminal_cmd"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "grep"}},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.CharsAfter >= stats.CharsBefore && stats.CompactDropped == 0 {
		t.Fatalf("compaction did nothing %+v outlen=%d inlen=%d", stats, len(out), len(raw))
	}
}

func TestCompactDropsMessageWhenAllBlocksDropped(t *testing.T) {
	res := compact.Result{Decisions: []compact.Decision{{ID: "t1", Action: compact.ActionDrop}}}
	msgs := []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "dropped"},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "kept"},
		}},
	}
	out := applyCompactToMessages(msgs, res)
	if len(out) != 1 {
		t.Fatalf("want 1 message, got %d: %+v", len(out), out)
	}
	m := out[0].(map[string]any)
	if c, _ := m["content"].([]any); len(c) != 1 {
		t.Fatalf("wrong message survived: %+v", m)
	}
}

func TestGrokMissingCatalogDoesNotWriteEmptyTools(t *testing.T) {
	req := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "フィボナッチ数列を出力するコードを作成して"},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chosen != "passthrough:no-catalog" {
		t.Fatalf("chosen %s %+v", stats.Chosen, stats)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["tools"]; ok {
		t.Fatalf("must not insert tools key: %+v", got["tools"])
	}
}

func TestFibonacciWithCatalogDoesNotStrip(t *testing.T) {
	req := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "フィボナッチ数列を出力するコードを作成して"},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "search_replace"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "task"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "run_terminal_cmd"}},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolAfter == 0 {
		t.Fatalf("must not empty catalog %+v", stats)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if len(got["tools"].([]any)) == 0 {
		t.Fatal("stripped all tools")
	}
}

func TestMCPToolsSurviveUnknownPrompt(t *testing.T) {
	req := map[string]any{
		"model":  "claude-opus-4-6",
		"system": "claude",
		"messages": []any{
			map[string]any{"role": "user", "content": "Slack に今日のまとめを投稿して"},
		},
		"tools": []any{
			map[string]any{"name": "Read"},
			map[string]any{"name": "Bash"},
			map[string]any{"name": "mcp__slack__post_message", "description": "Post a message to Slack"},
			map[string]any{"name": "mcp__github__create_issue", "description": "Create a GitHub issue"},
		},
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ToolAfter == 0 {
		t.Fatalf("MCP catalog was stripped %+v", stats)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	kept := map[string]bool{}
	for _, rawTool := range got["tools"].([]any) {
		kept[rawTool.(map[string]any)["name"].(string)] = true
	}
	if stats.Chosen == "passthrough" && !kept["mcp__slack__post_message"] {
		t.Fatal("passthrough dropped the Slack MCP tool")
	}
}

// A high-confidence local decision must not cost a Jev round trip.
func TestRewriteSkipsLiveWhenLocalConfident(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake", "answers": map[string]any{}})
	}))
	defer srv.Close()
	client := &jev.Client{APIKey: "test", BaseURL: srv.URL, Model: "fake", HTTP: srv.Client()}
	if !client.Live() {
		t.Fatal("client not live")
	}

	req := map[string]any{
		"model": "grok-4",
		"messages": []any{
			map[string]any{"role": "user", "content": "The auth middleware test is failing. Find it, fix the assertion in place, and re-run the tests."},
		},
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "grep"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "search_replace"}},
			map[string]any{"type": "function", "function": map[string]any{"name": "run_terminal_cmd"}},
		},
	}
	raw, _ := json.Marshal(req)
	_, stats, err := Rewrite(raw, host.Grok, client)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Chosen != "grep" {
		t.Fatalf("chosen %s; want the local decision", stats.Chosen)
	}
	if n := atomic.LoadInt64(&calls); n != 0 {
		t.Fatalf("made %d live Jev requests; want 0", n)
	}
}

func grokFn(name, desc string) any {
	return map[string]any{"type": "function", "function": map[string]any{"name": name, "description": desc}}
}

func grokCatalog() []any {
	sendFeedback := strings.Repeat("Save or update user feedback for later review. Drafts, messages, session, tool output, user request. ", 40)
	return []any{
		grokFn("run_terminal_command", "Run a bash command and return its output."),
		grokFn("read_file", "Read a file from the workspace."),
		grokFn("search_replace", "Replace an exact string in a file."),
		grokFn("list_dir", "List files and directories."),
		grokFn("grep", "Search file contents with regular expressions."),
		grokFn("kill_command_or_subagent", "Terminate a running background task."),
		grokFn("todo_write", "Create and manage a structured task list."),
		grokFn("get_command_or_subagent_output", "Get output from a background task."),
		grokFn("spawn_subagent", "Start a subagent that works on a task independently."),
		grokFn("scheduler_create", "Create a scheduled task."),
		grokFn("scheduler_delete", "Cancel a scheduled task."),
		grokFn("scheduler_list", "List scheduled tasks."),
		grokFn("monitor", "Start a background monitor."),
		grokFn("search_tool", "Search for MCP tools by keyword and retrieve their input schemas."),
		grokFn("use_tool", "Call an MCP integration tool."),
		grokFn("workflow", "Launch or control a workflow."),
		grokFn("enter_plan_mode", "Enter plan mode."),
		grokFn("exit_plan_mode", "Exit plan mode."),
		grokFn("ask_user_question", "Ask the user a multiple-choice question."),
		grokFn("send_feedback", sendFeedback),
		grokFn("web_fetch", "Fetch a URL as markdown."),
		grokFn("image_gen", "Generate an image."),
		grokFn("image_edit", "Edit an image."),
		grokFn("image_to_video", "Generate a video from an image."),
		grokFn("reference_to_video", "Generate a video from references."),
		grokFn("write", "Create or overwrite a file."),
	}
}

func TestGrokPreambleDoesNotPinSendFeedbackAndFitsJevBudget(t *testing.T) {
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, raw)
		var in struct {
			Questions map[string]json.RawMessage `json:"questions"`
		}
		_ = json.Unmarshal(raw, &in)
		answers := map[string]any{}
		if _, ok := in.Questions["next_tool"]; ok {
			answers["next_tool"] = map[string]any{
				"type": "choice", "choice": "search_tool", "confidence": 0.7,
				"probabilities": map[string]float64{"search_tool": 0.7},
			}
		}
		if _, ok := in.Questions["done"]; ok {
			answers["done"] = map[string]any{"type": "noul", "noul": 0.05, "confidence": 0.9}
		}
		for k := range in.Questions {
			if _, ok := answers[k]; !ok {
				answers[k] = map[string]any{"type": "noul", "noul": 0.2, "confidence": 1}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fake", "answers": answers})
	}))
	defer srv.Close()
	client := &jev.Client{APIKey: "test", BaseURL: srv.URL, Model: "fake", HTTP: srv.Client()}

	preamble := strings.Repeat("Keep every explicit requirement of the request in view until it is completed. Match the user's intent. user message session tool output draft feedback review comments. ", 80)
	if len(preamble) < 10_000 {
		t.Fatalf("preamble too short: %d", len(preamble))
	}
	query := "jev-routingを使っている状態です。jev apiを実行できているか、ツールカタログを tool call する処理をキャッシュされないように少しずつ値を変更して20階層浸漬してください。"
	user := preamble + "\n<user_query>\n" + query + "\n</user_query>\n"
	tools := grokCatalog()
	if len(tools) != 26 {
		t.Fatalf("catalog %d", len(tools))
	}
	req := map[string]any{
		"model": "grok-4.6",
		"messages": []any{
			map[string]any{"role": "user", "content": user},
		},
		"tools": tools,
	}
	raw, _ := json.Marshal(req)
	out, stats, err := Rewrite(raw, host.Grok, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) == 0 {
		t.Fatalf("want a live Jev POST so the 32k budget is exercised; chosen=%s", stats.Chosen)
	}
	for i, body := range bodies {
		var posted struct {
			State     json.RawMessage            `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.Unmarshal(body, &posted); err != nil {
			t.Fatalf("body %d: %v", i, err)
		}
		longest := 0
		for _, q := range posted.Questions {
			if n := compact.EstimateTokens(string(q)); n > longest {
				longest = n
			}
		}
		joint := compact.EstimateTokens(string(posted.State)) + longest
		if joint > jev.InputBudget {
			t.Fatalf("body %d joint tokens %d > %d", i, joint, jev.InputBudget)
		}
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	kept, _ := got["tools"].([]any)
	if len(kept) == 1 {
		t0, _ := kept[0].(map[string]any)
		fn, _ := t0["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if name == "" {
			name, _ = t0["name"].(string)
		}
		if name == "send_feedback" {
			t.Fatalf("catalog pinned to send_feedback; stats=%+v", stats)
		}
	}
}
