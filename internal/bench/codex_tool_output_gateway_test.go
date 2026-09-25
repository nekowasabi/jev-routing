package bench

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

// TestCodexToolOutputMaxAppliesThroughGatewayEnv reproduces the exact
// gatewayEnv/withEnv/startGateway wiring run.go uses for
// --codex-tool-output-truncate "on": it verifies JEV_CODEX_TOOL_OUTPUT_TRUNCATE
// actually reaches the gateway's proxy.Options and truncates a realistic
// custom_tool_call_output payload (array-of-content-items shape, confirmed
// against live Codex CLI 0.158 traffic), not just internal/proxy's own unit
// tests.
func TestCodexToolOutputMaxAppliesThroughGatewayEnv(t *testing.T) {
	var forwarded []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		forwarded = body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n")
	}))
	defer upstream.Close()

	gatewayEnv := map[string]string{
		"JEV_CODEX_TOOL_OUTPUT_TRUNCATE": "on",
		"CODEX_UPSTREAM":                 upstream.URL,
	}
	var gw *gateway
	err := withEnv(gatewayEnv, func() error {
		var startErr error
		gw, startErr = startGateway(host.Codex, "127.0.0.1:0", "baseline", "gateway-env-test", io.Discard, nil)
		return startErr
	})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()

	big := strings.Repeat("a", 36935)
	body := map[string]any{
		"model": "gpt-5.6-terra",
		"input": []any{
			map[string]any{"type": "custom_tool_call", "call_id": "c1", "name": "exec", "input": "cat big.txt"},
			map[string]any{"type": "custom_tool_call_output", "call_id": "c1", "output": []any{
				map[string]any{"type": "input_text", "text": "Ran `cat big.txt`"},
				map[string]any{"type": "input_text", "text": big},
			}},
		},
	}
	raw, _ := json.Marshal(body)
	u, _ := url.Parse(gw.origin)
	resp, err := http.Post(gw.origin+u.Path+"/v1/responses", "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if len(forwarded) == 0 {
		t.Fatal("nothing forwarded upstream")
	}
	var got map[string]any
	if err := json.Unmarshal(forwarded, &got); err != nil {
		t.Fatal(err)
	}
	items := got["input"].([]any)
	out := items[1].(map[string]any)["output"].([]any)
	text := out[1].(map[string]any)["text"].(string)
	if len(text) >= len(big) {
		t.Fatalf("not truncated: forwarded len=%d, original len=%d", len(text), len(big))
	}
	if !strings.Contains(text, "jev-routing: omitted") {
		t.Fatalf("truncated but missing note: %s", text)
	}
}
