package plan

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestModelRouting(t *testing.T) {
	pairs, err := LoadPairs(filepath.Join("testdata", "model-profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	base := ModelRequest{Task: "review", Role: "reviewer", Host: host.Claude, LegacyModel: "test-fast", LegacyEffort: "low", Pairs: pairs}

	t.Run("both-fixed", func(t *testing.T) {
		req := base
		req.ModelMode, req.EffortMode = ModeFixed, ModeFixed
		got := RouteModel(req, func(ModelRequest, map[string]string) (string, float64, error) {
			t.Fatal("asked")
			return "", 0, nil
		})
		if got.Asked || got.Model != "test-fast" || got.Effort != "low" || got.ReasonCode != ReasonBothFixed {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("model-fixed-effort-auto", func(t *testing.T) {
		req := base
		req.ModelMode, req.EffortMode = ModeFixed, ModeAuto
		got := RouteModel(req, func(_ ModelRequest, criteria map[string]string) (string, float64, error) {
			if _, ok := criteria["pair:test-deep:high"]; ok {
				t.Fatal("other model leaked")
			}
			return "pair:test-fast:high", 0.9, nil
		})
		if got.Model != "test-fast" || got.Effort != "high" || !got.Asked {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("effort-fixed-model-auto", func(t *testing.T) {
		req := base
		req.ModelMode, req.EffortMode = ModeAuto, ModeFixed
		req.LegacyEffort = "high"
		got := RouteModel(req, func(_ ModelRequest, criteria map[string]string) (string, float64, error) {
			if _, ok := criteria["pair:test-fast:low"]; ok {
				t.Fatal("other effort leaked")
			}
			return "pair:test-deep:high", 0.9, nil
		})
		if got.Model != "test-deep" || got.Effort != "high" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("both-auto", func(t *testing.T) {
		req := base
		req.ModelMode, req.EffortMode = ModeAuto, ModeAuto
		got := RouteModel(req, func(ModelRequest, map[string]string) (string, float64, error) {
			return "pair:test-deep:high", 0.9, nil
		})
		if got.PairID != "pair:test-deep:high" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("unspecified-is-fixed", func(t *testing.T) {
		req := base
		got := RouteModel(req, func(ModelRequest, map[string]string) (string, float64, error) {
			t.Fatal("asked")
			return "", 0, nil
		})
		if got.ReasonCode != ReasonBothFixed || got.Asked {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("unknown-fixed-model-default", func(t *testing.T) {
		req := base
		req.ModelMode, req.EffortMode, req.LegacyModel = ModeFixed, ModeAuto, ""
		got := RouteModel(req, func(ModelRequest, map[string]string) (string, float64, error) {
			t.Fatal("asked")
			return "pair:test-fast:high", 0.9, nil
		})
		if got.ReasonCode != ReasonUnknownFixedDefault || got.Effort != "low" || got.Model != "" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("unknown-fixed-effort-default", func(t *testing.T) {
		req := base
		req.ModelMode, req.EffortMode, req.LegacyEffort = ModeAuto, ModeFixed, ""
		got := RouteModel(req, func(ModelRequest, map[string]string) (string, float64, error) {
			t.Fatal("asked")
			return "pair:test-deep:high", 0.9, nil
		})
		if got.ReasonCode != ReasonUnknownFixedDefault || got.Model != "test-fast" || got.Effort != "" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("other-host-excluded", func(t *testing.T) {
		req := base
		req.Host, req.ModelMode, req.EffortMode = host.Grok, ModeAuto, ModeAuto
		got := RouteModel(req, func(_ ModelRequest, criteria map[string]string) (string, float64, error) {
			if _, ok := criteria["pair:test-deep:high"]; ok {
				t.Fatal("codex-only pair leaked")
			}
			return "pair:test-fast:low", 0.9, nil
		})
		if got.PairID != "pair:test-fast:low" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("missing-pair-and-low-conf", func(t *testing.T) {
		req := base
		req.ModelMode, req.EffortMode = ModeAuto, ModeAuto
		got := RouteModel(req, func(ModelRequest, map[string]string) (string, float64, error) {
			return "pair:nope", 0.9, nil
		})
		if got.Model != "test-fast" || got.ReasonCode != ReasonInvalidID {
			t.Fatalf("%+v", got)
		}
		got = RouteModel(req, func(ModelRequest, map[string]string) (string, float64, error) {
			return "pair:test-deep:high", 0.4, nil
		})
		if got.Effort != "low" || got.ReasonCode != ReasonLowConfidence {
			t.Fatalf("%+v", got)
		}
	})
}

func TestPairLabelIncludesCostAndDifficulty(t *testing.T) {
	got := PairLabel(Pair{
		Model: "claude-sonnet-5", Effort: "medium",
		Difficulty: "easy", Cost: "low", Description: "echo and one-liner",
	})
	if !strings.Contains(got, "difficulty=") || !strings.Contains(got, "cost=") {
		t.Fatalf("label=%q", got)
	}
}

func TestRouteModelHelloWorldPrefersCheaperPair(t *testing.T) {
	req := ModelRequest{
		Task: "print hello world and exit", Role: "worker", Host: host.Claude,
		ModelMode: ModeAuto, EffortMode: ModeAuto,
		LegacyModel: "test-opus", LegacyEffort: "medium",
		Pairs: []Pair{
			{ID: "expensive", Model: "test-opus", Effort: "medium", Difficulty: "hard", Cost: "high", Hosts: []string{"claude"}},
			{ID: "cheap", Model: "test-sonnet", Effort: "medium", Difficulty: "easy", Cost: "low", Hosts: []string{"claude"}},
		},
	}
	got := RouteModel(req, func(_ ModelRequest, criteria map[string]string) (string, float64, error) {
		for id, label := range criteria {
			if id == NoMatchID {
				continue
			}
			if strings.Contains(label, "cost=low") {
				return id, 0.9, nil
			}
		}
		t.Fatal("missing cost=low in criteria")
		return "", 0, nil
	})
	if got.Model != "test-sonnet" || got.ReasonCode != "jev" || got.PairID != "cheap" {
		t.Fatalf("%+v", got)
	}
}

func TestRouteModelLowConfidenceRecordsRejectedPair(t *testing.T) {
	req := ModelRequest{
		Task: "print hello world and exit", Role: "worker", Host: host.Claude,
		ModelMode: ModeAuto, EffortMode: ModeAuto,
		LegacyModel: "test-opus", LegacyEffort: "medium",
		Pairs: []Pair{
			{ID: "expensive", Model: "test-opus", Effort: "medium", Hosts: []string{"claude"}},
			{ID: "cheap", Model: "test-sonnet", Effort: "medium", Hosts: []string{"claude"}},
		},
	}
	got := RouteModel(req, func(ModelRequest, map[string]string) (string, float64, error) {
		return "cheap", 0.4, nil
	})
	if got.Model != "test-opus" || got.ReasonCode != ReasonLowConfidence || got.RejectedID != "cheap" {
		t.Fatalf("%+v", got)
	}
}

func TestModelPairInstructions(t *testing.T) {
	for _, want := range []string{"cheapest", "hello world", "echo", "one-liner", "review", "multi-file", "ambiguous", "no_match"} {
		if !strings.Contains(ModelPairInstructions, want) {
			t.Fatalf("missing %q in %q", want, ModelPairInstructions)
		}
	}
}
