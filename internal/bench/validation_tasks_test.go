package bench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidationTasksHaveIndependentAnswers(t *testing.T) {
	tasks, err := Tasks()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"dual-facts", "skill-proof", "child-facts"} {
		var task *Task
		for i := range tasks {
			if tasks[i].ID == id {
				task = &tasks[i]
				break
			}
		}
		if task == nil || task.Reference == nil {
			t.Fatalf("%s missing reference task", id)
		}
		workspace := filepath.Join(t.TempDir(), "workspace")
		if err := os.MkdirAll(workspace, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := task.Setup(workspace); err != nil {
			t.Fatal(err)
		}
		start, err := verify(*task, workspace, "")
		if err != nil || start.Solved {
			t.Fatalf("%s starting state passed: %+v, %v", id, start, err)
		}
		if err := task.Reference(workspace); err != nil {
			t.Fatal(err)
		}
		done, err := verify(*task, workspace, "")
		if err != nil || !done.Solved || done.Total < 2 {
			t.Fatalf("%s reference failed: %+v, %v", id, done, err)
		}
		if err := os.WriteFile(filepath.Join(workspace, "answer.json"), []byte(`{"module":"example.org/skill-bench","go":"1.25.0"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		missing, err := verify(*task, workspace, "")
		if err != nil || missing.Solved {
			t.Fatalf("%s accepted missing evidence: %+v, %v", id, missing, err)
		}
	}
}
