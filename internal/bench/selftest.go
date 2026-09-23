package bench

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Selftest proves the chess tasks measure what they claim, without an agent.
func Selftest(w io.Writer) int {
	if w == nil {
		w = os.Stdout
	}
	tasks, err := Tasks()
	if err != nil {
		fmt.Fprintln(w, err)
		return 1
	}
	failures := 0
	expect := func(what string, ok bool, detail string) {
		mark := "ok  "
		if !ok {
			mark = "FAIL"
			failures++
		}
		if detail != "" {
			fmt.Fprintf(w, "%s %s  (%s)\n", mark, what, detail)
			return
		}
		fmt.Fprintf(w, "%s %s\n", mark, what)
	}
	for _, task := range tasks {
		start, err := scoreTask(task, nil)
		if err != nil {
			expect(task.ID+": score starting workspace", false, err.Error())
			continue
		}
		expect(task.ID+": the starting workspace does not already pass", start.Passed < start.Total, fmt.Sprintf("%d/%d", start.Passed, start.Total))
		sol := Solution()
		done, err := scoreTask(task, &sol)
		if err != nil {
			expect(task.ID+": score reference", false, err.Error())
			continue
		}
		expect(task.ID+": the reference solution passes every check", done.Passed == done.Total && done.Total > 0, fmt.Sprintf("%d/%d %s", done.Passed, done.Total, joinFailed(done.Failed)))
	}
	var bugfix Task
	for _, task := range tasks {
		if task.ID == "chess-bugfix" {
			bugfix = task
		}
	}
	base := EngineWithoutSan()
	for _, b := range Bugs {
		src, err := inject(base, []bug{b})
		if err != nil {
			expect("chess-bugfix: "+b.what, false, err.Error())
			continue
		}
		result, err := scoreTask(bugfix, &src)
		if err != nil {
			expect("chess-bugfix: "+b.what, false, err.Error())
			continue
		}
		detail := ""
		if len(result.Failed) > 0 {
			detail = result.Failed[0]
			if len(result.Failed) > 1 {
				detail += "; " + result.Failed[1]
			}
		}
		expect(fmt.Sprintf("chess-bugfix: %q is caught on its own", b.what), result.Passed < result.Total, detail)
	}
	if failures > 0 {
		return 1
	}
	return 0
}

func joinFailed(failed []string) string {
	out := ""
	for i, name := range failed {
		if i > 0 {
			out += "; "
		}
		out += name
	}
	return out
}

func scoreTask(task Task, source *string) (verdict, error) {
	dir, err := os.MkdirTemp("", "jev-bench-selftest-")
	if err != nil {
		return verdict{}, err
	}
	defer os.RemoveAll(dir)
	if err := task.Setup(dir); err != nil {
		return verdict{}, err
	}
	if source != nil {
		if err := os.WriteFile(filepath.Join(dir, "src", "chess.js"), []byte(*source), 0o644); err != nil {
			return verdict{}, err
		}
	}
	return verify(task, dir, "")
}
