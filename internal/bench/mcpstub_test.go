package bench

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestServeMCP(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"jira_search_issues","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"nope"}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(in), &out, len(catalog)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 replies (the notification gets none), got %d:\n%s", len(lines), out.String())
	}
	var replies [4]struct {
		ID     int `json:"id"`
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			Tools           []struct {
				Name string `json:"name"`
			} `json:"tools"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	for i, line := range lines {
		if err := json.Unmarshal([]byte(line), &replies[i]); err != nil {
			t.Fatal(err)
		}
	}
	if replies[0].Result.ProtocolVersion != "2025-03-26" {
		t.Errorf("initialize should echo the client's protocol version: %s", lines[0])
	}
	valid := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	seen := map[string]bool{}
	for _, tool := range replies[1].Result.Tools {
		if !valid.MatchString(tool.Name) || seen[tool.Name] {
			t.Errorf("bad or duplicate tool name %q", tool.Name)
		}
		seen[tool.Name] = true
	}
	if len(seen) != len(catalog) || len(catalog) < 60 {
		t.Errorf("listed %d tools, catalog has %d", len(seen), len(catalog))
	}
	if !replies[2].Result.IsError {
		t.Errorf("tools/call should be an error result: %s", lines[2])
	}
	if replies[3].Error == nil || replies[3].Error.Code != -32601 {
		t.Errorf("unknown method should be -32601: %s", lines[3])
	}
	if _, err := catalogTools(len(catalog) + 1); err == nil {
		t.Error("catalog beyond the list should fail")
	}
}

func TestAgentCommandCatalog(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		plain, err := agentCommand(agent, "127.0.0.1:1", "/w", "p", "", false, 0)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(plain.Args, " "), "bench-mcp") {
			t.Errorf("%s: catalog 0 must not add the stub: %q", agent, plain.Args)
		}
		with, err := agentCommand(agent, "127.0.0.1:1", "/w", "p", "", false, 40)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(with.Args, " ")
		want := map[string]string{"codex": `mcp_servers={bench={command=`, "claude": `"mcpServers":{"bench":`}[agent]
		if !strings.Contains(joined, want) || !strings.Contains(joined, `"bench-mcp","40"`) {
			t.Errorf("%s: missing stub server: %q", agent, with.Args)
		}
		if agent == "claude" && with.Args[len(with.Args)-1] != "mcp__bench" {
			t.Errorf("claude: mcp__bench should be allowed: %q", with.Args)
		}
	}
	if _, err := agentCommand("grok", "127.0.0.1:1", "/w", "p", "", false, 40); err == nil {
		t.Error("grok with --catalog should fail")
	}
}
