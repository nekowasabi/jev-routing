package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestChildSurveyVerifier(t *testing.T) {
	tasks, err := Tasks()
	if err != nil {
		t.Fatal(err)
	}
	var task *Task
	for i := range tasks {
		if tasks[i].ID == "child-survey" {
			task = &tasks[i]
		}
	}
	if task == nil || !task.RequiresSubagent {
		t.Fatal("child-survey missing or not a subagent task")
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := task.Setup(workspace); err != nil {
		t.Fatal(err)
	}
	if start, err := task.Verify(workspace); err != nil || start.Solved {
		t.Fatalf("starting state passed: %+v, %v", start, err)
	}
	if err := task.Reference(workspace); err != nil {
		t.Fatal(err)
	}
	done, err := task.Verify(workspace)
	if err != nil || !done.Solved || done.Total != 16 {
		t.Fatalf("reference = %+v, %v; want 16/16", done, err)
	}

	want, err := xcellExpected("child-survey", workspace)
	if err != nil {
		t.Fatal(err)
	}
	entry := want["internal/jev/jev.go"].(map[string]any)
	entry["last_line"] = entry["last_line"].(float64) + 1
	raw := "{"
	for i, name := range childSurveyFiles {
		e := want[name].(map[string]any)
		if i > 0 {
			raw += ","
		}
		raw += fmt.Sprintf(`%q:{"last_line":%v,"last":%q}`, name, e["last_line"], e["last"])
	}
	if err := os.WriteFile(filepath.Join(workspace, "answer.json"), []byte(raw+"}"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrong, err := task.Verify(workspace)
	if err != nil || wrong.Solved || wrong.Passed != 15 || !reflect.DeepEqual(wrong.Failed, []string{"internal/jev/jev.go.last_line"}) {
		t.Fatalf("one miscount = %+v, %v; want 15/16 failing jev.go.last_line", wrong, err)
	}
	// last_line is the line whose text starts the last func declaration.
	src, err := os.ReadFile(filepath.Join(workspace, "internal/jev/jev.go"))
	if err != nil {
		t.Fatal(err)
	}
	e := want["internal/jev/jev.go"].(map[string]any)
	line := strings.Split(string(src), "\n")[int(e["last_line"].(float64)-1)-1]
	if !strings.HasPrefix(line, "func ") || !strings.Contains(line, e["last"].(string)+"(") {
		t.Fatalf("last_line %v points at %q, want func %s", e["last_line"].(float64)-1, line, e["last"])
	}
}

// claudeLine is one Claude Code stream-json assistant line: message id,
// parent_tool_use_id ("" = parent session), input usage and one content block.
func claudeLine(id, parent string, input, cacheWrite int, block string) string {
	ptu := "null"
	if parent != "" {
		ptu = fmt.Sprintf("%q", parent)
	}
	return fmt.Sprintf(`{"type":"assistant","parent_tool_use_id":%s,"message":{"id":%q,"usage":{"input_tokens":%d,"cache_read_input_tokens":0,"cache_creation_input_tokens":%d,"output_tokens":1},"content":[%s]}}`, ptu, id, input, cacheWrite, block) + "\n"
}

func toolResultLine(parent, id, text string) string {
	ptu := "null"
	if parent != "" {
		ptu = fmt.Sprintf("%q", parent)
	}
	return fmt.Sprintf(`{"type":"user","parent_tool_use_id":%s,"message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":%q}]}}`, ptu, id, text) + "\n"
}

// parentChildRun mirrors the shape of a real Claude Code child-facts log:
// the parent's first message is split around the child's first line, the
// child's final report (seq 5) is never printed, and the parent and child
// share one proxy session.
//
// parent: seq1 ctx150 (in100+cw50) out20 -> Agent; seq6 ctx170 (in150+cw20) out9
// child:  seq2 ctx210 out5 Read(A); seq3 ctx310 (in160+cw150) out5 cleared40 -> Read(B);
//
//	seq4 ctx290 out5 clearedUses1 -> Read(A) again, same result (rework);
//	seq5 ctx410 out7 final report (unprinted)
func parentChildRun() (events, log string) {
	events = `{"events":[` +
		clearEvent(1, 100, 20, 50, 0) + "," +
		clearEvent(2, 200, 5, 10, 0) + "," +
		clearEventUses(3, 160, 5, 150, 40, 1) + "," +
		clearEventUses(4, 280, 5, 10, 0, 1) + "," +
		clearEventUses(5, 400, 7, 10, 0, 1) + "," +
		clearEvent(6, 150, 9, 20, 0) +
		`]}`
	read := func(id, file string) string {
		return fmt.Sprintf(`{"type":"tool_use","id":%q,"name":"Read","input":{"file_path":%q}}`, id, file)
	}
	log = claudeLine("mP1", "", 100, 50, `{"type":"thinking"}`) +
		claudeLine("mC1", "a1", 200, 10, read("r1", "A")) +
		claudeLine("mP1", "", 100, 50, `{"type":"tool_use","id":"a1","name":"Agent","input":{}}`) +
		toolResultLine("a1", "r1", "A body") +
		claudeLine("mC2", "a1", 160, 150, read("r2", "B")) +
		toolResultLine("a1", "r2", "B body") +
		claudeLine("mC3", "a1", 280, 10, read("r3", "A")) +
		toolResultLine("a1", "r3", "A body") +
		toolResultLine("", "a1", "report") +
		claudeLine("mP2", "", 150, 20, `{"type":"text","text":"done"}`) +
		`{"type":"result","usage":{"input_tokens":250,"cache_read_input_tokens":0,"cache_creation_input_tokens":70,"output_tokens":29}}` + "\n"
	return events, log
}

func TestClaudeChildAttribution(t *testing.T) {
	events, log := parentChildRun()
	path := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	parent, child, tokens, err := claudeChildAttribution(path, []byte(events))
	if err != nil || !reflect.DeepEqual(parent, []int64{1, 6}) || !reflect.DeepEqual(child, []int64{2, 3, 4, 5}) || tokens != 210+5+310+5+290+5+410+7 {
		t.Fatalf("got parent=%v child=%v tokens=%d err=%v", parent, child, tokens, err)
	}
	// A parent request the log does not print breaks the parent usage sum.
	if err := os.WriteFile(path, []byte(strings.Replace(log, `"input_tokens":250`, `"input_tokens":251`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := claudeChildAttribution(path, []byte(events)); err == nil {
		t.Fatal("parent usage mismatch was accepted")
	}
}

// TestApplyClearNetParentChild: per stream, parent saves nothing; the child
// saves 40 at seq3 with extra = cw3 - (cf3 - cf2) = 150 - (350-210) = 10 and
// rework = seq4's ctx+out = 295 (its Read(A) repeats the cleared ordinal 0).
// net = 40-10-295 = -265; actual = 170+215+315+295+417+179 = 1591.
func TestApplyClearNetParentChild(t *testing.T) {
	events, log := parentChildRun()
	dir, run := writeClearNetRun(t, events, log)
	run.SubagentCalls = 1
	run.ParentRequestSeqs, run.ChildRequestSeqs = []int64{1, 6}, []int64{2, 3, 4, 5}
	applyClearNet(dir, &run)
	if derefTestPtr(run.ClearSavedTokens) != 40 || derefTestPtr(run.ClearExtraCacheWrite) != 10 || derefTestPtr(run.ClearReworkTokens) != 295 || derefTestPtr(run.ClearNetTokens) != -265 {
		t.Fatalf("saved=%v extra=%v rework=%v net=%v", derefTestPtr(run.ClearSavedTokens), derefTestPtr(run.ClearExtraCacheWrite), derefTestPtr(run.ClearReworkTokens), derefTestPtr(run.ClearNetTokens))
	}
	if run.ClearNetPct == nil || !near(*run.ClearNetPct, -265.0/(1591-265)) {
		t.Fatalf("netPct = %v", derefTestPtr(run.ClearNetPct))
	}

	// Without a verified attribution the streams cannot be separated.
	dir, run = writeClearNetRun(t, events, log)
	applyClearNet(dir, &run)
	if run.ClearSavedTokens != nil || run.ClearExtraCacheWrite != nil || run.ClearReworkTokens != nil || run.ClearNetTokens != nil {
		t.Fatalf("unattributed child run was measured: saved=%v rework=%v", derefTestPtr(run.ClearSavedTokens), derefTestPtr(run.ClearReworkTokens))
	}
}

func TestChildSurveyEvidence(t *testing.T) {
	logFor := func(parent string, files []string) string {
		out := ""
		for i, name := range files {
			out += claudeLine(fmt.Sprintf("m%d", i), parent, i, 0, fmt.Sprintf(`{"type":"tool_use","id":"r%d","name":"Read","input":{"file_path":"/w/workspace/%s"}}`, i, name))
		}
		return out
	}
	path := filepath.Join(t.TempDir(), "agent.log")
	for _, tc := range []struct {
		name string
		log  string
		want bool
	}{
		{"child reads all", logFor("a1", childSurveyFiles), true},
		{"child skips one", logFor("a1", childSurveyFiles[1:]), false},
		{"parent reads", logFor("", childSurveyFiles), false},
	} {
		if err := os.WriteFile(path, []byte(tc.log), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := childSurveyEvidence(path); got != tc.want {
			t.Fatalf("%s: got %v", tc.name, got)
		}
	}
}

func TestCompareKeySubagentModel(t *testing.T) {
	base := compareKeySettings{Task: "child-survey", Agent: "claude", Model: "claude-sonnet-5"}
	opus := base
	opus.SubagentModel = "opus"
	if computeCompareKey(base) == computeCompareKey(opus) {
		t.Fatal("a different subagent model must change the compare key")
	}
	if raw, _ := json.Marshal(base); strings.Contains(string(raw), "SubagentModel") {
		t.Fatalf("empty subagent model changed existing keys: %s", raw)
	}
}
