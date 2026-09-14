//go:build !windows

package status

import (
	"fmt"
	"os"
)

// processPath resolves the executable behind a pid via /proc. macOS has no
// /proc, so the path stays empty there - it is only used to prove the
// process is ours, and an empty answer reads as "unknown", not "error".
func processPath(pid int) string {
	if p, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
		return p
	}

	return ""
}
