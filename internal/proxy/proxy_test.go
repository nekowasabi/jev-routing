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
	t.Setenv("DEVIN_UPSTREAM", "")
	cases := map[host.ID]string{
		host.Claude: "https://api.anthropic.com",
		host.Codex:  "https://chatgpt.com/backend-api/codex",
		host.Grok:   "https://cli-chat-proxy.grok.com",
		host.Devin:  "https://server.codeium.com",
	}
	for h, want := range cases {
		if got := DefaultUpstream(h); got != want {
			t.Fatalf("%s: %s want %s", h, got, want)
		}
	}
	t.Setenv("DEVIN_UPSTREAM", "https://example.invalid")
	if got := DefaultUpstream(host.Devin); got != "https://example.invalid" {
		t.Fatalf("override: %s", got)
	}
}

func TestLooksLikeLLM(t *testing.T) {
	yes := []string{
		"/v1/messages", "/v1/chat/completions", "/v1/responses",
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
