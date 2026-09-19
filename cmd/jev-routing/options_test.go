package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
