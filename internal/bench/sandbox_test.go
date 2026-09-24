package bench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBwrapIsolatesTmpAndPreservesSandboxWrites is the structural version of
// the audit fix: it proves the isolation instead of only detecting its
// absence after the fact. Skips when bubblewrap is not installed.
func TestBwrapIsolatesTmpAndPreservesSandboxWrites(t *testing.T) {
	if !bwrapAvailable() {
		t.Skip("bwrap not installed")
	}

	hostMarker := filepath.Join("/tmp", fmt.Sprintf("jev-bench-sandbox-test-host-%d", os.Getpid()))
	if err := os.WriteFile(hostMarker, []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(hostMarker) })

	sandbox := t.TempDir()
	sandboxFile := filepath.Join(sandbox, "wrote-to-sandbox")
	agentTmp := "/tmp/jev-bench-sandbox-test-agent-private"

	script := fmt.Sprintf(
		"test -f %s && echo SAW_HOST_MARKER; echo x > %s; echo x > %s",
		hostMarker, agentTmp, sandboxFile,
	)
	file, args := bwrapWrap("/bin/sh", []string{"-c", script}, sandbox)
	logPath := filepath.Join(sandbox, "out.log")
	res := runProc(context.Background(), file, args, sandbox, nil, logPath, 10*time.Second, nil)
	out, _ := os.ReadFile(logPath)
	if res.ExitCode != 0 {
		t.Fatalf("bwrap script exited %d:\n%s", res.ExitCode, out)
	}

	// Host /tmp file must be invisible inside the run's private tmpfs.
	if strings.Contains(string(out), "SAW_HOST_MARKER") {
		t.Fatalf("agent saw a host /tmp file across bwrap's tmpfs isolation:\n%s", out)
	}

	// The run's own sandbox directory is bind-mounted, so writes there
	// must still land on the host for verify/diff to see afterward.
	if _, err := os.Stat(sandboxFile); err != nil {
		t.Fatalf("sandbox write did not land on host: %v", err)
	}

	// Whatever the agent wrote to its private /tmp must not leak to the
	// host's /tmp, where a later (unsandboxed) run's audit could find it.
	if _, err := os.Stat(agentTmp); !os.IsNotExist(err) {
		t.Fatalf("agent's /tmp write leaked to the host: err=%v", err)
	}
}
