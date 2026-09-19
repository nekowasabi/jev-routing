package proxy

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		host.Devin:  "https://api.devin.ai",
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
	yes := []string{"/v1/messages", "/v1/chat/completions", "/v1/responses", "/aiserver.v1.AgentService/Run"}
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
