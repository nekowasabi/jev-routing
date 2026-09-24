//go:build !unix

package bench

import "os"

// sameOwner has no portable ownership check outside unix; treat as unknown
// and let cleanupOutsideTmp skip the deletion.
func sameOwner(info os.FileInfo) bool {
	return false
}
