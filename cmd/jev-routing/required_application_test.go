package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/plan"
	"github.com/nekowasabi/jev-routing/internal/proxy"
)

func TestRequiredApplicationHosts(t *testing.T) {
	hosts := []host.ID{host.Claude, host.Codex, host.Grok, host.Cursor, host.Devin}
	cat, err := plan.BuildCatalog(plan.Inventory{
		Skills: []plan.SkillIn{{Name: "review", Provider: "test", Version: "1", BodyRef: "skill://review/SKILL.md", Available: true, Explicit: true, Description: "review"}},
	}, host.Claude, &plan.LaunchProbe{})
	if err != nil {
		t.Fatal(err)
	}
	skillID := plan.CapabilityID(plan.KindSkill, "test", "review", "1")
	bodies := map[string]string{"skill://review/SKILL.md": "# review\ncheck the diff"}
	root := filepath.Join("..", "..", "internal", "proxy", "testdata", "application")

	for _, h := range hosts {
		t.Run(string(h)+"/required-undelivered", func(t *testing.T) {
			store := proxy.NewAppStore()
			route := plan.Route(plan.RouteRequest{RequestID: string(h) + "-miss", Text: "use review skill", Host: h, Catalog: cat, Explicit: []string{skillID}}, nil)
			app, err := proxy.Apply(store, route, cat, map[string]string{}, nil, nil)
			if proxy.RequireApplied(proxy.PolicyRequired, app, err) == nil {
				t.Fatal("required must fail when the skill body is missing")
			}
		})
		t.Run(string(h)+"/required-delivered", func(t *testing.T) {
			store := proxy.NewAppStore()
			exec := &requiredExec{}
			route := plan.Route(plan.RouteRequest{RequestID: string(h) + "-ok", Text: "use review skill", Host: h, Catalog: cat, Explicit: []string{skillID}}, nil)
			app, err := proxy.Apply(store, route, cat, bodies, nil, exec)
			if err := proxy.RequireApplied(proxy.PolicyRequired, app, err); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(root, string(h)+".json"))
			if err != nil {
				t.Fatal(err)
			}
			out, err := proxy.ApplyHostContext(h, raw, "source: skill://review/SKILL.md")
			if err != nil {
				t.Fatal(err)
			}
			if !containsDelivered(out) {
				t.Fatalf("%s context not written back: %s", h, out)
			}
		})
		t.Run(string(h)+"/fallback", func(t *testing.T) {
			store := proxy.NewAppStore()
			route := plan.Route(plan.RouteRequest{RequestID: string(h) + "-fb", Text: "use review skill", Host: h, Catalog: cat, Explicit: []string{skillID}}, nil)
			app, err := proxy.Apply(store, route, cat, map[string]string{}, nil, nil)
			if proxy.RequireApplied(proxy.PolicyFallback, app, err) != nil {
				t.Fatal("fallback must not fail the run")
			}
		})
		t.Run(string(h)+"/thinking-cache-still-delivers-skill", func(t *testing.T) {
			if h != host.Claude {
				t.Skip("thinking+cache is a Claude contract")
			}
			store := proxy.NewAppStore()
			exec := &requiredExec{}
			route := plan.Route(plan.RouteRequest{RequestID: "think-ok", Text: "use review skill", Host: h, Catalog: cat, Explicit: []string{skillID}}, nil)
			app, err := proxy.Apply(store, route, cat, bodies, nil, exec)
			if err := proxy.RequireApplied(proxy.PolicyRequired, app, err); err != nil {
				t.Fatal(err)
			}
			raw := []byte(`{"model":"claude","thinking":{"type":"enabled","budget_tokens":8000},"messages":[{"role":"user","content":"review this diff","cache_control":{"type":"ephemeral"}}]}`)
			out, err := proxy.ApplyHostContext(h, raw, "source: skill://review/SKILL.md")
			if err != nil || !containsDelivered(out) {
				t.Fatalf("thinking+cache must still accept skill writeback: %s %v", out, err)
			}
		})
		t.Run(string(h)+"/unsupported-required", func(t *testing.T) {
			empty := plan.Catalog{Revision: "empty"}
			route := plan.Route(plan.RouteRequest{RequestID: string(h) + "-none", Text: "do work", Host: h, Catalog: empty}, nil)
			if route.Outcome == plan.RouteSelected {
				t.Fatal("empty catalog selected")
			}
			if err := proxy.RequireApplied(proxy.PolicyRequired, nil, nil); err == nil {
				t.Fatal("required must error when selection is not consumed")
			}
		})
	}
}

type requiredExec struct{ skills int }

func (r *requiredExec) DeliverSkill(string, string, string) error { r.skills++; return nil }
func (r *requiredExec) StartCall(proxy.HostCall) (string, error)  { return "c1", nil }

func containsDelivered(raw []byte) bool {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return false
	}
	enc, _ := json.Marshal(root)
	return string(enc) != "" && (containsText(root, "skill://review/SKILL.md"))
}

func containsText(v any, want string) bool {
	switch x := v.(type) {
	case string:
		return x != "" && (x == want || len(x) >= len(want) && (stringContains(x, want)))
	case map[string]any:
		for _, child := range x {
			if containsText(child, want) {
				return true
			}
		}
	case []any:
		for _, child := range x {
			if containsText(child, want) {
				return true
			}
		}
	}
	return false
}

func stringContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}
