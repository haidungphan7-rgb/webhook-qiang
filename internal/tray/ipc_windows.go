//go:build windows

package tray

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Named kernel objects are the whole IPC layer (names live in control.go so
// the non-Windows stubs compile against them): a mutex is the single-instance
// guard; two auto-reset events carry "exit" and "uninstall" from control
// invocations (`webhook-zq tray --exit`) to the running tray. `Local\` scopes
// them to the current logon session, which is the right boundary: one tray
// per desktop, no cross-session signalling.

// acquireInstance tries to take the single-instance mutex.
// WAIT_OBJECT_0 or WAIT_ABANDONED both mean "the name is ours now" - an
// abandoned mutex is a previous tray that died without releasing, and the
// only sane recovery is to take over (the icon is gone either way).
func acquireInstance() (windows.Handle, bool) {
	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return 0, false
	}

	mu, err := windows.CreateMutex(nil, true, name)
	if err != nil {
		return 0, false
	}

	rc, err := windows.WaitForSingleObject(mu, 0)
	switch {
	case err == nil && rc == windows.WAIT_OBJECT_0:
		return mu, true
	case err == nil && rc == windows.WAIT_ABANDONED:
		return mu, true // predecessor died holding it; we inherit the name
	default:
		_ = windows.ReleaseMutex(mu)
		_ = windows.CloseHandle(mu)

		return 0, false // another tray is alive
	}
}

// releaseInstance drops the single-instance mutex. Call only on the way out,
// after every side effect (service stopped, uninstaller spawned) is done:
// whoever acquires the name next must not observe half-shutdown state.
func releaseInstance(mu windows.Handle) {
	_ = windows.ReleaseMutex(mu)
	_ = windows.CloseHandle(mu)
}

// probeInstance answers "is a tray running": the mutex is free within 200ms
// of trying means nobody is alive to hold it. The wait hides a tray that is
// mid-startup and has not reached its WaitForSingleObject yet.
func probeInstance() bool {
	return !nobodyHoldsMutex(200)
}

// waitInstanceReleased blocks until the mutex is free - the running tray's
// exit acknowledgement, because release is the last thing it does. The
// timeout is the caller's business; exceeding it means the tray is stuck.
func waitInstanceReleased(timeout time.Duration) bool {
	// #nosec G115 -- a 5s ack window; anything past 32 bits is absurd.
	return nobodyHoldsMutex(uint32(timeout.Milliseconds()))
}

// nobodyHoldsMutex tries to take the single-instance mutex within the given
// wait and drops it immediately: acquiring proves no live tray exists (the
// handle would be closed by the kernel the moment one died), holding it
// would make us the impostor. WAIT_ABANDONED counts as free - a predecessor
// died without releasing, and the only recovery is takeover.
func nobodyHoldsMutex(timeout uint32) bool {
	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return false
	}

	mu, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		return false
	}

	defer func() { _ = windows.CloseHandle(mu) }()

	rc, err := windows.WaitForSingleObject(mu, timeout)
	if err != nil {
		return false
	}

	if rc == windows.WAIT_OBJECT_0 {
		// We took it: release before closing, or the mutex stays held by a
		// dead handle and the next real tray is locked out.
		_ = windows.ReleaseMutex(mu)

		return true
	}

	return rc == windows.WAIT_ABANDONED
}

// createControlEvents makes the two auto-reset events the tray listens on.
// Events, unlike the mutex, are created up front and stay named for the whole
// tray lifetime so a control invocation can open them at any moment.
func createControlEvents() (exit, uninstall windows.Handle, err error) {
	exitName, _ := windows.UTF16PtrFromString(exitEventName)
	uninstallName, _ := windows.UTF16PtrFromString(uninstallEventName)

	if exit, err = windows.CreateEvent(nil, 0, 0, exitName); err != nil {
		return 0, 0, fmt.Errorf("cannot create the exit event: %w", err)
	}

	if uninstall, err = windows.CreateEvent(nil, 0, 0, uninstallName); err != nil {
		_ = windows.CloseHandle(exit)

		return 0, 0, fmt.Errorf("cannot create the uninstall event: %w", err)
	}

	return exit, uninstall, nil
}

