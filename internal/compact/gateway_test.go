package compact

import "testing"

func TestUncertainScoresKeepHistory(t *testing.T) {
	c := Candidate{Items: pair("1", "Read", "file body", 400)}
	d := DecideCall(c, map[string]float64{}, 0.5)
	if d[0].Action != ActionKeep || d[1].Action != ActionKeep {
		t.Fatalf("missing scores must keep, got %+v", d)
	}
	d = DecideCall(c, map[string]float64{"result_1_r": 0, "call_1": 0}, 0.5)
	if d[0].Action != ActionDrop || d[1].Action != ActionDrop {
		t.Fatalf("valid zero is a low score, got %+v", d)
	}
}
