package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

type verdict struct {
	Passed   int
	Total    int
	Score    float64
	Solved   bool
	Failed   []string
	TimedOut bool
}

func verifierScript() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "jev-routing", "bench")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "verify.mjs")
	body, err := chessFS.ReadFile("assets/chess/verify.mjs")
	if err != nil {
		return "", err
	}
	current, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(current, body) {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return "", err
		}
	}
	return path, nil
}

func verify(task Task, workspace, logFile string) (verdict, error) {
	if task.Verify != nil {
		return task.Verify(workspace)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return verdict{}, fmt.Errorf("node is required to score chess tasks: %w", err)
	}
	script, err := verifierScript()
	if err != nil {
		return verdict{}, err
	}
	args := []string{script, workspace}
	if task.San {
		args = append(args, "--san")
	}
	var total int
	var checks []struct {
		Name string `json:"name"`
		OK   bool   `json:"ok"`
	}
	out := runProc(context.Background(), node, args, "", nil, logFile, 3*time.Minute, func(line string) {
		var entry struct {
			Total *int   `json:"total"`
			Name  string `json:"name"`
			OK    bool   `json:"ok"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			return
		}
		if entry.Total != nil {
			total = *entry.Total
			return
		}
		if entry.Name != "" {
			checks = append(checks, struct {
				Name string `json:"name"`
				OK   bool   `json:"ok"`
			}{entry.Name, entry.OK})
		}
	})
	passed := 0
	var failed []string
	for _, check := range checks {
		if check.OK {
			passed++
		} else {
			failed = append(failed, check.Name)
		}
	}
	score := 0.0
	if total > 0 {
		score = float64(passed) / float64(total)
	}
	v := verdict{Passed: passed, Total: total, Score: score, Solved: total > 0 && passed == total, Failed: failed, TimedOut: out.TimedOut}
	return v, nil
}

func verifyAnswer(workspace string, expected map[string]any) (verdict, error) {
	raw, err := os.ReadFile(filepath.Join(workspace, "answer.json"))
	var got map[string]any
	if err == nil {
		_ = json.Unmarshal(raw, &got)
	}
	v := verdict{Total: len(expected)}
	keys := make([]string, 0, len(expected))
	for key := range expected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		want := expected[key]
		if got[key] == want {
			v.Passed++
		} else {
			v.Failed = append(v.Failed, key)
		}
	}
	v.Score = float64(v.Passed) / float64(v.Total)
	v.Solved = v.Passed == v.Total
	return v, nil
}
