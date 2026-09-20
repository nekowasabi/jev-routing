package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/plan"
)

type fakeExec struct {
	mu     sync.Mutex
	skills []string
	calls  []HostCall
	nextID string
	err    error
}

func (f *fakeExec) DeliverSkill(decisionID, body, source string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skills = append(f.skills, source+"\n"+body)
	return f.err
}

func (f *fakeExec) StartCall(call HostCall) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	if f.err != nil {
		return "", f.err
	}
	if f.nextID == "" {
		f.nextID = "call-1"
	}
	return f.nextID, nil
}

func lifecycleCatalog(t *testing.T) plan.Catalog {
	t.Helper()
	cat, err := plan.BuildCatalog(plan.Inventory{
		Skills: []plan.SkillIn{{Name: "review", Provider: "test", Version: "1", BodyRef: "skill://review/SKILL.md", Available: true, Explicit: true, Description: "review"}},
		CLIs:   []plan.CLIIn{{Name: "rg", Provider: "test", Version: "1", Command: "rg", Args: []string{"--json"}, Available: true, Description: "search"}},
		Plugins: []plan.PluginIn{{
			Name: "devtools", Provider: "test", Version: "1", Available: true,
			Children: []plan.Capability{{
				Kind: plan.KindMCP, Name: "lookup", Version: "1",
				Target: plan.Target{MCPConn: "snap:dev", MCPTool: "lookup"},
			}},
		}},
	}, host.Claude, &plan.LaunchProbe{})
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func TestAutoApplyUsesRequestCatalog(t *testing.T) {
	s := &Server{
		Host: host.Claude,
		Options: Options{AutoApply: true, ApplicationPolicy: PolicyRequired, KindModes: map[string]string{
			"skill": KindApply, "mcp_tool": KindApply, "cli": KindApply, "plugin": KindApply,
		}},
		SkillBodies: map[string]string{"host-skill:skill-review": "# review"},
		Apps:        NewAppStore(),
		Executor:    &recordingExec{},
	}
	body := []byte(`{"messages":[{"role":"user","content":"use the skill-review skill"}],"tools":[{"name":"skill-review","description":"review skill"}]}`)
	s.autoApply(body)
	if s.LastDelivered == "" || !strings.Contains(s.LastDelivered, "review") {
		t.Fatalf("expected skill delivery from request catalog, applyErr=%q delivered=%q", s.ApplyErr, s.LastDelivered)
	}
	out, err := ApplyHostContext(host.Claude, body, s.LastDelivered)
	if err != nil || !strings.Contains(string(out), "review") {
		t.Fatalf("writeback %s %v", out, err)
	}
}

func TestPreviousResponseIDDoesNotCountAsApplied(t *testing.T) {
	cliCat, err := plan.BuildCatalog(plan.Inventory{
		CLIs: []plan.CLIIn{{Name: "rg", Provider: "test", Version: "1", Command: "rg", Args: []string{"--json"}, Available: true, Description: "search"}},
	}, host.Codex, &plan.LaunchProbe{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		Host:     host.Codex,
		Options:  Options{AutoApply: true, ApplicationPolicy: PolicyRequired, KindModes: map[string]string{"cli": KindApply}},
		Catalog:  cliCat,
		Apps:     NewAppStore(),
		Executor: &recordingExec{},
	}
	body := []byte(`{"model":"x","previous_response_id":"resp_1","input":[{"role":"user","content":"search files with rg"}],"tools":[{"name":"rg","description":"search files"}]}`)
	s.autoApply(body)
	if s.ApplyErr == "" || !strings.Contains(s.ApplyErr, "previous_response_id") {
		t.Fatalf("required must not treat a blocked Codex call as applied: %q", s.ApplyErr)
	}
}

func TestObserveHostCallCorrelatesResult(t *testing.T) {
	store := NewAppStore()
	app := &Application{DecisionID: "d1", CallID: "call-9", State: AppStarted, Kind: plan.KindCLI}
	store.put(app)
	if err := ObserveHostCall(store, "call-9", `{"ok":true}`, 0); err != nil {
		t.Fatal(err)
	}
	got := store.Get("d1")
	if got.State != AppVerified {
		t.Fatalf("%+v", got)
	}
}

func TestObserveHostCallMatchesUniqueStarted(t *testing.T) {
	store := NewAppStore()
	store.put(&Application{DecisionID: "d1", CallID: "call-1", State: AppStarted, Kind: plan.KindCLI})
	if err := ObserveHostCall(store, "exec-2", `{"ok":true}`, 0); err != nil {
		t.Fatal(err)
	}
	if got := store.Get("d1"); got.State != AppVerified {
		t.Fatalf("%+v", got)
	}
}

func TestAutoApplyEmptyUserDoesNotKeepDelivery(t *testing.T) {
	s := &Server{
		Host:          host.Grok,
		LastDelivered: "stale",
		Options:       Options{AutoApply: true, ApplicationPolicy: PolicyRequired, KindModes: map[string]string{"skill": KindApply}},
		Apps:          NewAppStore(),
		Executor:      &recordingExec{},
	}
	s.autoApply([]byte(`{"model":"grok"}`))
	if s.LastDelivered != "" || s.ApplyErr != "" {
		t.Fatalf("empty user must not keep or apply: delivered=%q err=%q", s.LastDelivered, s.ApplyErr)
	}
}

func TestAutoApplyUsesSkillDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review", "SKILL.md"), []byte("# review\ncheck the live diff"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JEV_SKILL_DIR", dir)
	opt := localOpt()
	opt.AutoApply = true
	opt.ApplicationPolicy = PolicyRequired
	opt.KindModes = map[string]string{"skill": KindApply}
	s, err := NewWithOptions("127.0.0.1:0", host.Claude, nil, io.Discard, opt)
	if err != nil {
		t.Fatal(err)
	}
	s.autoApply([]byte(`{"messages":[{"role":"user","content":"use the review skill"}]}`))
	if s.LastDelivered == "" || !strings.Contains(s.LastDelivered, "check the live diff") {
		t.Fatalf("skill dir must deliver body: applyErr=%q delivered=%q", s.ApplyErr, s.LastDelivered)
	}
}

func TestHandlerObservesResponseToolCallAndResult(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"tool_use","id":"call_live","name":"lookup"}]}`))
	}))
	defer upstream.Close()
	t.Setenv("ANTHROPIC_UPSTREAM", upstream.URL)
	opt := localOpt()
	srv, err := NewWithOptions("127.0.0.1:0", host.Claude, nil, io.Discard, opt)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude","messages":[{"role":"user","content":"look this up"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	got := srv.Apps.Get("call_live")
	if got == nil || got.State != AppStarted {
		t.Fatalf("response tool_use not started: %+v", got)
	}
	req2 := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_live","content":"{\"ok\":true}"}]}]}`))
	req2.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(httptest.NewRecorder(), req2)
	got = srv.Apps.Get("call_live")
	if got == nil || got.State != AppVerified || !strings.Contains(got.Result, "ok") {
		t.Fatalf("tool result not verified: %+v", got)
	}
}

