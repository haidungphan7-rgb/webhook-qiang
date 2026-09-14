//go:build windows

package uninstallcmd

import (
	"os"
	"testing"
)

// TestOurProcessExcludesSelf is the regression test for the uninstaller
// killing itself: run from the installed directory (the "Apps & features"
// UninstallString path) the uninstaller is a webhook-zq.exe inside appDir,
// so without the pid exclusion it lands in its own kill list and every
// removal step after the taskkill never executes.
func TestOurProcessExcludesSelf(t *testing.T) {
	t.Parallel()

	if _, ok := ourProcess(uint32(os.Getpid()), "", nil, ""); ok {
		t.Fatal("ourProcess must never report the uninstaller's own pid as removable")
	}
}
