package bench

import (
	"strings"
	"testing"
)

func TestSourceRevisionIdentifiesRunningBinary(t *testing.T) {
	got := sourceRevision()
	if got == "unknown" || !strings.Contains(got, ":") {
		t.Fatalf("missing source identity: %q", got)
	}
}