func TestHandlerObservesClaudeSSEContentBlockToolUse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_bash\",\"name\":\"Bash\"}}\n\n"))
	}))
	defer upstream.Close()
	t.Setenv("ANTHROPIC_UPSTREAM", upstream.URL)
	srv, err := NewWithOptions("127.0.0.1:0", host.Claude, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude","messages":[{"role":"user","content":"echo hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	got := srv.Apps.Get("toolu_bash")
	if got == nil || got.State != AppStarted {
		t.Fatalf("sse content_block tool_use not started: %+v", got)
	}
	req2 := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bash","content":"jev-live-cli-ok"}]}]}`))
	req2.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(httptest.NewRecorder(), req2)
	got = srv.Apps.Get("toolu_bash")
	if got == nil || got.State != AppVerified || !strings.Contains(got.Result, "jev-live-cli-ok") {
		t.Fatalf("sse tool result not verified: %+v", got)
	}
}

func TestHandlerObservesResponsesFunctionCallAndResult(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_exec\",\"name\":\"exec\"}}\n\n"))
	}))
	defer upstream.Close()
	t.Setenv("CODEX_UPSTREAM", upstream.URL)
	srv, err := NewWithOptions("127.0.0.1:0", host.Codex, nil, io.Discard, localOpt())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt","input":[{"role":"user","content":"echo hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	got := srv.Apps.Get("call_exec")
	if got == nil || got.State != AppStarted {
		t.Fatalf("responses function_call not started: %+v", got)
	}
	req2 := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt","input":[{"type":"function_call_output","call_id":"call_exec","output":"jev-live-cli-ok"}]}`))
	req2.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(httptest.NewRecorder(), req2)
	got = srv.Apps.Get("call_exec")
	if got == nil || got.State != AppVerified || !strings.Contains(got.Result, "jev-live-cli-ok") {
		t.Fatalf("responses result not verified: %+v", got)
	}
}

