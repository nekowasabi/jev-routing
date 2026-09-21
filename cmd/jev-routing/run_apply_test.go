package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/proxy"
)

func TestRunAppliesSkillOnNormalPath(t *testing.T) {
	jsonBody := []byte(`{"model":"x","messages":[{"role":"user","content":"use the skill-review skill"}],"input":[{"role":"user","content":"use the skill-review skill"}],"tools":[{"name":"skill-review","description":"review skill"},{"type":"function","function":{"name":"skill-review","description":"review skill"}}]}`)
	cases := []struct {
		h       host.ID
		upEnv   string
		bin     string
		baseEnv string
		path    string
		body    []byte
		ct      string
	}{
		{host.Claude, "ANTHROPIC_UPSTREAM", "claude", "ANTHROPIC_BASE_URL", "/v1/messages", jsonBody, "application/json"},
		{host.Codex, "CODEX_UPSTREAM", "codex", "OPENAI_BASE_URL", "/responses", jsonBody, "application/json"},
		{host.Grok, "GROK_OAUTH_UPSTREAM", "grok", "GROK_CLI_CHAT_PROXY_BASE_URL", "/chat/completions", jsonBody, "application/json"},
		{host.Devin, "DEVIN_UPSTREAM", "devin", "DEVIN_API_URL", "/exa.api_server_pb.ApiServerService/GetChatMessage", devinSkillFrame(), "application/connect+proto"},
	}
	for _, tc := range cases {
		t.Run(string(tc.h), func(t *testing.T) {
			var forwarded []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwarded, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()
			dir := t.TempDir()
			framePath := filepath.Join(dir, "frame.bin")
			if err := os.WriteFile(framePath, tc.body, 0o644); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\npython3 - <<'PY'\nimport os,urllib.request\nbase=os.environ['" + tc.baseEnv + "']\npath=os.environ['TEST_PATH']\nurl=base.rstrip('/')+path\nct=os.environ.get('TEST_CT','application/json')\ndata=open(os.environ['TEST_FRAME'],'rb').read()\nreq=urllib.request.Request(url,data=data,headers={'Content-Type':ct},method='POST')\nurllib.request.urlopen(req).read()\nPY\n"
			if err := os.WriteFile(filepath.Join(dir, tc.bin), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			statsPath := filepath.Join(dir, "stats.json")
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("XDG_CACHE_HOME", dir)
			t.Setenv("JEV_LISTEN", "127.0.0.1:0")
			t.Setenv("JEV_RUN_STATS", statsPath)
			t.Setenv("JEV_AUTO_APPLY", "1")
			t.Setenv("JEV_SELECTION_MODE", "local")
			t.Setenv("JEV_APPLICATION_POLICY", proxy.PolicyRequired)
			t.Setenv("JEV_ROUTING_MODE", "filter")
			t.Setenv("TYPESAFE_API_KEY", "")
			t.Setenv("JEV_API_KEY", "")
			t.Setenv(tc.upEnv, upstream.URL)
			t.Setenv("TEST_FRAME", framePath)
			t.Setenv("TEST_CT", tc.ct)
			t.Setenv("TEST_PATH", tc.path)
			if code := cmdRun([]string{string(tc.h)}); code != 0 {
				t.Fatalf("run exit=%d forwarded=%s", code, forwarded)
			}
			if !strings.Contains(string(forwarded), "jev-routing context") && !strings.Contains(string(forwarded), "review skill") {
				t.Fatalf("run path did not apply skill context: %s", forwarded)
			}
			raw, err := os.ReadFile(statsPath)
			if err != nil {
				t.Fatal(err)
			}
			var snap map[string]any
			if err := json.Unmarshal(raw, &snap); err != nil {
				t.Fatal(err)
			}
			if s, _ := snap["lastDelivered"].(string); s == "" {
				t.Fatalf("stats missing lastDelivered: %s", raw)
			}
		})
	}
}

func devinSkillFrame() []byte {
	raw, _ := json.Marshal(map[string]any{
		"prompt": "use the skill-review skill",
		"tools":  []any{map[string]any{"name": "skill-review", "description": "review skill"}},
	})
	return connectFrameForTest(protoLD(1, raw))
}

func connectFrameForTest(payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out
}

func protoLD(field int, raw []byte) []byte {
	tag := protoVarintForTest(uint64(field)<<3 | 2)
	return append(append(tag, protoVarintForTest(uint64(len(raw)))...), raw...)
}

func protoVarintForTest(x uint64) []byte {
	var b []byte
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}
