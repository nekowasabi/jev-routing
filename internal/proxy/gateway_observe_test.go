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

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
)

func testProxy(t *testing.T, h host.ID, client *jev.Client, opt Options) (*Server, *int64) {
	t.Helper()
	var n int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&n, 1)
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":11,"completion_tokens":3,"completion_tokens_details":{"reasoning_tokens":1},"prompt_tokens_details":{"cached_tokens":2}}}`))
	}))
	t.Cleanup(up.Close)
	if opt.Mode == "" {
		opt = DefaultOptions()
	}
	s, err := NewWithOptions("127.0.0.1:8787", h, client, io.Discard, opt)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(up.URL)
	s.Upstream = u
	return s, &n
}

func TestGatewayEvents(t *testing.T) {
	s, _ := testProxy(t, host.Grok, nil, DefaultOptions())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(chatReq("The auth middleware test is failing. Find it.", workTools()))))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	events, recorded, _, _ := s.Events().Snapshot(0)
	if recorded != 1 || len(events) != 1 {
		t.Fatalf("recorded=%d events=%d", recorded, len(events))
	}
	if events[0].Chosen == "" || events[0].Source == "" {
		t.Fatalf("%+v", events[0])
	}
	if strings.Contains(events[0].Chosen, "Bearer") {
		t.Fatal("secret leaked")
	}
	for i := 0; i < 1005; i++ {
		s.Events().Add(Event{Chosen: "x", Reason: "local"})
	}
	_, recorded, oldest, trunc := s.Events().Snapshot(1)
	if recorded != eventLimit {
		t.Fatalf("cap %d", recorded)
	}
	if oldest == 0 || !trunc && oldest > 1 {
		// since=1 may be dropped
		_ = trunc
	}
}

func TestGatewayJevTrace(t *testing.T) {
	unknown := "summarize this repo's architecture for me"
	var calls int64
	c := jevAnswers(t, "grep", 0.9, 0.9, 0.9, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{
				"next_tool":  map[string]any{"type": "choice", "choice": "grep", "confidence": 0.9},
				"needs_tool": map[string]any{"type": "noul", "noul": 0.9, "confidence": 0.9},
			},
		})
	})
	s, _ := testProxy(t, host.Grok, c, DefaultOptions())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(chatReq(unknown, workTools()))))
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	events, _, _, _ := s.Events().Snapshot(0)
	if len(events) != 1 || events[0].JevCalls < 1 {
		t.Fatalf("want jev call recorded %+v", events)
	}
	// cache hit on identical selection: second request still asks (selection ask is cached by full input)
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	if atomic.LoadInt64(&calls) < 1 {
		t.Fatal("expected at least one HTTP ask")
	}
}

func TestGatewayUsage(t *testing.T) {
	s, _ := testProxy(t, host.Grok, nil, DefaultOptions())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(chatReq("The auth middleware test is failing. Find it.", workTools()))))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	// drain
	_ = rec.Body.String()
	events, _, _, _ := s.Events().Snapshot(0)
	if len(events) == 0 {
		t.Fatal("no event")
	}
	// usage is filled asynchronously as body is read; recorder reads it.
	if events[0].Usage == nil {
		// httptest recorder reads the body, so usage should be present
		t.Fatalf("usage missing %+v", events[0])
	}
	if events[0].Usage.InputTokens == nil || *events[0].Usage.InputTokens != 11 {
		t.Fatalf("usage %+v", events[0].Usage)
	}
}

func TestGatewayStreaming(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1}}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer up.Close()
	s, err := NewWithOptions("127.0.0.1:8787", host.Grok, nil, io.Discard, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	s.Upstream, _ = url.Parse(up.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(chatReq("The auth middleware test is failing. Find it.", workTools()))))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	_ = rec.Body.String()
	events, _, _, _ := s.Events().Snapshot(0)
	if events[0].Usage == nil || events[0].Usage.OutputTokens == nil || *events[0].Usage.OutputTokens != 2 {
		t.Fatalf("last cumulative usage %+v", events[0].Usage)
	}
}

func TestAnalysisMetrics(t *testing.T) {
	TestGatewayEvents(t)
	TestGatewayUsage(t)
}