func TestObserveConnectResponseProtoJSONCall(t *testing.T) {
	s := &Server{Host: host.Cursor, Apps: NewAppStore(), Executor: &recordingExec{}}
	inner := []byte(`{"type":"function_call","call_id":"call_proto","name":"exec"}`)
	s.observeConnectStream(connectFrame(0, protoBytes(1, inner)))
	got := s.Apps.Get("call_proto")
	if got == nil || got.State != AppStarted {
		t.Fatalf("proto json call not started: %+v", got)
	}
	s.observeConnectStream(connectFrame(0, protoBytes(1, []byte(`{"type":"function_call_output","call_id":"call_proto","output":"jev-live-cli-ok"}`))))
	got = s.Apps.Get("call_proto")
	if got == nil || got.State != AppVerified || !strings.Contains(got.Result, "jev-live-cli-ok") {
		t.Fatalf("proto json result not verified: %+v", got)
	}
}

func TestObserveConnectGzipFunctionCall(t *testing.T) {
	s := &Server{Host: host.Cursor, Apps: NewAppStore(), Executor: &recordingExec{}}
	inner := protoBytes(1, []byte(`{"type":"function_call","call_id":"call_gz","name":"exec"}`))
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(inner); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	s.observeConnectStream(connectFrame(connectFlagCompressed, buf.Bytes()))
	got := s.Apps.Get("call_gz")
	if got == nil || got.State != AppStarted {
		t.Fatalf("gzip proto json call not started: %+v", got)
	}
}

func TestObserveJSONIgnoresHostMetaCalls(t *testing.T) {
	s := &Server{Host: host.Grok, Apps: NewAppStore(), Executor: &recordingExec{}}
	s.observeJSONCalls([]byte(`{"input":[{"type":"function_call","call_id":"call_meta","name":"session_title"}]}`))
	if got := s.Apps.Get("call_meta"); got != nil {
		t.Fatalf("host meta call must not start an application: %+v", got)
	}
}

