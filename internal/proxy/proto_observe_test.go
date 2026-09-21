package proxy

import (
	"strings"
	"testing"
)

func TestProtoURLHostsExtractsHostOnly(t *testing.T) {
	// field 1, wire 2, URL with a query token that must not be stored.
	payload := []byte("https://server.codeium.com/exa.api_server_pb.ApiServerService/GetChatMessage?token=SECRET")
	body := []byte{0x0a, byte(len(payload))}
	body = append(body, payload...)
	hosts := protoURLHosts(body)
	if len(hosts) != 1 || hosts[0] != "server.codeium.com" {
		t.Fatalf("hosts=%v", hosts)
	}
	joined := strings.Join(hosts, ",")
	if strings.Contains(joined, "SECRET") || strings.Contains(joined, "token") {
		t.Fatalf("leaked %q", hosts)
	}
}
