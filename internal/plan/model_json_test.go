package plan

import (
	"encoding/json"
	"testing"
)

func TestModelResultJSONLowercaseKeys(t *testing.T) {
	raw, err := json.Marshal(ModelResult{
		Model: "m", Effort: "high", Source: "jev",
		ReasonCode: "jev", PairID: "p1", Asked: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"model", "effort", "reason_code", "asked"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing %s in %s", k, raw)
		}
	}
	for _, k := range []string{"Model", "ReasonCode"} {
		if _, ok := m[k]; ok {
			t.Fatalf("pascal key %s in %s", k, raw)
		}
	}
}
