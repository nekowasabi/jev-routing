//go:build unix

package bench

import (
	"os"
	"syscall"
)

// sameOwner reports whether path belongs to the current user.
// Why: a shared /tmp can hold another user's files; cleanup must only ever
// touch what this run's own agent created.
func sameOwner(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == os.Getuid()
}
