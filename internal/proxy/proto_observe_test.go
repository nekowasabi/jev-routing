package proxy

import (
	"strings"
	"testing"
)

func TestRewriteCursorAgentHostsReplacesAgentn(t *testing.T) {
	payload := []byte("https://agentn.global.api5.cursor.sh/agent.v1.AgentService/Run")
	body := append([]byte{0x0a, byte(len(payload))}, payload...)
	got := rewriteCursorAgentHosts(body, "127.0.0.1:8787")
	hosts := protoURLHosts(got)
	if len(hosts) != 1 || hosts[0] != "127.0.0.1:8787" {
		t.Fatalf("hosts=%v body=%q", hosts, got)
	}
	if protoURLHosts(body)[0] != "agentn.global.api5.cursor.sh" {
		t.Fatal("fixture host")
	}
}

func TestProtoURLHostsExtractsHostOnly(t *testing.T) {
	// field 1, wire 2, URL with a query token that must not be stored.
	payload := []byte("https://api2.cursor.sh/agent.v1.AgentService/Run?token=SECRET")
	body := []byte{0x0a, byte(len(payload))}
	body = append(body, payload...)
	hosts := protoURLHosts(body)
	if len(hosts) != 1 || hosts[0] != "api2.cursor.sh" {
		t.Fatalf("hosts=%v", hosts)
	}
	joined := strings.Join(hosts, ",")
	if strings.Contains(joined, "SECRET") || strings.Contains(joined, "token") {
		t.Fatalf("leaked %q", hosts)
	}
}
