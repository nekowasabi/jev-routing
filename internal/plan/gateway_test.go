package plan

import (
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestGatewayGrokNames(t *testing.T) {
	if Native(host.Grok, "Bash") != "run_terminal_cmd" {
		t.Fatalf("canonical bash=%s", Native(host.Grok, "Bash"))
	}
	if Native(host.Grok, "Agent") != "task" {
		t.Fatalf("canonical agent=%s", Native(host.Grok, "Agent"))
	}
	legacy := map[string]bool{"run_terminal_command": true, "spawn_subagent": true, "read_file": true, "grep": true}
	if AliasIn(legacy, "run_terminal_cmd") != "run_terminal_command" {
		t.Fatalf("legacy bash alias=%q", AliasIn(legacy, "run_terminal_cmd"))
	}
	if AliasIn(legacy, "Agent") != "spawn_subagent" {
		t.Fatalf("legacy agent alias=%q", AliasIn(legacy, "Agent"))
	}
	p := "Please run the tests"
	d := DecideSpecs(p, nil, []Spec{
		{Name: "read_file"}, {Name: "grep_search"}, {Name: "run_terminal_cmd"}, {Name: "task"},
	}, host.Grok)
	if d.Tool != "run_terminal_cmd" || d.Passthrough {
		t.Fatalf("run the tests on new catalog: %+v", d)
	}
	d2 := DecideSpecs(p, nil, []Spec{
		{Name: "read_file"}, {Name: "grep"}, {Name: "run_terminal_command"}, {Name: "spawn_subagent"},
	}, host.Grok)
	if d2.Tool != "run_terminal_command" || d2.Passthrough {
		t.Fatalf("run the tests on legacy catalog: %+v", d2)
	}
	d3 := DecideSpecs("Explore the auth package thoroughly", nil, []Spec{
		{Name: "read_file"}, {Name: "task"},
	}, host.Grok)
	if d3.Tool != "task" {
		t.Fatalf("explore new catalog: %+v", d3)
	}
}

func TestDecideSpecsKeepsShellOnEchoCommand(t *testing.T) {
	p := "Run this exact shell command now: echo jev-live-cli-ok. Use a shell or exec tool."
	polluted := "検索してから読んでください。\n" + p
	specs := []Spec{{Name: "read_file"}, {Name: "grep"}, {Name: "run_terminal_command"}, {Name: "spawn_subagent"}}
	d := DecideSpecs(polluted, nil, specs, host.Grok)
	if d.Tool != "run_terminal_command" || d.Passthrough {
		t.Fatalf("echo command must keep the shell tool: %+v", d)
	}
	if SequentialLocate(polluted) {
		t.Fatal("explicit shell ask must not count as sequential locate")
	}
}
