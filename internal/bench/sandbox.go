package bench

import (
	"os/exec"
	"sync"
)

// bwrapPath finds the bubblewrap binary once per process. Empty means
// bubblewrap is unavailable and the caller must fall back to running the
// agent unsandboxed (see run.go's sandboxMethod == "none").
var bwrapPath = sync.OnceValue(func() string {
	p, err := exec.LookPath("bwrap")
	if err != nil {
		return ""
	}
	return p
})

// bwrapAvailable reports whether bubblewrap can wrap the agent process.
func bwrapAvailable() bool {
	return bwrapPath() != ""
}

// bwrapWrap wraps file/args so the agent runs with a private, empty /tmp.
//
// Why: the host /tmp was shared by every run in a series (same bench
// process, same filesystem), so one run's /tmp writes could land in the
// next run's /tmp and get read as "foreign" — or worse, actually be read.
// A text-log audit can only catch this after the fact. bwrap prevents it
// structurally: --tmpfs /tmp gives the run a fresh, empty /tmp that
// disappears when the process exits, so nothing an agent leaves in /tmp
// can ever reach another run, and no other run's leftovers can be found
// there either. The root filesystem stays read-write bound at the same
// paths (--bind / /), so host credentials (~/.claude, ~/.codex), the git
// checkout, and the proxy on 127.0.0.1 are unaffected; the network
// namespace is shared by default (no --unshare-net). The run's own
// sandbox directory is re-bound explicitly so writes there still land on
// the host for verify/diff to see after the process exits.
func bwrapWrap(file string, args []string, sandbox string) (string, []string) {
	wrapped := []string{
		"--bind", "/", "/",
		"--tmpfs", "/tmp",
		"--bind", sandbox, sandbox,
		"--dev", "/dev",
		"--proc", "/proc",
		"--die-with-parent",
		"--",
		file,
	}
	return bwrapPath(), append(wrapped, args...)
}
