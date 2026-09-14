package tray

import (
	"errors"
	"os"
)

// Named kernel objects are the whole IPC layer. A mutex is the single-instance
// guard; two auto-reset events carry "exit" and "uninstall" from control
// invocations (`webhook-zq tray --exit`) to the running tray. `Local\` scopes
// them to the current logon session, which is the right boundary: one tray
// per desktop, no cross-session signalling. The names live here (not in the
// Windows-only file) because the non-Windows stubs reference them too.
const (
	mutexName          = `Local\webhook-zq-tray`
	exitEventName      = `Local\webhook-zq-tray-exit`
	uninstallEventName = `Local\webhook-zq-tray-uninstall`
)

// FromTrayKey / FromTrayValue mark a service process the tray spawned. The
// service checks it before spawning its own tray: tray -> start -> tray ->
// ... is otherwise an infinite loop with an icon as its only symptom.
const (
	FromTrayKey   = "WEBHOOK_ZQ_FROM_TRAY"
	FromTrayValue = "1"
)

// FromTrayEnv is the environment entry appended to the spawned service's
// environment: os.Environ() + this string.
const FromTrayEnv = FromTrayKey + "=" + FromTrayValue

// SpawnedFromTray answers whether this process was started by the tray - the
// one process that must not answer by spawning a tray of its own.
func SpawnedFromTray() bool {
	return os.Getenv(FromTrayKey) != ""
}

// ErrAlreadyRunning is what a second tray reports after doing the one useful
// thing a second instance can do: opening the interface in a browser. The CLI
// turns it into a silent exit 0 - never an error dialog.
var ErrAlreadyRunning = errors.New("tray already running")

// ErrNoTray means no tray is running to receive a control signal. Distinct
// from any other failure so callers can map it onto "exit 1" without
// pattern-matching errno strings.
var ErrNoTray = errors.New("tray not running")

// Options configures one tray run.
type Options struct {
	// Port the tray probes and links to; the service and the tray must agree.
	Port uint16
}

// IsRunning answers whether a tray holds the single-instance mutex.
func IsRunning() bool {
	return probeInstance()
}

// RequestExit signals the running tray to quit (taking the service with it).
// ERROR_FILE_NOT_FOUND means no tray - the caller reports that, not us.
func RequestExit() error {
	return signalEvent(exitEventName)
}

// RequestUninstall signals the running tray to run its uninstall flow.
func RequestUninstall() error {
	return signalEvent(uninstallEventName)
}
