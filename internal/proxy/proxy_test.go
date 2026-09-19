package proxy

import (
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

