package proxy

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestDefaultUpstream(t *testing.T) {
	t.Setenv("ANTHROPIC_UPSTREAM", "")
	t.Setenv("CODEX_UPSTREAM", "")
	t.Setenv("GROK_OAUTH_UPSTREAM", "")
	t.Setenv("CURSOR_UPSTREAM", "")
	t.Setenv("DEVIN_UPSTREAM", "")
	cases := map[host.ID]string{
		host.Claude: "https://api.anthropic.com",
		host.Codex:  "https://chatgpt.com/backend-api/codex",
		host.Grok:   "https://cli-chat-proxy.grok.com",
		host.Cursor: "https://api2.cursor.sh",
		host.Devin:  "https://server.codeium.com",
	}
	for h, want := range cases {
		if got := DefaultUpstream(h); got != want {
			t.Fatalf("%s: %s want %s", h, got, want)
		}
	}
	t.Setenv("CURSOR_UPSTREAM", "https://example.invalid")
	if got := DefaultUpstream(host.Cursor); got != "https://example.invalid" {
		t.Fatalf("override: %s", got)
	}
}

func TestLooksLikeLLM(t *testing.T) {
	yes := []string{
		"/v1/messages", "/v1/chat/completions", "/v1/responses",
		"/aiserver.v1.AgentService/Run", "/agent.v1.AgentService/Run",
		"/v3/organizations/org/sessions", "/v1/inference",
	}
	no := []string{"/healthz", "/stats"}
	for _, p := range yes {
		if !looksLikeLLM(p) {
			t.Fatalf("want true for %s", p)
		}
	}
	for _, p := range no {
		if looksLikeLLM(p) {
			t.Fatalf("want false for %s", p)
		}
	}
}

func TestLooksDevinInference(t *testing.T) {
	yes := []string{
		"/exa.api_server_pb.ApiServerService/GetChatMessage",
		"/exa.api_server_pb.ApiServerService/GetDevstralStream",
	}
	no := []string{
		"/exa.seat_management_pb.SeatManagementService/GetCliTeamSettings",
		"/exa.seat_management_pb.SeatManagementService/GetUserStatus",
		"/healthz",
	}
	for _, p := range yes {
		if !looksDevinInference(p) {
			t.Fatalf("want true for %s", p)
		}
		if looksLikeLLM(p) {
			t.Fatalf("looksLikeLLM must stay false for %s", p)
		}
	}
	for _, p := range no {
		if looksDevinInference(p) {
			t.Fatalf("want false for %s", p)
		}
		if looksLikeLLM(p) {
			t.Fatalf("seat-management must not be LLM rewrite: %s", p)
		}
	}
}

func TestHandlerCanceledContextDoesNotLogDefaultProxyError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer upstream.Close()
	t.Setenv("GROK_OAUTH_UPSTREAM", upstream.URL)

	var defaultBuf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&defaultBuf)
	defer log.SetOutput(orig)

	var proxyLog bytes.Buffer
	srv, err := New("127.0.0.1:0", host.Grok, nil, &proxyLog)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/v1/ping", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if strings.Contains(defaultBuf.String(), "http: proxy error") {
		t.Fatalf("default log leaked proxy error: %q", defaultBuf.String())
	}
}

func TestHandlerNonCancelProxyErrorLogsOnProxyLogger(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()
	t.Setenv("GROK_OAUTH_UPSTREAM", closed.URL)

	var defaultBuf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&defaultBuf)
	defer log.SetOutput(orig)

	var proxyLog bytes.Buffer
	srv, err := New("127.0.0.1:0", host.Grok, nil, &proxyLog)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/ping", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d", rec.Code)
	}
	if !strings.Contains(proxyLog.String(), "proxy error") {
		t.Fatalf("proxy log missing error: %q", proxyLog.String())
	}
}

