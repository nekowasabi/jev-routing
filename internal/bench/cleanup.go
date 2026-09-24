package bench

import (
	"os"
	"path/filepath"
	"strings"
)

// CleanupResult is what cleanupOutsideTmp removed after a run.
type CleanupResult struct {
	Cleaned []string
	Failed  []string
}

// cleanupOutsideTmp removes the regular files a run created (Touch.How ==
// "created") outside its sandbox, when they landed under the OS temp
// directory. Directories, symlinks, paths outside the temp directory, and
// files owned by another user are left alone and simply not reported as
// cleaned.
//
// Why: agents sometimes ignore TMPDIR (run.go sets it, but the agent is free
// to write straight to /tmp) and bench does not otherwise clean /tmp, so a
// later run's audit finds the earlier run's leftovers with how=found and
// gets misclassified as contaminated.
func cleanupOutsideTmp(outside []Touch) CleanupResult {
	roots := tmpRoots()
	var result CleanupResult
	for _, touch := range outside {
		if touch.How != "created" || !underAny(touch.real, roots) {
			continue
		}
		info, err := os.Lstat(touch.real)
		if err != nil {
			continue // already gone; nothing to clean
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() || !sameOwner(info) {
			continue
		}
		if err := os.Remove(touch.real); err != nil {
			result.Failed = append(result.Failed, touch.real)
			continue
		}
		result.Cleaned = append(result.Cleaned, touch.real)
	}
	return result
}

// snapshotTmp lists the immediate entries of each tmp root before an agent
// starts. Audit uses it to tell "existed before this run" (another run's
// leftovers, real contamination) from "made during this run by a command
// the regex-based creator check cannot parse" (e.g. a Bash heredoc, cp,
// node's fs.writeFileSync, or a relative-path mkdir later referenced by
// its absolute path).
func snapshotTmp() map[string]bool {
	seen := map[string]bool{}
	for _, root := range tmpRoots() {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			seen[filepath.Join(root, entry.Name())] = true
		}
	}
	return seen
}

// topLevelTmpEntry returns the immediate child of a tmp root that path is
// under (e.g. /tmp/jev-bench-1-2/workspace -> /tmp/jev-bench-1-2), and
// whether path is under a tmp root at all.
func topLevelTmpEntry(path string) (string, bool) {
	for _, root := range tmpRoots() {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if i := strings.IndexRune(rel, filepath.Separator); i >= 0 {
			rel = rel[:i]
		}
		return filepath.Join(root, rel), true
	}
	return "", false
}

func tmpRoots() []string {
	roots := map[string]bool{"/tmp": true}
	if d := os.TempDir(); d != "" {
		roots[d] = true
	}
	out := make([]string, 0, len(roots))
	for r := range roots {
		out = append(out, r)
	}
	return out
}

// underAny reports whether path is strictly inside one of roots (not the root itself).
func underAny(path string, roots []string) bool {
	if path == "" {
		return false
	}
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
			continue
		}
		return true
	}
	return false
}
