package bench

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCleanupOutsideTmpOnlyRemovesCreatedTmpFiles covers the /tmp litter a
// later run's audit would otherwise misread as "found" and flag as
// contamination (see run.go's TMPDIR handling).
func TestCleanupOutsideTmpOnlyRemovesCreatedTmpFiles(t *testing.T) {
	dir := t.TempDir() // under os.TempDir(), i.e. inside a tmp root
	created := filepath.Join(dir, "out.txt")
	found := filepath.Join(dir, "chess_backup.js")
	subdir := filepath.Join(dir, "sub")
	for _, p := range []string{created, found} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}

	outside := []Touch{
		{Path: created, How: "created", real: created},
		{Path: found, How: "found", real: found},
		{Path: subdir, How: "created", real: subdir},
		{Path: "/etc/hosts", How: "created", real: "/etc/hosts"}, // not under a tmp root
	}
	result := cleanupOutsideTmp(outside)

	if len(result.Cleaned) != 1 || result.Cleaned[0] != created {
		t.Fatalf("cleaned = %v, want only %s", result.Cleaned, created)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("created file survived cleanup: err=%v", err)
	}
	if _, err := os.Stat(found); err != nil {
		t.Fatalf("found file must survive: %v", err)
	}
	if _, err := os.Stat(subdir); err != nil {
		t.Fatalf("directory must survive: %v", err)
	}
	if _, err := os.Stat("/etc/hosts"); err != nil {
		t.Skip("no /etc/hosts on this system")
	}
}
