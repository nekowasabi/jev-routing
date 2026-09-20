package host

import (
	"reflect"
	"strings"
	"testing"
)

func TestChildArgs(t *testing.T) {
	listen := "127.0.0.1:45678"
	want := []string{
		"--config", `model_provider="jev"`,
		"--config", `model_providers.jev.name="jev-routing"`,
		"--config", `model_providers.jev.base_url="http://127.0.0.1:45678/v1"`,
		"--config", `model_providers.jev.wire_api="responses"`,
		"--config", `model_providers.jev.requires_openai_auth=true`,
	}
	if got := ChildArgs(Codex, listen); !reflect.DeepEqual(got, want) {
		t.Fatalf("ChildArgs(Codex) = %#v, want %#v", got, want)
	}
	if got := ChildArgs(Cursor, listen); !reflect.DeepEqual(got, []string{"--endpoint", "http://localhost:45678"}) {
		t.Fatalf("ChildArgs(Cursor) = %#v", got)
	}
	for _, h := range []ID{Claude, Grok, Devin} {
		if got := ChildArgs(h, listen); got != nil {
			t.Fatalf("ChildArgs(%s) = %#v, want nil", h, got)
		}
	}
}

func TestAdvertise(t *testing.T) {
	if got := Advertise(Cursor, "127.0.0.1:8787"); got != "localhost:8787" {
		t.Fatalf("Advertise(Cursor, 127.0.0.1) = %q", got)
	}
	if got := Advertise(Cursor, "[::1]:8787"); got != "localhost:8787" {
		t.Fatalf("Advertise(Cursor, [::1]) = %q", got)
	}
	if got := Advertise(Cursor, "example.com:443"); got != "example.com:443" {
		t.Fatalf("Advertise other host = %q", got)
	}
	if got := Advertise(Claude, "127.0.0.1:8787"); got != "127.0.0.1:8787" {
		t.Fatalf("Advertise(Claude) = %q", got)
	}
}

func TestCommandArgsCollectsCodexConfigWithoutChangingPrompt(t *testing.T) {
	listen := "127.0.0.1:45678"
	args := []string{"--config", `model_provider="other"`, "exec", "resume", "--last",
		"-c", `model_reasoning_effort="low"`, "--config=features.foo=true", "-cmodel_verbosity=low",
		"-c=model_context_window=32000", "--", "-c", "prompt content", "--config=literal"}
	want := []string{"--config", `model_provider="other"`, "--config", `model_reasoning_effort="low"`,
		"--config", "features.foo=true", "--config", "model_verbosity=low", "--config", "model_context_window=32000"}
	want = append(want, ChildArgs(Codex, listen)...)
	want = append(want, "exec", "resume", "--last", "--", "-c", "prompt content", "--config=literal")
	if got := CommandArgs(Codex, listen, args); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	if got := CommandArgs(Claude, listen, args); !reflect.DeepEqual(got, args) {
		t.Fatal("non-Codex arguments changed")
	}
}

func TestParse(t *testing.T) {
	cases := map[string]ID{
		"claude": Claude, "anthropic": Claude,
		"codex": Codex, "openai": Codex,
		"grok": Grok, "xai": Grok,
		"cursor": Cursor, "cursor-agent": Cursor, "cursor-cli": Cursor,
		"devin": Devin, "cognition": Devin, "devin-cli": Devin,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Fatalf("Parse(%q) = %q, %v want %q", in, got, err, want)
		}
	}
	if _, err := Parse("unknown"); err == nil {
		t.Fatal("expected error")
	}
}

func TestNativeGrokCatalogNames(t *testing.T) {
	if Native(Grok, "Bash") != "run_terminal_cmd" {
		t.Fatalf("grok Bash: %s", Native(Grok, "Bash"))
	}
	if Native(Grok, "Agent") != "task" {
		t.Fatalf("grok Agent: %s", Native(Grok, "Agent"))
	}
	if Native(Grok, "Grep") != "grep_search" || Native(Grok, "Write") != "write" || Native(Grok, "ToolSearch") != "search_tool" {
		t.Fatalf("grok official catalog: Grep=%s Write=%s ToolSearch=%s", Native(Grok, "Grep"), Native(Grok, "Write"), Native(Grok, "ToolSearch"))
	}
}

