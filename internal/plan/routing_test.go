package plan

import (
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func testRouteCatalog(t *testing.T) Catalog {
	t.Helper()
	cat, err := BuildCatalog(Inventory{
		Skills: []SkillIn{{Name: "review", Provider: "test", Version: "1", BodyRef: "skill://review/SKILL.md", Available: true, Explicit: true, Description: "review skill"}},
		CLIs:   []CLIIn{{Name: "rg", Provider: "test", Version: "1", Command: "rg", Args: []string{"--json"}, Available: true, Description: "search files"}},
		Tools:  []Spec{{Name: "Grep", Desc: "search"}},
	}, host.Claude, &LaunchProbe{})
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func TestRouteExplicitAndValidation(t *testing.T) {
	cat := testRouteCatalog(t)
	skillID := CapabilityID(KindSkill, "test", "review", "1")
	got := Route(RouteRequest{RequestID: "r1", Text: "review the pr", Host: host.Claude, Catalog: cat, Explicit: []string{skillID}}, nil)
	if got.Outcome != RouteSelected || got.CapabilityID != skillID || got.ReasonCode != ReasonExplicit {
		t.Fatalf("%+v", got)
	}
	bad := Route(RouteRequest{Text: "x", Host: host.Claude, Catalog: cat, Explicit: []string{"mcp_tool:nope:x@1"}}, nil)
	if bad.ReasonCode != ReasonInvalidID || bad.Outcome != RouteNoDecision {
		t.Fatalf("invalid id %+v", bad)
	}
	ver := Route(RouteRequest{Text: "x", Host: host.Claude, Catalog: cat, ExpectedRevision: "other"}, nil)
	if ver.ReasonCode != ReasonVersionChanged {
		t.Fatalf("version %+v", ver)
	}
}

func TestRouteAskNoMatchAndMissing(t *testing.T) {
	cat := testRouteCatalog(t)
	nomatch := Route(RouteRequest{Text: "maybe something", Host: host.Claude, Catalog: cat, NewRequest: true}, func(string, map[string]string) (string, float64, error) {
		return NoMatchID, 0.9, nil
	})
	if nomatch.Outcome != RouteNoDecision || nomatch.ReasonCode != ReasonNoMatch {
		t.Fatalf("no_match %+v", nomatch)
	}
	missing := Route(RouteRequest{Text: "maybe something", Host: host.Claude, Catalog: cat, NewRequest: true}, func(string, map[string]string) (string, float64, error) {
		return "", 0, nil
	})
	if missing.ReasonCode != ReasonMissingAnswer {
		t.Fatalf("missing %+v", missing)
	}
	invalid := Route(RouteRequest{Text: "maybe something", Host: host.Claude, Catalog: cat, NewRequest: true}, func(string, map[string]string) (string, float64, error) {
		return "not-in-catalog", 0.9, nil
	})
	if invalid.ReasonCode != ReasonInvalidID {
		t.Fatalf("invalid %+v", invalid)
	}
}

func TestRouteReplaySameRequestID(t *testing.T) {
	cat := testRouteCatalog(t)
	a := Route(RouteRequest{RequestID: "same", Text: "hello", Host: host.Claude, Catalog: cat}, nil)
	b := Route(RouteRequest{RequestID: "same", Text: "hello", Host: host.Claude, Catalog: cat}, nil)
	if a.DecisionID != b.DecisionID {
		t.Fatalf("replay ids %s %s", a.DecisionID, b.DecisionID)
	}
	c := Route(RouteRequest{RequestID: "other", Text: "hello", Host: host.Claude, Catalog: cat, NewRequest: true}, nil)
	if c.DecisionID == a.DecisionID {
		t.Fatal("new same-text request reused decision")
	}
}
