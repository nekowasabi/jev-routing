package bench

import "testing"

func TestClassifySessionsIncludesSameModelChildAndJev(t *testing.T) {
	run := RunRecord{Agent: "claude", SessionUsage: map[string]SessionUsage{
		"parent": {Requests: 2, Input: 2, Cached: 10, Output: 3},
		"child":  {Requests: 1, Input: 4, Cached: 20, CacheWrite: 5, Output: 6, JevInput: 7, JevOutput: 8},
	}}
	classifySessions(&run, "parent")
	if !run.ParentChildVerified || run.MainSessionKey != "parent" || run.ChildSessions != 1 || run.ChildTokens != 50 {
		t.Fatalf("attribution=%+v", run)
	}
	run.UnattributedRequests = 1
	run.ParentChildVerified = false
	run.ChildSessions = 0
	run.ChildTokens = 0
	classifySessions(&run, "parent")
	if run.ParentChildVerified || run.ChildSessions != 0 {
		t.Fatalf("unattributed request accepted: %+v", run)
	}
}