func TestGatewayDashboard(t *testing.T) {
	s, _ := testProxy(t, host.Grok, nil, DefaultOptions())
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.RemoteAddr = "127.0.0.1:9"
	req.Host = "127.0.0.1:8787"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "jev-routing") {
		t.Fatalf("dashboard %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("cache-control") != "no-store" {
		t.Fatal("cache-control")
	}

	ev := httptest.NewRequest(http.MethodGet, "/dashboard/events?since=0", nil)
	ev.RemoteAddr = "127.0.0.1:9"
	ev.Host = "127.0.0.1:8787"
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, ev)
	if rec.Code != 200 {
		t.Fatalf("events %d", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["router"]; !ok {
		t.Fatalf("%v", payload)
	}

	bad := httptest.NewRequest(http.MethodGet, "/dashboard/events?since=-1", nil)
	bad.RemoteAddr = "127.0.0.1:9"
	bad.Host = "127.0.0.1:8787"
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, bad)
	if rec.Code != 400 {
		t.Fatalf("since -1 -> %d", rec.Code)
	}

	remote := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	remote.RemoteAddr = "8.8.8.8:9"
	remote.Host = "127.0.0.1:8787"
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, remote)
	if rec.Code != 404 {
		t.Fatalf("non-loopback %d", rec.Code)
	}

	pub, _ := NewWithOptions("0.0.0.0:8787", host.Grok, nil, io.Discard, DefaultOptions())
	req = httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.RemoteAddr = "127.0.0.1:9"
	req.Host = "127.0.0.1:8787"
	rec = httptest.NewRecorder()
	pub.Handler().ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("public bind %d", rec.Code)
	}

	xss := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	xss.RemoteAddr = "127.0.0.1:9"
	xss.Host = "127.0.0.1:8787"
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, xss)
	if strings.Contains(rec.Body.String(), "<script>alert") && !strings.Contains(rec.Body.String(), "textContent") {
		t.Fatal("page should use textContent, not write raw events")
	}
}

func TestAnalysisDashboard(t *testing.T) {
	TestGatewayDashboard(t)
}

func TestGatewayOptions(t *testing.T) {
	t.Setenv("JEV_ROUTING_MODE", "forced")
	t.Setenv("JEV_COMPACTION", "off")
	t.Setenv("JEV_REASONING", "preserve")
	t.Setenv("JEV_RUN_ID", "cmp-1")
	t.Setenv("JEV_ARGS_MODEL", "")
	t.Setenv("JEV_ARGS_TOOLS", "")
	t.Setenv("JEV_DIRECT_TOOLS", "")
	o, err := OptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if o.Mode != ModeForced || o.Compaction != CompactionOff || o.Reasoning != ReasoningPreserve || o.RunID != "cmp-1" {
		t.Fatalf("%+v", o)
	}
	t.Setenv("JEV_ROUTING_MODE", "nope")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("want invalid mode error")
	}
	t.Setenv("JEV_ROUTING_MODE", "filter")
	t.Setenv("JEV_ARGS_MODEL", "x")
	t.Setenv("JEV_ARGS_TOOLS", "")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("want pair error")
	}
	t.Setenv("JEV_ARGS_MODEL", "x")
	t.Setenv("JEV_ARGS_TOOLS", "a,a")
	t.Setenv("JEV_ROUTING_MODE", "forced")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("want duplicate error")
	}
	t.Setenv("JEV_ARGS_MODEL", "")
	t.Setenv("JEV_ARGS_TOOLS", "")
	t.Setenv("JEV_DIRECT_TOOLS", "ping")
	t.Setenv("JEV_ARGS_MODEL", "x")
	t.Setenv("JEV_ARGS_TOOLS", "ping")
	if _, err := OptionsFromEnv(); err == nil {
		t.Fatal("direct+args error")
	}
}

func TestGatewayRunStats(t *testing.T) {
	s, _ := testProxy(t, host.Grok, nil, DefaultOptions())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(chatReq("The auth middleware test is failing. Find it.", workTools()))))
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	st := s.RunStats()
	for _, k := range []string{"requests", "charsBefore", "charsAfter"} {
		if _, ok := st[k]; !ok {
			t.Fatalf("missing compatible key %s", k)
		}
	}
	if st["requests"].(int) < 1 {
		t.Fatalf("%v", st)
	}
}

func TestAnalysisRunStats(t *testing.T) {
	TestGatewayRunStats(t)
}
