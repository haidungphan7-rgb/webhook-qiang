//go:build !windows

package tray

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"
)

// The tray is a Windows-only affordance (fyne.io/systray there needs no cgo).
// On other platforms the hidden `tray` subcommand still exists so scripts and
// docs never hit "unknown command", but it says no instead of half-working.

const errUnsupported = "the tray is Windows-only; on " + runtime.GOOS + " run `start` directly"

func acquireInstance() (any, bool)           { return nil, false }
func releaseInstance(any)                    {}
func probeInstance() bool                    { return false }
func waitInstanceReleased(time.Duration) bool { return false }
func createControlEvents() (any, any, error) { return nil, nil, fmt.Errorf("%s", errUnsupported) }
func signalEvent(string) error               { return ErrNoTray }
func stopService()                           {}
func newJobObject() (*jobObject, error)      { return nil, fmt.Errorf("%s", errUnsupported) }
func confirmDialog(string, string) bool      { return false }
func waitForTaskbar()                        {}
func freeConsole()                           {}

type jobObject struct{}

func (j *jobObject) assign(any) error { return nil }
func (j *jobObject) close()           {}

const spawnFlagsHide = 0
const spawnFlagsNewConsole = 0

func spawnHidden(exe string, args, extraEnv []string, stdout, stderr *os.File) (*exec.Cmd, error) {
	return nil, fmt.Errorf("%s", errUnsupported)
}

func spawnNewConsole(exe string, args ...string) error {
	return fmt.Errorf("%s", errUnsupported)
}

func openURL(string) error          { return fmt.Errorf("%s", errUnsupported) }
func revealInExplorer(string) error { return fmt.Errorf("%s", errUnsupported) }