func TestNativeCursorDevin(t *testing.T) {
	if Native(Cursor, "Bash") != "Shell" {
		t.Fatalf("cursor Bash: %s", Native(Cursor, "Bash"))
	}
	if Native(Cursor, "Agent") != "Task" {
		t.Fatalf("cursor Agent: %s", Native(Cursor, "Agent"))
	}
	if Native(Cursor, "Task") != "Task" {
		t.Fatalf("cursor Task: %s", Native(Cursor, "Task"))
	}
	if Native(Cursor, "Edit") != "Write" {
		t.Fatalf("cursor Edit: %s", Native(Cursor, "Edit"))
	}
	if Native(Cursor, "Read") != "Read" {
		t.Fatalf("cursor Read: %s", Native(Cursor, "Read"))
	}
	if Native(Cursor, "TodoWrite") != "updateTodos" {
		t.Fatalf("cursor TodoWrite: %s", Native(Cursor, "TodoWrite"))
	}
	if Native(Cursor, "AskUserQuestion") != "askQuestion" {
		t.Fatalf("cursor AskUserQuestion: %s", Native(Cursor, "AskUserQuestion"))
	}
	if Native(Cursor, "EnterPlanMode") != "createPlan" {
		t.Fatalf("cursor EnterPlanMode: %s", Native(Cursor, "EnterPlanMode"))
	}
	if Native(Devin, "Bash") != "exec" {
		t.Fatalf("devin Bash: %s", Native(Devin, "Bash"))
	}
	if Native(Devin, "Read") != "read" {
		t.Fatalf("devin Read: %s", Native(Devin, "Read"))
	}
	if Native(Devin, "Edit") != "edit" {
		t.Fatalf("devin Edit: %s", Native(Devin, "Edit"))
	}
	if Native(Devin, "Write") != "write" {
		t.Fatalf("devin Write: %s", Native(Devin, "Write"))
	}
	if Native(Devin, "Agent") != "run_subagent" {
		t.Fatalf("devin Agent: %s", Native(Devin, "Agent"))
	}
	if Native(Devin, "WebFetch") != "webfetch" {
		t.Fatalf("devin WebFetch: %s", Native(Devin, "WebFetch"))
	}
	if Native(Devin, "NotebookEdit") != "notebook_edit" || Native(Devin, "TaskOutput") != "read_subagent" || Native(Devin, "ReadMcpResourceTool") != "mcp_read_resource" {
		t.Fatalf("devin official catalog: NotebookEdit=%s TaskOutput=%s ReadMcpResourceTool=%s", Native(Devin, "NotebookEdit"), Native(Devin, "TaskOutput"), Native(Devin, "ReadMcpResourceTool"))
	}
	if Native(Devin, "TodoWrite") != "todo_write" {
		t.Fatalf("devin TodoWrite: %s", Native(Devin, "TodoWrite"))
	}
	if Native(Devin, "AskUserQuestion") != "ask_user_question" {
		t.Fatalf("devin AskUserQuestion: %s", Native(Devin, "AskUserQuestion"))
	}
	if Native(Devin, "EnterPlanMode") != "write_plan" {
		t.Fatalf("devin EnterPlanMode: %s", Native(Devin, "EnterPlanMode"))
	}
	if Native(Devin, "ExitPlanMode") != "exit_plan_mode" {
		t.Fatalf("devin ExitPlanMode: %s", Native(Devin, "ExitPlanMode"))
	}
	if Native(Devin, "Skill") != "Skill" {
		t.Fatalf("devin Skill must stay identity: %s", Native(Devin, "Skill"))
	}
	if Native(Devin, "github_get_pr") != "github_get_pr" {
		t.Fatal("MCP names must pass through")
	}
}

func TestBinaryLabel(t *testing.T) {
	if Cursor.Binary() != "cursor-agent" {
		t.Fatalf("binary %s", Cursor.Binary())
	}
	if Devin.Binary() != "devin" {
		t.Fatalf("binary %s", Devin.Binary())
	}
}

func TestChildEnvCursorDevin(t *testing.T) {
	listen := "127.0.0.1:8787"
	env := ChildEnv(Cursor, listen)
	if !containsKV(env, "CURSOR_API_ENDPOINT=http://localhost:8787") {
		t.Fatalf("cursor env missing endpoint: %v", env)
	}
	if !containsKV(env, "CURSOR_API_BASE_URL=http://localhost:8787") {
		t.Fatalf("cursor env missing base url: %v", env)
	}
	if containsKVPrefix(env, "OPENAI_BASE_URL=") {
		t.Fatalf("cursor must not set OPENAI_BASE_URL")
	}
	env = ChildEnv(Devin, listen)
	if !containsKV(env, "DEVIN_API_URL=http://127.0.0.1:8787") {
		t.Fatalf("devin env missing url: %v", env)
	}
	if !containsKV(env, "WINDSURF_API_SERVER_URL=http://127.0.0.1:8787") {
		t.Fatalf("devin env missing windsurf url: %v", env)
	}
}

func containsKV(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}

func containsKVPrefix(env []string, prefix string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}
