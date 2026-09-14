//go:build windows

package status

import (
	"golang.org/x/sys/windows"
)

// processPath resolves the executable behind a pid via the native API. The
// previous implementation shelled out to wmic, which current Windows
// installs no longer ship; QueryFullProcessImageName is the supported
// replacement and spawns no child process to do it.
func processPath(pid int) string {
	// #nosec G115 -- pids come from the local port table, which lists
	// DWORDs.
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}

	defer func() { _ = windows.CloseHandle(h) }()

	// Long-path headroom: the binary may sit under a deep user profile.
	var buf [1024]uint16

	size := uint32(len(buf))

	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}

	return windows.UTF16ToString(buf[:size])
}
