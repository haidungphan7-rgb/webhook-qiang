//go:build windows

package installcmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows/registry"

	"github.com/yuandzhang/webhook-zq/internal/config"
)

// appListKey is where "Apps & features" reads its per-user entries from. Writing
// under HKCU needs no elevation, which is what keeps the whole install
// administrator-free.
const appListKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\webhook-zq`

// installTarget is where the binary is copied to: next to the config file, so
// `uninstall` removes the program and its settings as one unit.
func installTarget() (string, error) {
	appDir, err := config.AppDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(appDir, "bin", "webhook-zq.exe"), nil
}

// registerAppList creates the "Apps & features" entry. The UninstallString
// points back at the installed binary, so removing the app from Settings runs
// the interactive uninstaller - the experience users expect from any program.
func registerAppList(exePath, appVersion string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, appListKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("cannot open the app-list registry key: %w", err)
	}

	defer k.Close()

	values := []struct{ name, value string }{
		{"DisplayName", "webhook-zq"},
		{"DisplayVersion", appVersion},
		{"InstallLocation", filepath.Dir(exePath)},
		{"UninstallString", `"` + exePath + `" uninstall`},
		{"DisplayIcon", exePath + ",0"},
		{"URLInfoAbout", "https://github.com/haidungphan7-rgb/webhook-qiang"},
	}

	for _, v := range values {
		if err := k.SetStringValue(v.name, v.value); err != nil {
			return fmt.Errorf("cannot write %s: %w", v.name, err)
		}
	}

	if fi, err := os.Stat(exePath); err == nil {
		// #nosec G115 -- display-only estimate in KB; truncation above 4 GB is harmless.
		if err := k.SetDWordValue("EstimatedSize", uint32(fi.Size()/1024)); err != nil {
			return fmt.Errorf("cannot write EstimatedSize: %w", err)
		}
	}

	if err := k.SetDWordValue("NoModify", 1); err != nil {
		return fmt.Errorf("cannot write NoModify: %w", err)
	}

	if err := k.SetDWordValue("NoRepair", 1); err != nil {
		return fmt.Errorf("cannot write NoRepair: %w", err)
	}

	return nil
}

// enableAutostart registers a per-user logon task. A scheduled task needs no
// elevation either, unlike a Windows service.
func enableAutostart(exePath string) error {
	// #nosec G204 -- fixed command; the only variable is the installed exe path.
	out, err := exec.Command("schtasks", "/Create", "/TN", "webhook-zq",
		"/SC", "ONLOGON", "/TR", `"`+exePath+`" start`, "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot register the logon task: %w: %s", err, out)
	}

	fmt.Println("已注册开机自启（登录时自动 start）。")

	return nil
}
