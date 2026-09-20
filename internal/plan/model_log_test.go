package plan

import (
	"path/filepath"
	"testing"
)

func TestRecordAndLoadModelDecisions(t *testing.T) {
	t.Setenv("JEV_MODEL_LOG", filepath.Join(t.TempDir(), "model-routes.jsonl"))
	RecordModelDecision(ModelDecision{Host: "claude", AppliedModel: "gpt-x", ReasonCode: "jev"})
	got := LoadModelDecisions()
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].AppliedModel != "gpt-x" || got[0].ReasonCode != "jev" {
		t.Fatalf("%+v", got[0])
	}
}
