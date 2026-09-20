package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestCapabilityCatalog(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file Catalog
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, item := range file.Items {
		kinds[item.Kind] = true
		if !item.Target.bound(item.Kind) {
			t.Fatalf("fixture %s missing target", item.ID)
		}
	}
	for _, k := range []string{KindModel, KindSubagent, KindSkill, KindMCP, KindCLI, KindPlugin} {
		if !kinds[k] {
			t.Fatalf("missing kind %s", k)
		}
	}
	round, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	var back Catalog
	if err := json.Unmarshal(round, &back); err != nil || len(back.Items) != len(file.Items) {
		t.Fatalf("roundtrip %+v err=%v", back, err)
	}

	probe := &LaunchProbe{}
	inv := Inventory{
		Tools: []Spec{{Name: "Grep", Desc: "search"}, {Name: "mcp__slack__search", Desc: "slack"}},
		Skills: []SkillIn{
			{Name: "review", Provider: "test", Version: "1", BodyRef: "skill://review/SKILL.md", Available: true, Explicit: true},
			{Name: "missing", Provider: "test", Version: "1", BodyRef: "skill://missing/SKILL.md", Available: false},
		},
		CLIs:      []CLIIn{{Name: "rg", Provider: "test", Version: "1", Command: "rg", Args: []string{"--json"}, Available: true, Permission: ""}},
		Models:    []ModelIn{{Name: "fast", Provider: "test", Version: "1", Available: true, Pairs: []ModelEffort{{Model: "test-fast", Effort: "low"}}}},
		Subagents: []SubagentIn{{Name: "reviewer", Provider: "test", Version: "1", Launcher: "Agent", Available: true}},
		Plugins: []PluginIn{{
			Name: "devtools", Provider: "test", Version: "1", Available: true,
			Children: []Capability{{
				Kind: KindMCP, Name: "lookup", Version: "1",
				Target: Target{MCPConn: "snap:devtools", MCPTool: "lookup"},
			}},
		}},
	}
	cat, err := BuildCatalog(inv, host.Claude, probe)
	if err != nil {
		t.Fatal(err)
	}
	if probe.MCPStarts != 0 || probe.CLIRuns != 0 {
		t.Fatalf("launched processes %+v", probe)
	}

	t.Run("same-name-different-provider", func(t *testing.T) {
		var ids []string
		for _, item := range cat.Items {
			if item.Name == "search" || item.Name == "mcp__slack__search" {
				ids = append(ids, item.ID)
			}
		}
		slack := Capability{Kind: KindMCP, Name: "search", Provider: "slack", Version: "1", Availability: AvailInstalled, Target: Target{MCPConn: "snap:slack", MCPTool: "search"}}
		hostTool := capabilityFromSpec(Spec{Name: "search"}, host.Claude)
		if slack.ID == "" {
			slack.ID = CapabilityID(slack.Kind, slack.Provider, slack.Name, slack.Version)
		}
		if slack.ID == hostTool.ID {
			t.Fatalf("providers collapsed %s", slack.ID)
		}
		_ = ids
	})

	t.Run("id-collision", func(t *testing.T) {
		dup := Catalog{}
		item := Capability{Kind: KindCLI, Name: "rg", Provider: "test", Version: "1", Target: Target{CLICommand: "rg"}}
		if err := dup.Add(item); err != nil {
			t.Fatal(err)
		}
		if err := dup.Add(item); err == nil {
			t.Fatal("collision accepted")
		}
	})

	t.Run("not-installed", func(t *testing.T) {
		for _, item := range cat.Eligible() {
			if item.Name == "missing" {
				t.Fatal("missing skill was eligible")
			}
		}
	})

	t.Run("unknown-permission", func(t *testing.T) {
		found := false
		for _, item := range cat.Items {
			if item.Kind == KindCLI && item.Name == "rg" {
				found = true
				if item.Permission != PermUnknown {
					t.Fatalf("permission coerced %q", item.Permission)
				}
			}
		}
		if !found {
			t.Fatal("rg missing")
		}
	})

	t.Run("plugin-child-not-duplicated", func(t *testing.T) {
		again := inv
		again.Plugins = append(again.Plugins, PluginIn{
			Name: "devtools", Provider: "test", Version: "1", Available: true,
			Children: []Capability{{
				Kind: KindMCP, Name: "lookup", Version: "1",
				Target: Target{MCPConn: "snap:devtools", MCPTool: "lookup"},
			}},
		})
		// second plugin add will collide on plugin id; children must still not double
		cat2, _ := BuildCatalog(again, host.Claude, probe)
		n := 0
		for _, item := range cat2.Items {
			if item.Kind == KindMCP && item.Name == "lookup" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("lookup count %d", n)
		}
	})

	t.Run("explicit-skill", func(t *testing.T) {
		ok := false
		for _, item := range cat.Eligible() {
			if item.Kind == KindSkill && item.Name == "review" && item.Explicit {
				ok = true
			}
		}
		if !ok {
			t.Fatal("explicit skill dropped")
		}
	})

	t.Run("version-changes-revision", func(t *testing.T) {
		inv2 := inv
		inv2.CLIs[0].Version = "2"
		cat2, err := BuildCatalog(inv2, host.Claude, probe)
		if err != nil {
			t.Fatal(err)
		}
		if cat2.Revision == cat.Revision {
			t.Fatal("revision unchanged after version bump")
		}
	})

	t.Run("spec-roundtrip", func(t *testing.T) {
		specs := []Spec{{Name: "Read", Desc: "read a file"}, {Name: "Agent", Desc: "delegate"}}
		caps := CapabilitiesFromSpecs(specs, host.Grok)
		back := Catalog{Items: caps}.Specs()
		if len(back) != 2 || back[0].Name != "Read" || back[1].Name != "Agent" {
			t.Fatalf("%+v", back)
		}
	})

	t.Run("name-only-rejected", func(t *testing.T) {
		var empty Catalog
		if err := empty.Add(Capability{Kind: KindMCP, Name: "x", Provider: "p", Version: "1"}); err == nil {
			t.Fatal("name-only accepted")
		}
	})

	if probe.MCPStarts != 0 || probe.CLIRuns != 0 {
		t.Fatalf("probe used after tests %+v", probe)
	}
}

func TestInventoryFromSkillDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# review\ncheck the diff"
	if err := os.WriteFile(filepath.Join(dir, "review", "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	inv, bodies, err := InventoryFromSkillDir(dir)
	if err != nil || len(inv.Skills) != 1 || inv.Skills[0].Name != "review" {
		t.Fatalf("%+v %v", inv, err)
	}
	cat, err := BuildCatalog(inv, host.Claude, &LaunchProbe{})
	if err != nil {
		t.Fatal(err)
	}
	item, ok := Lookup(cat, CapabilityID(KindSkill, "local", "review", "1"))
	if !ok || bodies[item.Target.SkillBodyRef] != body {
		t.Fatalf("skill body not bound %+v bodies=%v", item, bodies)
	}
}