// signalEvent fires a named event. ERROR_FILE_NOT_FOUND is translated to
// ErrNoTray - "no event object exists" is exactly "no tray created it", and
// callers branch on that (exit 1), not on raw errno strings.
func signalEvent(name string) error {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}

	ev, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, p)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return ErrNoTray
		}

		return err
	}

	defer func() { _ = windows.CloseHandle(ev) }()

	return windows.SetEvent(ev)
}

// ourProcessPids lists PIDs whose executable path is this very binary,
// excluding self. `start` and `tray` are the same exe file, so "stop the
// service" is exactly "kill every other process running our path" - a rule
// that can never hit a neighbour who merely shares the port, and never misses
// a service that moved ports. Matching is on the full image path (not the
// process name) so an unrelated binary that merely shares our name survives.
func ourProcessPids() []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}

	defer func() { _ = windows.CloseHandle(snap) }()

	self := canonicalPath(selfExePath())

	var (
		pids  []uint32
		entry windows.ProcessEntry32
	)

	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := windows.Process32First(snap, &entry); err != nil {
		return nil
	}

	for {
		if entry.ProcessID != windows.GetCurrentProcessId() {
			if canonicalPath(fullImagePath(entry.ProcessID)) == self {
				pids = append(pids, entry.ProcessID)
			}
		}

		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}

	return pids
}

// selfExePath is os.Executable with an argv[0] fallback - the comparison only
// needs consistency, not perfection.
func selfExePath() string {
	p, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}

	return p
}

// fullImagePath resolves a pid's executable path. Empty string on any
// failure: access-denied on system processes is routine and simply means
// "not ours".
func fullImagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}

	defer func() { _ = windows.CloseHandle(h) }()

	var buf [windows.MAX_PATH]uint16

	size := uint32(len(buf))

	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}

	return windows.UTF16ToString(buf[:size])
}

// canonicalPath lowercases and backslash-normalises a path for comparison.
// Case-insensitivity is a Windows filesystem fact; snapshots report whatever
// case the process was launched with.
func canonicalPath(p string) string {
	if p == "" {
		return ""
	}

	out := make([]rune, 0, len(p))

	for _, r := range p {
		switch {
		case r == '/':
			out = append(out, '\\')
		case r >= 'A' && r <= 'Z':
			out = append(out, r-'A'+'a')
		default:
			out = append(out, r)
		}
	}

	return string(out)
}

// stopService terminates every other process running our binary. Terminate
// is used deliberately: the service has no IPC to ask for a graceful stop,
// and the tray already showed the user what "exit" means.
func stopService() {
	for _, pid := range ourProcessPids() {
		if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); err == nil {
			_ = windows.TerminateProcess(h, 1)
			_ = windows.CloseHandle(h)
		}
	}
}

// jobObject wraps a kill-on-close job. Assigning the spawned service to it
// makes "tray dies for any reason" (crash, taskkill, logoff) take the
// service down too - no orphan server outliving its only handle.
type jobObject struct {
	handle windows.Handle
}

// newJobObject creates a job whose processes die when the last handle closes.
func newJobObject() (*jobObject, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot create the job object: %w", err)
	}

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE

	if _, err := windows.SetInformationJobObject(h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(h)

		return nil, fmt.Errorf("cannot configure the job object: %w", err)
	}

	return &jobObject{handle: h}, nil
}

// assign puts a child process into the job.
func (j *jobObject) assign(h windows.Handle) error {
	if j == nil {
		return nil
	}

	return windows.AssignProcessToJobObject(j.handle, h)
}

// close drops the job handle. KILL_ON_JOB_CLOSE does the rest.
func (j *jobObject) close() {
	if j != nil {
		_ = windows.CloseHandle(j.handle)
	}
}

