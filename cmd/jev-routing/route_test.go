package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
	"github.com/nekowasabi/jev-routing/internal/jev"
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

func TestRouteCommandModelJSON(t *testing.T) {
	t.Setenv("JEV_MODEL_LOG", filepath.Join(t.TempDir(), "model-routes.jsonl"))
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("JEV_API_KEY", "")
	in := routeInput{
		Request: "review", Host: "claude", Role: "reviewer",
		ModelMode: "auto", EffortMode: "auto",
		LegacyModel: "claude-opus-5", LegacyEffort: "medium",
		Pairs: []plan.Pair{
			{ID: "a", Model: "claude-opus-5", Effort: "medium"},
			{ID: "b", Model: "grok-4.6", Effort: "medium"},
		},
	}
	raw, _ := json.Marshal(in)
	var out bytes.Buffer
	if code := cmdRoute([]string{"--json"}, bytes.NewReader(raw), &out); code != 0 {
		t.Fatalf("exit %d out=%s", code, out.String())
	}
	var wrap map[string]any
	if err := json.Unmarshal(out.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	model, ok := wrap["model"].(map[string]any)
	if !ok {
		t.Fatalf("model %T %s", wrap["model"], out.Bytes())
	}
	if _, ok := model["model"]; !ok {
		t.Fatalf("missing model: %s", out.Bytes())
	}
	if _, ok := model["reason_code"]; !ok {
		t.Fatalf("missing reason_code: %s", out.Bytes())
	}
	if _, ok := model["Model"]; ok {
		t.Fatalf("pascal Model: %s", out.Bytes())
	}
	if _, ok := model["ReasonCode"]; ok {
		t.Fatalf("pascal ReasonCode: %s", out.Bytes())
	}
	if model["reason_code"] != plan.ReasonNoMatch {
		t.Fatalf("reason_code=%v", model["reason_code"])
	}
	if model["model"] != "claude-opus-5" {
		t.Fatalf("model=%v", model["model"])
	}
	got := plan.LoadModelDecisions()
	if len(got) != 1 || got[0].AppliedModel != "claude-opus-5" {
		t.Fatalf("%+v", got)
	}
}

func TestChoiceAskerNilWhenNotLive(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("JEV_API_KEY", "")
	if ask := choiceAsker(jev.FromEnv(), "model_pair", "x"); ask != nil {
		t.Fatal("expected nil asker when not live")
	}
}

func TestChoiceQuestionsUsesRequestedID(t *testing.T) {
	qs := choiceQuestions("model_pair", plan.ModelPairInstructions, map[string]string{"pair:a": "a"})
	q, ok := qs["model_pair"]
	if !ok {
		t.Fatalf("missing model_pair: %v", qs)
	}
	if q.Instructions != plan.ModelPairInstructions {
		t.Fatalf("instructions=%q", q.Instructions)
	}
	if _, ok := qs["capability"]; ok {
		t.Fatalf("unexpected capability: %v", qs)
	}
	capQS := choiceQuestions("capability", "pick one capability id", map[string]string{"cli:x": "x"})
	if _, ok := capQS["capability"]; !ok {
		t.Fatalf("missing capability: %v", capQS)
	}
	if _, ok := capQS["model_pair"]; ok {
		t.Fatalf("unexpected model_pair: %v", capQS)
	}
}

func TestModelAskerUsesModelPairAndStructuredState(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("JEV_API_KEY", "")
	if ask := modelAsker(jev.FromEnv()); ask != nil {
		t.Fatal("expected nil model asker when not live")
	}
	if ask := choiceAsker(jev.FromEnv(), "capability", "pick one capability id"); ask != nil {
		t.Fatal("expected nil capability asker when not live")
	}
	st := modelAskState(plan.ModelRequest{Task: "print hello world and exit", Role: "worker", Host: host.Claude})
	if st["request"] != "print hello world and exit" || st["role"] != "worker" || st["host"] != string(host.Claude) {
		t.Fatalf("%v", st)
	}
}
