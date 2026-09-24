package bench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectAgentCommandDoesNotRoute(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_BASE_URL", "OPENAI_BASE_URL", "GROK_CLI_CHAT_PROXY_BASE_URL", "DEVIN_API_URL", "WINDSURF_API_SERVER_URL", "JEV_ROUTING_HOST"} {
		t.Setenv(key, "http://127.0.0.1:8890")
	}
	for _, agent := range []string{"claude", "codex", "grok", "devin"} {
		t.Run(agent, func(t *testing.T) {
			cmd, err := agentCommand(agent, "", "/tmp/work", "task", "", "medium", false, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(append(cmd.Args, cmd.Env...), " ")
			for _, leak := range []string{"http://:80", "http:///v1", `model_provider="jev"`, "JEV_ROUTING_HOST=", "127.0.0.1:8890"} {
				if strings.Contains(joined, leak) {
					t.Fatalf("direct %s retained proxy setting %q", agent, leak)
				}
			}
		})
	}
}

func TestClaimLockPreservesKeptSandbox(t *testing.T) {
	path, err := os.MkdirTemp("", "jev-bench-0-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })
	if err := os.WriteFile(filepath.Join(path, ".bench-keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := claimLock()
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("kept sandbox removed: %v", err)
	}
}

func TestXCellTasksVerifySavedAnswer(t *testing.T) {
	tasks, err := Tasks()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"xcell-module", "xcell-locate"} {
		t.Run(id, func(t *testing.T) {
			var task *Task
			for i := range tasks {
				if tasks[i].ID == id {
					task = &tasks[i]
				}
			}
			if task == nil || task.Setup == nil || task.Verify == nil || task.Reference == nil {
				t.Fatalf("task %q incomplete", id)
			}
			workspace := t.TempDir()
			if err := task.Setup(workspace); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(workspace, "go.mod")); err != nil {
				t.Fatal(err)
			}
			if err := task.Reference(workspace); err != nil {
				t.Fatal(err)
			}
			got, err := task.Verify(workspace)
			if err != nil || !got.Solved {
				t.Fatalf("reference verdict %+v, %v", got, err)
			}
			if err := os.WriteFile(filepath.Join(workspace, "answer.json"), []byte(`{}`), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err = task.Verify(workspace)
			if err != nil || got.Solved {
				t.Fatalf("empty answer verdict %+v, %v", got, err)
			}
			if err := task.Reference(workspace); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module changed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			got, err = task.Verify(workspace)
			if err != nil || got.Solved {
				t.Fatalf("modified fixture verdict %+v, %v", got, err)
			}
		})
	}
}
