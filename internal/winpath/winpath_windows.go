//go:build windows

package winpath

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	// envKey is the per-user environment block; Explorer merges it with the
	// system one for every process it starts.
	envKey = `Environment`

	hwndBroadcast      = 0xffff // HWND_BROADCAST: every top-level window
	wmSettingChange    = 0x001a
	smtoAbortIfHung    = 0x0002
	broadcastTimeoutMs = 5000
)

var (
	user32DLL               = windows.NewLazySystemDLL("user32.dll")
	procSendMessageTimeoutW = user32DLL.NewProc("SendMessageTimeoutW")
)

func add(dir string) (bool, error) {
	cur, typ, err := readPath()
	if err != nil {
		return false, err
	}

	if containsEntry(splitPath(cur), dir) {
		return false, nil
	}

	newVal := cur
	if newVal != "" && !strings.HasSuffix(newVal, ";") {
		newVal += ";"
	}

	// Append-only string surgery: the untouched entries keep their exact
	// bytes (quoting, %VAR% references) rather than being normalized
	// through a split/join round-trip.
	newVal += dir

	if err := writePath(newVal, typ); err != nil {
		return false, err
	}

	broadcastEnvChange()

	return true, nil
}

func remove(dir string) (bool, error) {
	cur, typ, err := readPath()
	if err != nil {
		return false, err
	}

	entries := splitPath(cur)
	if !containsEntry(entries, dir) {
		return false, nil
	}

	var kept []string

	want := normalizeEntry(dir)

	for _, e := range entries {
		if normalizeEntry(e) != want {
			kept = append(kept, e)
		}
	}

	// Removing the last entry removes the value outright - an empty PATH
	// value behaves like no PATH value and confuses at least one installer
	// per decade.
	if len(kept) == 0 {
		k, err := registry.OpenKey(registry.CURRENT_USER, envKey, registry.SET_VALUE)
		if err != nil {
			return false, fmt.Errorf("cannot open the environment key: %w", err)
		}

		defer k.Close()

		if err := k.DeleteValue("Path"); err != nil {
			return false, fmt.Errorf("cannot clear the user PATH: %w", err)
		}

		broadcastEnvChange()

		return true, nil
	}

	if err := writePath(strings.Join(kept, ";"), typ); err != nil {
		return false, err
	}

	broadcastEnvChange()

	return true, nil
}

func contains(dir string) bool {
	cur, _, err := readPath()
	if err != nil {
		return false
	}

	return containsEntry(splitPath(cur), dir)
}

// readPath reads the raw user PATH plus its registry type, so the write-back
// can preserve REG_EXPAND_SZ vs REG_SZ. An absent value is the normal fresh
// machine, not an error.
func readPath() (string, uint32, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, envKey, registry.QUERY_VALUE)
	if err != nil {
		return "", 0, fmt.Errorf("cannot open the environment key: %w", err)
	}

	defer k.Close()

	cur, typ, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return "", registry.EXPAND_SZ, nil
	}

	if err != nil {
		return "", 0, fmt.Errorf("cannot read the user PATH: %w", err)
	}

	return cur, typ, nil
}

// writePath stores the new value under the same type it was read as.
func writePath(val string, typ uint32) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, envKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("cannot open the environment key: %w", err)
	}

	defer k.Close()

	if typ == registry.EXPAND_SZ {
		err = k.SetExpandStringValue("Path", val)
	} else {
		err = k.SetStringValue("Path", val)
	}

	if err != nil {
		return fmt.Errorf("cannot write the user PATH: %w", err)
	}

	return nil
}

func splitPath(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}

	return strings.Split(v, ";")
}

func containsEntry(entries []string, dir string) bool {
	want := normalizeEntry(dir)

	for _, e := range entries {
		if normalizeEntry(e) == want {
			return true
		}
	}

	return false
}

// normalizeEntry makes two PATH entries comparable: Windows paths are
// case-insensitive, trailing separators and quotes are noise.
func normalizeEntry(e string) string {
	e = strings.Trim(strings.TrimSpace(e), `"`)

	return strings.ToLower(filepath.Clean(e))
}

// broadcastEnvChange tells running top-level windows (Explorer above all)
// that an environment variable changed, so terminals opened from now on see
// the new PATH without a logoff. Already-running terminals keep their
// inherited environment; that is Windows semantics, not ours to fix.
func broadcastEnvChange() {
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}

	// #nosec G103 -- the pointer is the documented calling convention of the API.
	// #nosec G104 -- a failed broadcast costs a stale PATH until next logon; nothing actionable remains.
	_, _, _ = procSendMessageTimeoutW.Call(
		uintptr(hwndBroadcast),
		uintptr(wmSettingChange),
		0,
		uintptr(unsafe.Pointer(env)),
		uintptr(smtoAbortIfHung),
		uintptr(broadcastTimeoutMs),
		0,
	)
}