func TestHandlerNonJSONLLMCountsCharsAndPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("CURSOR_UPSTREAM", upstream.URL)

	srv, err := New("127.0.0.1:0", host.Cursor, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte{0x00, 0x00, 0x00, 0x00, 0x04, 0x01, 0x02, 0x03, 0x04}
	req := httptest.NewRequest(http.MethodPost, "/aiserver.v1.AgentService/Run", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	snap := srv.RunStats()
	if snap["requests"] != 1 {
		t.Fatalf("requests=%v", snap["requests"])
	}
	if snap["rewritten"] != 0 {
		t.Fatalf("rewritten=%v (protobuf cannot be filtered)", snap["rewritten"])
	}
	if snap["passthrough"] != 1 {
		t.Fatalf("passthrough=%v", snap["passthrough"])
	}
	if snap["charsBefore"] != len(body) || snap["charsAfter"] != len(body) {
		t.Fatalf("chars %v→%v want %d", snap["charsBefore"], snap["charsAfter"], len(body))
	}
}

func TestHandlerAgentRunSSENonJSONObserved(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("CURSOR_UPSTREAM", upstream.URL)

	srv, err := New("127.0.0.1:0", host.Cursor, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte{0x00, 0x00, 0x00, 0x00, 0x04, 0x01, 0x02, 0x03, 0x04}
	req := httptest.NewRequest(http.MethodPost, "/agent.v1.AgentService/RunSSE", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	events := srv.RunStats()["events"].([]Event)
	if len(events) != 1 {
		t.Fatalf("events=%d", len(events))
	}
	ev := events[0]
	if ev.ContentType != "application/connect+proto" || ev.Method != http.MethodPost {
		t.Fatalf("observation %+v", ev)
	}
	if ev.Reason != reasonStream {
		t.Fatalf("reason=%s", ev.Reason)
	}
	snap := srv.RunStats()
	if snap["rewritten"] != 0 || snap["passthrough"] != 1 {
		t.Fatalf("stream rewritten as JSON: %+v", snap)
	}
	if snap["requests"] != 1 || snap["reached"] != 1 {
		t.Fatalf("counters %+v", snap)
	}
}

func TestHandlerAgentRunStreamIsNotBuffered(t *testing.T) {
	arrived := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("CURSOR_UPSTREAM", upstream.URL)

	srv, err := New("127.0.0.1:0", host.Cursor, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	req := httptest.NewRequest(http.MethodPost, "/agent.v1.AgentService/Run", pr)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("handler buffered the streaming body")
	}
	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not finish")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandlerGetChatMessageStreamIsNotBuffered(t *testing.T) {
	arrived := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("DEVIN_UPSTREAM", upstream.URL)

	srv, err := New("127.0.0.1:0", host.Devin, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	req := httptest.NewRequest(http.MethodPost, "/exa.api_server_pb.ApiServerService/GetChatMessage", pr)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("handler buffered the GetChatMessage body")
	}
	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not finish")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandlerSeatManagementIsNotLLMRewrite(t *testing.T) {
	arrived := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	t.Setenv("DEVIN_UPSTREAM", upstream.URL)

	srv, err := New("127.0.0.1:0", host.Devin, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	req := httptest.NewRequest(http.MethodPost, "/exa.seat_management_pb.SeatManagementService/GetCliTeamSettings", pr)
	req.Header.Set("Content-Type", "application/connect+proto")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("handler buffered seat-management")
	}
	_ = pw.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not finish")
	}
	snap := srv.RunStats()
	events, _ := snap["events"].([]Event)
	if len(events) != 1 || events[0].Reason != reasonNotLLMPath {
		t.Fatalf("seat-management treated as rewrite: %+v", events)
	}
}

func TestRewriteStreamingAgentURL(t *testing.T) {
	s := &Server{Host: host.Cursor, cursorAgentHost: "agentn.global.api5.cursor.sh"}
	r := httptest.NewRequest(http.MethodPost, "https://api2.cursor.sh/agent.v1.AgentService/Run", nil)
	if !s.rewriteStreamingAgentURL(r) {
		t.Fatal("expected rewrite")
	}
	if r.URL.Scheme != "https" || r.URL.Host != "agentn.global.api5.cursor.sh" || r.Host != "agentn.global.api5.cursor.sh" {
		t.Fatalf("url=%v host=%s", r.URL, r.Host)
	}
	if r.URL.Path != "/agent.v1.AgentService/Run" {
		t.Fatalf("path rewritten: %s", r.URL.Path)
	}
}

func TestHandlerRemembersCursorAgentHostFromProtoResponse(t *testing.T) {
	payload := []byte("https://agentn.global.api5.cursor.sh/agent.v1.AgentService/Run")
	body := append([]byte{0x0a, byte(len(payload))}, payload...)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/proto")
		_, _ = w.Write(body)
	}))
	defer upstream.Close()
	t.Setenv("CURSOR_UPSTREAM", upstream.URL)

	srv, err := New("127.0.0.1:0", host.Cursor, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/aiserver.v1.AiService/GetServerConfig", bytes.NewReader([]byte{0x00}))
	req.Header.Set("Content-Type", "application/proto")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if srv.cursorAgentHost != "agentn.global.api5.cursor.sh" {
		t.Fatalf("cursorAgentHost=%q", srv.cursorAgentHost)
	}
}
