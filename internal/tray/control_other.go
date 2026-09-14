//go:build !windows

package tray

import "time"

// WaitExited is a no-op on non-Windows: there is no tray to wait for.
func WaitExited(_ time.Duration) bool {
	return true
}

// SpawnFromServer is a no-op on non-Windows: there is no tray icon to bring up.
func SpawnFromServer(_ uint16) {}
