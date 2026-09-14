package tray

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/yuandzhang/webhook-zq/internal/config"
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

// healthy reports whether the service answers /healthz. This is the whole
// truth source for the icon colour: blue means the port answers, nothing else.
func healthy(port uint16) bool {
	client := &http.Client{Timeout: 2 * time.Second}

	res, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		return false
	}

	defer res.Body.Close()

	return res.StatusCode == http.StatusOK
}

// uiBaseURL is where the browser goes for "Open interface".
func uiBaseURL(port uint16) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// serverLogPath resolves <app dir>\logs\server.log - the file the tray
// redirects its spawned service into, and the file "Open log" reveals.
func serverLogPath() (string, error) {
	appDir, err := config.AppDir()
	if err != nil {
		return "", err
	}

	return appDir + string(os.PathSeparator) + "logs" + string(os.PathSeparator) + "server.log", nil
}

// IsRunning answers whether a tray holds the single-instance mutex.
func IsRunning() bool {
	return probeInstance()
}

// WaitExited blocks until the running tray releases the single-instance mutex
// (its exit acknowledgement: release is the last thing a shutting-down tray
// does, after the service is gone) or the timeout passes. False means the
// tray acknowledged but did not finish - stuck, not silent.
func WaitExited(timeout time.Duration) bool {
	if runtime.GOOS != "windows" {
		return true
	}

	return waitInstanceReleased(timeout)
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

// SpawnFromServer is the hook `start` calls once its port is listening: bring
// up the tray icon for this service. Best effort by design - a tray failure
// must never take a healthy server down. The FROM_TRAY env is deliberately
// NOT set here: it is the marker for processes the tray spawned, and this
// tray belongs to a server the user started.
func SpawnFromServer(port uint16) {
	if runtime.GOOS != "windows" {
		return
	}

	exe, err := os.Executable()
	if err != nil {
		return
	}

	// #nosec G204 -- fixed binary (ourselves), the only variable is the port.
	cmd, err := spawnHidden(exe, []string{"tray", "--port", strconv.Itoa(int(port))}, nil, nil, nil)
	if err != nil {
		return
	}

	// Reap the child; a zombie tray process would hold the single-instance
	// mutex after death otherwise (abandoned, but still ugly).
	go func() { _ = cmd.Wait() }()
}
