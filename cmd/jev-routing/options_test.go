package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/proxy"
)

func TestGatewayOptions(t *testing.T) {
	t.Setenv("JEV_ROUTING_MODE", "bad")
	if _, err := proxy.OptionsFromEnv(); err == nil {
		t.Fatal("invalid mode must fail")
	}
	t.Setenv("JEV_ROUTING_MODE", "filter")
	t.Setenv("JEV_COMPACTION", "on")
	t.Setenv("JEV_REASONING", "legacy")
	o, err := proxy.OptionsFromEnv()
	if err != nil || o.Mode != proxy.ModeFilter {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestGatewayRunStats(t *testing.T) {
	s, err := proxy.New("127.0.0.1:0", host.Grok, nil, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")
	t.Setenv("JEV_RUN_STATS", path)
	writeRunStats(s)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"requests", "charsBefore", "charsAfter"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("missing %s in %s", k, raw)
		}
	}
}

func TestAnalysisRunStats(t *testing.T) {
	TestGatewayRunStats(t)
}

func TestGatewayDashboard(t *testing.T) {
	s, err := proxy.New("127.0.0.1:0", host.Grok, nil, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	if s.Events() == nil {
		t.Fatal("events")
	}
}

func TestGatewayArgsModel(t *testing.T) {
	t.Setenv("JEV_ROUTING_MODE", "filter")
	t.Setenv("JEV_ARGS_MODEL", "x")
	t.Setenv("JEV_ARGS_TOOLS", "grep")
	if _, err := proxy.OptionsFromEnv(); err == nil {
		t.Fatal("args model requires forced")
	}
	t.Setenv("JEV_ROUTING_MODE", "forced")
	o, err := proxy.OptionsFromEnv()
	if err != nil || o.ArgsModel != "x" || !o.ArgsTools["grep"] {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestRunCombinesCodexConfigBeforeExec(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "argv")
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$TEST_CODEX_ARGV\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_CODEX_ARGV", argsPath)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("JEV_LISTEN", "127.0.0.1:0")
	t.Setenv("JEV_RUN_STATS", "")
	t.Setenv("JEV_ROUTING_MODE", "filter")
	if code := cmdRun([]string{"codex", "--", "exec", "--model", "gpt-5.6-terra", "-c", `model_reasoning_effort="low"`, "--", "prompt"}); code != 0 {
		t.Fatalf("run exit=%d", code)
	}
	raw, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	execIndex := slices.Index(args, "exec")
	providerIndex := slices.Index(args, `model_provider="jev"`)
	effortIndex := slices.Index(args, `model_reasoning_effort="low"`)
	if execIndex < 0 || providerIndex < 0 || effortIndex < 0 || providerIndex > execIndex || effortIndex > execIndex {
		t.Fatalf("configuration still spans subcommand scopes: %#v", args)
	}
}

func TestRunDashboardOpensAfterStartup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("JEV_LISTEN", "127.0.0.1:0")
	t.Setenv("JEV_RUN_STATS", "")

	var got string
	previous := openDashboard
	openDashboard = func(url string) error {
		got = url
		return nil
	}
	t.Cleanup(func() { openDashboard = previous })

	if code := cmdRun([]string{"--dashboard", "codex"}); code != 0 {
		t.Fatalf("run exit=%d", code)
	}
	if !strings.HasPrefix(got, "http://127.0.0.1:") || !strings.HasSuffix(got, "/dashboard") {
		t.Fatalf("dashboard URL = %q", got)
	}
}
