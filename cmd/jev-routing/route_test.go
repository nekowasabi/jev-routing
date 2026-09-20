package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/plan"
)

func TestRouteCommand(t *testing.T) {
	cat, err := plan.BuildCatalog(plan.Inventory{
		Skills: []plan.SkillIn{{Name: "review", Provider: "test", Version: "1", BodyRef: "skill://review/SKILL.md", Available: true, Explicit: true, Description: "review"}},
	}, "claude", &plan.LaunchProbe{})
	if err != nil {
		t.Fatal(err)
	}
	skillID := plan.CapabilityID(plan.KindSkill, "test", "review", "1")
	in := routeInput{
		Request: "review this", Host: "claude", RequestID: "cli-1",
		Catalog: cat, Explicit: []string{skillID},
	}
	raw, _ := json.Marshal(in)
	var out bytes.Buffer
	if code := cmdRoute([]string{"--json"}, bytes.NewReader(raw), &out); code != 0 {
		t.Fatalf("exit %d out=%s", code, out.String())
	}
	var got plan.RouteResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Outcome != plan.RouteSelected || got.CapabilityID != skillID || got.ReasonCode != plan.ReasonExplicit {
		t.Fatalf("%+v", got)
	}

	in.Explicit = []string{"mcp_tool:missing:x@1"}
	raw, _ = json.Marshal(in)
	out.Reset()
	if code := cmdRoute([]string{"--json"}, bytes.NewReader(raw), &out); code != 0 {
		t.Fatalf("invalid id exit %d", code)
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.ReasonCode != plan.ReasonInvalidID {
		t.Fatalf("invalid %+v", got)
	}

	if code := cmdRoute(nil, bytes.NewReader(raw), &out); code != 2 {
		t.Fatalf("missing --json exit %d", code)
	}
}
