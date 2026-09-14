//go:build !windows

package installcmd

import (
	"fmt"
	"os"
	"path/filepath"
)

// installTarget puts the binary on ~/.local/bin, the one user-writable location
// that is on PATH by default on most Linux distributions. macOS users who do
// not have it on PATH get told so instead of being silently installed somewhere
// useless.
func installTarget() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve the home directory: %w", err)
	}

	return filepath.Join(home, ".local", "bin", "webhook-zq"), nil
}

// registerAppList is a no-op outside Windows: there is no system-wide app list
// to register with, and pretending otherwise would only confuse.
func registerAppList(exePath, appVersion string) error {
	return nil
}

// enableAutostart defers to the platform's own service manager; a portable
// binary has no business rewriting shell init files.
func enableAutostart(exePath string) error {
	fmt.Println("此平台的开机自启请使用系统服务管理器（systemd / launchd）。")

	return nil
}