// idYes is MessageBox's IDYES return value (6). Neither x/sys nor syscall
// exports it; the literal with a name beats a bare 6 in the comparison.
const idYes = 6

// confirmDialog is the one-gate MessageBox for destructive actions.
func confirmDialog(text, caption string) bool {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(caption)

	// #nosec G104 -- a failed MessageBox must read as "not confirmed".
	rc, _ := windows.MessageBox(0, t, c,
		windows.MB_YESNO|windows.MB_ICONQUESTION|windows.MB_SETFOREGROUND)

	return rc == idYes
}

// spawnFlagsHide is the creation flag set for background children: no window,
// no console of their own, fully detached from our lifetime.
const spawnFlagsHide = windows.DETACHED_PROCESS | windows.CREATE_NO_WINDOW

// spawnFlagsNewConsole is for children the user should watch: a fresh console
// that survives independently of the tray.
const spawnFlagsNewConsole = windows.CREATE_NEW_CONSOLE

// spawnHidden launches a child with no window of its own. The tray's service
// and tray children are background processes; anything they print goes to the
// redirected log file, never to a console the user did not open. extraEnv is
// appended to the inherited environment - the only entry ever passed is the
// FROM_TRAY marker on the service spawn.
func spawnHidden(exe string, args, extraEnv []string, stdout, stderr *os.File) (*exec.Cmd, error) {
	cmd := exec.Command(exe, args...)

	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}

	if stdout != nil {
		cmd.Stdout = stdout
	}

	if stderr != nil {
		cmd.Stderr = stderr
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: spawnFlagsHide}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return cmd, nil
}

// spawnNewConsole launches a child in its own console window - used for the
// doctor report and the interactive uninstaller, both of which exist to be
// read by a human before the window closes.
func spawnNewConsole(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: spawnFlagsNewConsole}

	return cmd.Start()
}

// openURL hands a URL to the shell; the default browser does the rest.
func openURL(u string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
}

// revealInExplorer opens Explorer with the file selected.
func revealInExplorer(path string) error {
	return exec.Command("explorer.exe", "/select,"+path).Start()
}

// taskbarPoll / taskbarMaxPolls are the Shell_TrayWnd readiness wait: a
// logon task fires before explorer has built the taskbar, and registering an
// icon that early makes it silently missing. 500ms x 120 = one minute of
// patience; the wait only gates icon registration, never the service.
const (
	taskbarPoll     = 500 * time.Millisecond
	taskbarMaxPolls = 120
)

// procFindWindowW is FindWindowW via LazyProc: x/sys/windows does not wrap
// it. Used only for the Shell_TrayWnd readiness poll.
var procFindWindowW = windows.NewLazySystemDLL("user32.dll").NewProc("FindWindowW")

// procFreeConsole is FreeConsole via LazyProc: same gap in x/sys/windows.
var procFreeConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole")

// waitForTaskbar blocks until the shell taskbar window exists (or the wait
// budget runs out). There is no error path by design: the worst outcome is a
// late icon, never a dead tray. FindWindowW is not wrapped by x/sys, hence
// the LazyProc.
//
//nolint:gosec // G103: unsafe.Pointer use is the Win32 calling convention here.
func waitForTaskbar() {
	name, err := windows.UTF16PtrFromString("Shell_TrayWnd")
	if err != nil {
		return
	}

	for range taskbarMaxPolls {
		if hwnd, _, _ := procFindWindowW.Call(
			uintptr(unsafe.Pointer(name)), 0); hwnd != 0 {
			return
		}

		time.Sleep(taskbarPoll)
	}
}

// freeConsole detaches from the console Task Scheduler (or a curious user in
// a terminal) attached us to. The last process leaving a console closes its
// window, which is what turns "a console flashes at logon" into "a console
// blinks and is gone".
func freeConsole() {
	// #nosec G104 -- no console to free is the common case, not an error.
	_, _, _ = procFreeConsole.Call()
}
