//go:build !windows

package uninstallcmd

import (
	"os"
	"path/filepath"

	"github.com/yuandzhang/webhook-zq/internal/config"
)

// platformItems has nothing to collect outside Windows: the installer never
// registers services, tasks or app-list entries there.
func platformItems() []item {
	return nil
}

// maybePause is a Windows-only affordance: elsewhere the uninstaller always
// runs in a terminal the user owns, so there is no window that could vanish.
func maybePause(bool) {}

// scanFiles reports the installed binary and the config directory when they
// exist.
func scanFiles() []item {
	var items []item

	if home, err := os.UserHomeDir(); err == nil {
		bin := filepath.Join(home, ".local", "bin", "webhook-zq")

		if _, err := os.Stat(bin); err == nil {
			items = append(items, item{
				label:  "程序文件",
				detail: bin,
				remove: func() error { return os.Remove(bin) },
			})
		}
	}

	if appDir, err := config.AppDir(); err == nil {
		if _, err := os.Stat(appDir); err == nil {
			items = append(items, item{
				label:  "配置文件",
				detail: appDir,
				remove: func() error { return os.RemoveAll(appDir) },
			})
		}
	}

	return items
}
