package plan

import (
	"context"
	"testing"
	"time"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestRoutingBatchBudget(t *testing.T) {
	cat, err := LoadCatalog("testdata/capabilities.json")
	if err != nil {
		t.Fatal(err)
	}
	cli := CapabilityID(KindCLI, "test", "rg", "1")
	mcp := CapabilityID(KindMCP, "slack", "search", "1")
	items := []BatchItem{
		{ID: "q-cli", Request: RouteRequest{Text: "search files with rg", Host: host.Claude, Catalog: cat, NewRequest: true}},
		{ID: "q-mcp", Request: RouteRequest{Text: "search slack messages", Host: host.Claude, Catalog: cat, NewRequest: true}},
	}

	t.Run("answers-by-id", func(t *testing.T) {
		got := RouteBatch(context.Background(), items, func(_ context.Context, qs map[string]map[string]string) (map[string]string, map[string]float64, *int, *int, bool, error) {
			if _, ok := qs["q-cli"]; !ok || qs["q-mcp"] == nil {
				t.Fatal("missing question ids")
			}
			in, out := 11, 3
			return map[string]string{"q-mcp": mcp, "q-cli": cli}, map[string]float64{"q-mcp": 0.9, "q-cli": 0.9}, &in, &out, false, nil
		}, time.Time{})
		if len(got) != 2 || got[0].Route.CapabilityID != cli || got[1].Route.CapabilityID != mcp {
			t.Fatalf("%+v", got)
		}
		if got[0].InputTok == nil || *got[0].InputTok != 11 || got[1].InputTok == nil || *got[0].InputTok+0 != *got[1].InputTok {
			t.Fatalf("shared usage %+v", got)
		}
	})

	t.Run("partial-missing", func(t *testing.T) {
		got := RouteBatch(context.Background(), items, func(context.Context, map[string]map[string]string) (map[string]string, map[string]float64, *int, *int, bool, error) {
			return map[string]string{"q-cli": cli}, map[string]float64{"q-cli": 0.9}, nil, nil, false, nil
		}, time.Time{})
		if !got[1].Missing || got[1].Route.ReasonCode != ReasonMissingAnswer {
			t.Fatalf("%+v", got[1])
		}
		if got[1].InputTok != nil {
			t.Fatal("missing usage must stay missing")
		}
	})

	t.Run("deadline", func(t *testing.T) {
		got := RouteBatch(context.Background(), items, func(context.Context, map[string]map[string]string) (map[string]string, map[string]float64, *int, *int, bool, error) {
			t.Fatal("asked after deadline")
			return nil, nil, nil, nil, false, nil
		}, time.Now().Add(-time.Second))
		if !got[0].Fallback || got[0].Route.ReasonCode != ReasonAskTimeout {
			t.Fatalf("%+v", got[0])
		}
	})

	t.Run("cache-key-changes", func(t *testing.T) {
		other := cat
		other.Revision = "other"
		first := Route(RouteRequest{Text: "search files with rg", Host: host.Claude, Catalog: cat, NewRequest: true}, nil)
		second := Route(RouteRequest{Text: "search files with rg", Host: host.Claude, Catalog: other, NewRequest: true}, nil)
		if first.CatalogRevision == second.CatalogRevision {
			t.Fatal("capability version must change cache identity")
		}
	})
}