func TestObserveJSONStartsAndVerifiesCall(t *testing.T) {
	s := &Server{Host: host.Claude, Apps: NewAppStore(), Executor: &recordingExec{}}
	body := []byte(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_a","name":"lookup"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_a","content":"{\"ok\":true}"}]}]}`)
	s.observeHostFrames(body)
	got := s.Apps.Get("call_a")
	if got == nil || got.State != AppVerified || !strings.Contains(got.Result, "ok") {
		t.Fatalf("observed call was not verified: %+v", got)
	}
}

func TestAutoApplyGenericRequestDoesNotFailRequired(t *testing.T) {
	s := &Server{
		Host: host.Claude,
		Options: Options{AutoApply: true, ApplicationPolicy: PolicyRequired, KindModes: map[string]string{
			"skill": KindApply, "cli": KindApply, "mcp_tool": KindApply,
		}},
		Apps:     NewAppStore(),
		Executor: &recordingExec{},
	}
	s.autoApply([]byte(`{"messages":[{"role":"user","content":"say pong"}],"tools":[{"name":"Read","description":"read a file"},{"name":"exec","description":"run a command"}]}`))
	if s.ApplyErr != "" {
		t.Fatalf("required must not fail a request that named no capability: %q", s.ApplyErr)
	}
	if s.LastDelivered != "" {
		t.Fatalf("unexpected delivery %q", s.LastDelivered)
	}
	if n := len(s.Apps.Snapshot()); n != 0 {
		t.Fatalf("local tool pick must not start a host call: %+v", s.Apps.Snapshot())
	}
}

func TestAutoApplyReadsActionUserText(t *testing.T) {
	s := &Server{
		Host: host.Cursor,
		Options: Options{AutoApply: true, ApplicationPolicy: PolicyRequired, KindModes: map[string]string{
			"skill": KindApply,
		}},
		Apps:     NewAppStore(),
		Executor: &recordingExec{},
	}
	body := []byte(`{"action":{"userMessageAction":{"userMessage":{"text":"use the skill-review skill"}}},"tools":[{"name":"skill-review","description":"review skill"}]}`)
	s.autoApply(body)
	if s.LastDelivered == "" || !strings.Contains(s.LastDelivered, "review") {
		t.Fatalf("action user text must select the advertised skill, applyErr=%q delivered=%q", s.ApplyErr, s.LastDelivered)
	}
}

func TestAutomaticApplicationLifecycle(t *testing.T) {
	cat := lifecycleCatalog(t)
	bodies := map[string]string{"skill://review/SKILL.md": "# review\ncheck the diff"}
	skillID := plan.CapabilityID(plan.KindSkill, "test", "review", "1")
	cliID := plan.CapabilityID(plan.KindCLI, "test", "rg", "1")
	pluginID := plan.CapabilityID(plan.KindPlugin, "test", "devtools", "1")

	t.Run("skill-delivery", func(t *testing.T) {
		store, exec := NewAppStore(), &fakeExec{}
		route := plan.Route(plan.RouteRequest{RequestID: "skill-1", Text: "use review skill", Host: host.Claude, Catalog: cat, Explicit: []string{skillID}}, nil)
		app, err := Apply(store, route, cat, bodies, nil, exec)
		if err != nil || app.State != AppDelivered || app.DeliveredHash == "" || len(exec.skills) != 1 {
			t.Fatalf("deliver %+v err=%v skills=%v", app, err, exec.skills)
		}
		if !Success(app) || app.State == AppVerified && app.Kind == plan.KindSkill && app.Result == "" {
			// delivery is application evidence; task success stays separate
		}
		if !Success(app) {
			t.Fatal("skill delivery must count as applied")
		}
	})

	t.Run("selection-only-not-success", func(t *testing.T) {
		route := plan.RouteResult{DecisionID: "sel", Outcome: plan.RouteSelected, CapabilityID: skillID}
		if Success(&Application{DecisionID: route.DecisionID, State: AppSelected, CapabilityID: skillID, Kind: plan.KindSkill}) {
			t.Fatal("selected without delivery is not success")
		}
	})

	t.Run("plugin-resolves-child-mcp", func(t *testing.T) {
		store, exec := NewAppStore(), &fakeExec{nextID: "mcp-1"}
		route := plan.Route(plan.RouteRequest{RequestID: "plug-1", Text: "lookup", Host: host.Claude, Catalog: cat, Explicit: []string{pluginID}}, nil)
		app, err := Apply(store, route, cat, bodies, nil, exec)
		if err != nil || app.State != AppStarted || app.CallID != "mcp-1" || len(exec.calls) != 1 || exec.calls[0].Name != "lookup" {
			t.Fatalf("plugin %+v err=%v calls=%+v", app, err, exec.calls)
		}
		zero := 0
		if err := ObserveResult(store, app.DecisionID, "mcp-1", `{"ok":true}`, &zero); err != nil || store.Get(app.DecisionID).State != AppVerified {
			t.Fatalf("result %v %+v", err, store.Get(app.DecisionID))
		}
	})

	t.Run("cli-json-command-and-mismatch", func(t *testing.T) {
		store, exec := NewAppStore(), &fakeExec{nextID: "cli-1"}
		route := plan.Route(plan.RouteRequest{RequestID: "cli-1", Text: "search", Host: host.Claude, Catalog: cat, Explicit: []string{cliID}}, nil)
		app, err := Apply(store, route, cat, bodies, []string{"rg", "--json", "foo"}, exec)
		if err != nil || app.State != AppStarted {
			t.Fatalf("cli %+v err=%v", app, err)
		}
		zero := 0
		if err := ObserveResult(store, app.DecisionID, "cli-1", `{"ok":true}`, &zero); err != nil {
			t.Fatal(err)
		}
		store2, exec2 := NewAppStore(), &fakeExec{}
		if _, err := Apply(store2, route, cat, bodies, []string{"bash", "-c", "rm -rf /"}, exec2); err == nil || len(exec2.calls) != 0 {
			t.Fatal("wrong command was executed")
		}
	})

	t.Run("missing-result-not-verified", func(t *testing.T) {
		store, exec := NewAppStore(), &fakeExec{nextID: "c2"}
		route := plan.Route(plan.RouteRequest{RequestID: "miss-1", Text: "search", Host: host.Claude, Catalog: cat, Explicit: []string{cliID}}, nil)
		app, err := Apply(store, route, cat, bodies, []string{"rg", "--json"}, exec)
		if err != nil {
			t.Fatal(err)
		}
		if err := ObserveResult(store, app.DecisionID, "c2", "", nil); err == nil || store.Get(app.DecisionID).State == AppVerified {
			t.Fatal("empty result verified")
		}
	})

	t.Run("incomplete-stream-not-started", func(t *testing.T) {
		store, exec := NewAppStore(), &fakeExec{}
		item, _ := plan.Lookup(cat, plan.CapabilityID(plan.KindMCP, "devtools", "lookup", "1"))
		route := plan.RouteResult{DecisionID: "stream-1", Outcome: plan.RouteSelected, CapabilityID: item.ID}
		if _, err := StartStreamCall(store, route, item, exec, false, "lookup"); err == nil || len(exec.calls) != 0 {
			t.Fatal("incomplete stream executed")
		}
	})

	t.Run("replay-does-not-double-start", func(t *testing.T) {
		store, exec := NewAppStore(), &fakeExec{nextID: "once"}
		route := plan.Route(plan.RouteRequest{RequestID: "rep-1", Text: "search", Host: host.Claude, Catalog: cat, Explicit: []string{cliID}}, nil)
		if _, err := Apply(store, route, cat, bodies, []string{"rg", "--json"}, exec); err != nil {
			t.Fatal(err)
		}
		again, err := Apply(store, route, cat, bodies, []string{"rg", "--json"}, exec)
		if err != nil || again.State != AppStarted || len(exec.calls) != 1 {
			t.Fatalf("double start %+v err=%v n=%d", again, err, len(exec.calls))
		}
	})

	t.Run("new-same-text-is-new-decision", func(t *testing.T) {
		a := plan.Route(plan.RouteRequest{RequestID: "n1", Text: "same text", Host: host.Claude, Catalog: cat, Explicit: []string{skillID}}, nil)
		b := plan.Route(plan.RouteRequest{RequestID: "n2", Text: "same text", Host: host.Claude, Catalog: cat, Explicit: []string{skillID}}, nil)
		if a.DecisionID == b.DecisionID {
			t.Fatal("new request reused decision")
		}
	})

	t.Run("version-change-blocks", func(t *testing.T) {
		route := plan.Route(plan.RouteRequest{RequestID: "ver", Text: "x", Host: host.Claude, Catalog: cat, ExpectedRevision: "old", Explicit: []string{skillID}}, nil)
		if route.ReasonCode != plan.ReasonVersionChanged {
			t.Fatalf("%+v", route)
		}
		store, exec := NewAppStore(), &fakeExec{}
		if _, err := Apply(store, route, cat, bodies, nil, exec); err == nil {
			t.Fatal("applied stale revision")
		}
		if len(exec.skills) != 0 {
			t.Fatal("stale skill delivered")
		}
	})

	t.Run("explicit-skill-kept", func(t *testing.T) {
		route := plan.Route(plan.RouteRequest{RequestID: "ex", Text: "ignore other tools", Host: host.Claude, Catalog: cat, Explicit: []string{skillID}}, func(string, map[string]string) (string, float64, error) {
			t.Fatal("explicit must not ask Jev")
			return "", 0, nil
		})
		if route.CapabilityID != skillID || route.ReasonCode != plan.ReasonExplicit {
			t.Fatalf("%+v", route)
		}
	})

	t.Run("no-match-not-applied", func(t *testing.T) {
		route := plan.Route(plan.RouteRequest{RequestID: "nm", Text: "???", Host: host.Claude, Catalog: cat, NewRequest: true}, func(string, map[string]string) (string, float64, error) {
			return plan.NoMatchID, 0.9, nil
		})
		store, exec := NewAppStore(), &fakeExec{}
		if _, err := Apply(store, route, cat, bodies, nil, exec); err == nil || len(exec.calls)+len(exec.skills) != 0 {
			t.Fatal("no_match applied")
		}
	})
}

func TestAutomaticApplicationJSONRoundTrip(t *testing.T) {
	raw, _ := json.Marshal(Application{DecisionID: "d", State: AppDelivered, Kind: plan.KindSkill})
	if !json.Valid(raw) {
		t.Fatal(string(raw))
	}
}

func TestHandlerRequiredSkillWriteback(t *testing.T) {
	opt := localOpt()
	opt.AutoApply = true
	opt.ApplicationPolicy = PolicyRequired
	opt.KindModes = map[string]string{"skill": KindApply}
	cases := []struct {
		host host.ID
		env  string
		path string
		body []byte
	}{
		{host.Claude, "ANTHROPIC_UPSTREAM", "/v1/messages", []byte(`{"model":"claude","messages":[{"role":"user","content":"use the skill-review skill"}],"tools":[{"name":"skill-review","description":"review skill"}]}`)},
		{host.Codex, "CODEX_UPSTREAM", "/v1/responses", []byte(`{"model":"x","input":[{"role":"user","content":"use the skill-review skill"}],"tools":[{"name":"skill-review","description":"review skill"}]}`)},
		{host.Grok, "GROK_OAUTH_UPSTREAM", "/v1/chat/completions", []byte(`{"model":"grok","messages":[{"role":"user","content":"use the skill-review skill"}],"tools":[{"type":"function","function":{"name":"skill-review","description":"review skill"}}]}`)},
	}
	for _, tc := range cases {
		t.Run(string(tc.host), func(t *testing.T) {
			var forwarded []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwarded, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()
			t.Setenv(tc.env, upstream.URL)
			srv, err := NewWithOptions("127.0.0.1:0", tc.host, nil, io.Discard, opt)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d", rec.Code)
			}
			if !bytes.Contains(forwarded, []byte("jev-routing context")) {
				t.Fatalf("upstream missing applied skill context: %s", forwarded)
			}
			if srv.LastDelivered == "" {
				t.Fatal("skill was not delivered on the run path")
			}
		})
	}
}
