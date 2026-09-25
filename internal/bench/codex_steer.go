package bench

import "encoding/json"

// codexSteerCounts tallies a --codex-steer run's mode breakdown from the
// proxy's raw dashboard-events snapshot (gateway.snapshot): how many
// requests were forced, how many got tool_choice:"none", and a
// reason->count breakdown of the rest (passthrough). Only meaningful for a
// run made with JEV_CODEX_STEER set -- the caller (run.go) gates this on
// --codex-steer, since outside that context an "forced" apply can also come
// from the ordinary local/hybrid selection engine and would not mean the
// same thing.
func codexSteerCounts(snapshot []byte) (forced, none int, passthrough map[string]int) {
	passthrough = map[string]int{}
	if len(snapshot) == 0 {
		return 0, 0, passthrough
	}
	var body struct {
		Events []struct {
			Reason string `json:"reason"`
			Apply  string `json:"apply"`
		} `json:"events"`
	}
	if json.Unmarshal(snapshot, &body) != nil {
		return 0, 0, passthrough
	}
	for _, e := range body.Events {
		if e.Reason == "not_llm_path" {
			continue
		}
		switch e.Apply {
		case "forced":
			forced++
		case "codex_none":
			none++
		default:
			if e.Reason != "" {
				passthrough[e.Reason]++
			}
		}
	}
	return forced, none, passthrough
}
