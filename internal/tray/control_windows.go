//go:build windows

package tray

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/yuandzhang/webhook-zq/internal/config"
)

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

// WaitExited blocks until the running tray releases the single-instance mutex
// (its exit acknowledgement: release is the last thing a shutting-down tray
// does, after the service is gone) or the timeout passes. False means the
// tray acknowledged but did not finish - stuck, not silent.
func WaitExited(timeout time.Duration) bool {
	return waitInstanceReleased(timeout)
}

// SpawnFromServer is the hook `start` calls once its port is listening: bring
// up the tray icon for this service. Best effort by design - a tray failure
// must never take a healthy server down. The FROM_TRAY env is deliberately
// NOT set here: it is the marker for processes the tray spawned, and this
// tray belongs to a server the user started.
func SpawnFromServer(port uint16) {
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
