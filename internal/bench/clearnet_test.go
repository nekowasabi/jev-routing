package bench

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// writeClearNetRun lays out dir/task.on.1/{proxy-events.json,agent.log} for
// applyClearNet to read, mirroring what run.go writes for a real run.
func writeClearNetRun(t *testing.T, eventsJSON, agentLog string) (dir string, run RunRecord) {
	t.Helper()
	dir = t.TempDir()
	runDir := filepath.Join(dir, "task.on.1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "proxy-events.json"), []byte(eventsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if agentLog != "" {
		if err := os.WriteFile(filepath.Join(runDir, "agent.log"), []byte(agentLog), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run = RunRecord{Task: "task", Agent: "claude", Mode: "on", Rep: 1, ClaudeClear: true, ClaudeClearKeep: 1}
	return dir, run
}

func clearEvent(seq, input, output, cacheWrite, cleared int) string {
	return fmt.Sprintf(`{"seq":%d,"usage":{"inputTokens":%d,"outputTokens":%d,"cachedTokens":0,"cacheWriteTokens":%d},"clearedInputTokens":%d}`,
		seq, input, output, cacheWrite, cleared)
}

func assistantTurn(fileArg string) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"%s"}}]}}`, fileArg)
}

// TestApplyClearNetFixedEvents hand-computes saved/extra/rework/net for a
// fixed 4-request run and checks applyClearNet reproduces them exactly.
//
// req1: ctx=150 (in100+cw50), out20, cleared0
// req2: ctx=150 (in100+cw50), out20, cleared0
// req3: ctx=180 (in100+cw80), out20, cleared40  <- clear fires here
// req4: ctx=160 (in100+cw60), out25, cleared0, and its tool call repeats
//
//	req1's (now older than keep=1) call -> rework
//
// saved  = 0+0+40+0 = 40
// extra  = req3 only: expected=cf3-cf2=(180+40)-150=70; cw3=80 -> extra=10
// rework = req4's ctx+out = 160+25 = 185 (req1's Read("fileA") repeats)
// net    = 40 - 10 - 185 = -155
// actual = (150+20)+(150+20)+(180+20)+(160+25) = 725
// cfTotal = 725 + (-155) = 570; netPct = -155/570
func TestApplyClearNetFixedEvents(t *testing.T) {
	events := `{"events":[` +
		clearEvent(1, 100, 20, 50, 0) + "," +
		clearEvent(2, 100, 20, 50, 0) + "," +
		clearEvent(3, 100, 20, 80, 40) + "," +
		clearEvent(4, 100, 25, 60, 0) +
		`]}`
	log := assistantTurn("fileA") + "\n" +
		assistantTurn("fileB") + "\n" +
		assistantTurn("fileC") + "\n" +
		assistantTurn("fileA") + "\n"
	dir, run := writeClearNetRun(t, events, log)
	applyClearNet(dir, &run)

	if run.ClearSavedTokens == nil || *run.ClearSavedTokens != 40 {
		t.Fatalf("saved = %v, want 40", derefTestPtr(run.ClearSavedTokens))
	}
	if run.ClearExtraCacheWrite == nil || *run.ClearExtraCacheWrite != 10 {
		t.Fatalf("extra = %v, want 10", derefTestPtr(run.ClearExtraCacheWrite))
	}
	if run.ClearReworkTokens == nil || *run.ClearReworkTokens != 185 {
		t.Fatalf("rework = %v, want 185", derefTestPtr(run.ClearReworkTokens))
	}
	if run.ClearNetTokens == nil || *run.ClearNetTokens != -155 {
		t.Fatalf("net = %v, want -155", derefTestPtr(run.ClearNetTokens))
	}
	wantPct := -155.0 / 570.0
	if run.ClearNetPct == nil || !near(*run.ClearNetPct, wantPct) {
		t.Fatalf("netPct = %v, want %v", derefTestPtr(run.ClearNetPct), wantPct)
	}
}

// TestApplyClearNetNoClearIsZeroNotMissing checks that an "on" run where
// --claude-clear never actually fired reports net=0 (a valid result), not a
// missing measurement.
func TestApplyClearNetNoClearIsZeroNotMissing(t *testing.T) {
	events := `{"events":[` +
		clearEvent(1, 100, 20, 50, 0) + "," +
		clearEvent(2, 100, 20, 50, 0) +
		`]}`
	dir, run := writeClearNetRun(t, events, "")
	applyClearNet(dir, &run)

	if run.ClearSavedTokens == nil || *run.ClearSavedTokens != 0 {
		t.Fatalf("saved = %v, want 0", derefTestPtr(run.ClearSavedTokens))
	}
	if run.ClearReworkTokens == nil || *run.ClearReworkTokens != 0 {
		t.Fatalf("rework = %v, want 0 (not missing)", derefTestPtr(run.ClearReworkTokens))
	}
	if run.ClearNetTokens == nil || *run.ClearNetTokens != 0 {
		t.Fatalf("net = %v, want 0", derefTestPtr(run.ClearNetTokens))
	}
	if run.ClearNetPct == nil || *run.ClearNetPct != 0 {
		t.Fatalf("netPct = %v, want 0", derefTestPtr(run.ClearNetPct))
	}
}

// TestApplyClearNetReworkUnavailableIsNull checks that when the clear did
// fire but the agent.log/proxy-events.json turn counts cannot be correlated
// 1:1, rework and net stay nil (not zero-filled), while saved/extra -- which
// do not depend on agent.log -- are still populated.
func TestApplyClearNetReworkUnavailableIsNull(t *testing.T) {
	events := `{"events":[` +
		clearEvent(1, 100, 20, 50, 0) + "," +
		clearEvent(2, 100, 20, 50, 0) + "," +
		clearEvent(3, 100, 20, 80, 40) +
		`]}`
	// Only two assistant turns for three requests: the positional
	// correlation is broken, so rework cannot be trusted.
	log := assistantTurn("fileA") + "\n" + assistantTurn("fileB") + "\n"
	dir, run := writeClearNetRun(t, events, log)
	applyClearNet(dir, &run)

	if run.ClearSavedTokens == nil || *run.ClearSavedTokens != 40 {
		t.Fatalf("saved = %v, want 40", derefTestPtr(run.ClearSavedTokens))
	}
	if run.ClearExtraCacheWrite == nil || *run.ClearExtraCacheWrite != 10 {
		t.Fatalf("extra = %v, want 10", derefTestPtr(run.ClearExtraCacheWrite))
	}
	if run.ClearReworkTokens != nil {
		t.Fatalf("rework = %v, want nil (unavailable)", *run.ClearReworkTokens)
	}
	if run.ClearNetTokens != nil {
		t.Fatalf("net = %v, want nil (unavailable)", *run.ClearNetTokens)
	}
	if run.ClearNetPct != nil {
		t.Fatalf("netPct = %v, want nil (unavailable)", *run.ClearNetPct)
	}
}

// TestApplyClearNetSkipsWrongCondition confirms non-Claude-clear runs are
// left entirely unpopulated.
func TestApplyClearNetSkipsWrongCondition(t *testing.T) {
	dir, run := writeClearNetRun(t, `{"events":[]}`, "")
	run.ClaudeClear = false
	applyClearNet(dir, &run)
	if run.ClearSavedTokens != nil || run.ClearNetTokens != nil {
		t.Fatalf("expected untouched run, got %+v", run)
	}
}

func derefTestPtr[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
