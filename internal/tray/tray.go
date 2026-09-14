//go:build windows

// Package tray is the Windows tray icon: a hidden `tray` subcommand of the
// same single binary the service runs in. The icon is the switch a non-
// technical user gets - start/stop/uninstall without a terminal, without a
// stray console window, and without an orphan server when it closes.
package tray

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/windows"

	"github.com/yuandzhang/webhook-zq/internal/config"
	"github.com/yuandzhang/webhook-zq/internal/tray/assets"
)

// pollInterval is how stale the icon may be. Ten seconds matches the
// "glanceable, not chatty" bar: a stopped server turns the icon grey within
// one glance away and back.
const pollInterval = 10 * time.Second

type iconState int32

const (
	stateStopped iconState = iota
	stateStarting
	stateRunning
)

// app holds everything the menu actions and the poller share.
type app struct {
	opts Options

	state atomic.Int32

	// spawned guards "one spawn attempt at a time": ensureService may be
	// called from Run, from the menu and from itself; only the first may
	// spawn. Reset whenever the service is confirmed gone, so "start"
	// after a manual stop works again.
	spawned atomic.Bool

	// stopping makes every exit path idempotent: menu quit, `tray --exit`
	// and a double click on exit all funnel through shutdown once.
	stopping atomic.Bool

	// uninstalling marks "the uninstaller was already spawned" so the
	// post-Run cleanup does not kill the uninstaller - it runs our exe
	// path too, and stopService() matches by exe path.
	uninstalling atomic.Bool

	mToggle  *systray.MenuItem
	toggleMu sync.Mutex
}

// Run acquires the single-instance mutex, adopts or starts the service and
// blocks in systray.Run until the tray quits.
func Run(opts Options) error {
	// First breath: drop the console Task Scheduler (or a terminal) gave us.
	// The last process leaving a console closes its window - this is what
	// keeps a logon task from flashing a black window at the user.
	freeConsole()

	mu, ok := acquireInstance()
	if !ok {
		// Second instance: the only useful action is surfacing the
		// interface, then dying quietly. No error, no second icon.
		_ = openURL(uiBaseURL(opts.Port))

		return ErrAlreadyRunning
	}

	exitEv, uninstallEv, err := createControlEvents()
	if err != nil {
		releaseInstance(mu)

		return err
	}

	a := &app{opts: opts}

	// The service does not wait for the taskbar: a logon task may fire
	// before explorer exists, and "server up" must not lag behind "desktop
	// drawn". Only the icon registration waits below.
	go a.ensureService()

	go a.watchExit(exitEv)
	go a.watchUninstall(uninstallEv)
	go a.poll()

	waitForTaskbar()

	systray.Run(a.onReady, nil)

	// Decision 4 ordering: the service dies BEFORE the mutex is released,
	// so the next tray never adopts a half-dead instance. The uninstaller
	// path is the exception - it owns process cleanup from here on.
	if !a.uninstalling.Load() {
		stopService()
	}

	releaseInstance(mu)

	return nil
}

// onReady builds the menu. Item order is the lead-approved final layout:
// open (first position doubles as the default/bold entry), then the two
// high-frequency actions, diagnostics, and only then the destructive ones
// below separators.
func (a *app) onReady() {
	systray.SetIcon(assets.Stopped())
	a.applyState(a.currentState())

	mOpen := systray.AddMenuItem("打开界面", "在浏览器中打开 webhook-zq")
	mToggle := systray.AddMenuItem("启动服务", "启动或停止后台服务")
	mSettings := systray.AddMenuItem("打开设置页", "浏览器打开设置")
	mDoctor := systray.AddMenuItem("自检", "在新窗口运行 doctor 并显示结果")
	mLog := systray.AddMenuItem("打开日志", "在资源管理器中定位服务日志")

	systray.AddSeparator()

	mUninstall := systray.AddMenuItem("卸载 webhook-zq…", "停止服务并打开卸载向导")

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("退出", "退出托盘并停止服务")

	a.mToggle = mToggle

	go func() {
		for range mOpen.ClickedCh {
			_ = openURL(uiBaseURL(a.opts.Port))
		}
	}()

	go func() {
		for range mToggle.ClickedCh {
			a.toggle()
		}
	}()

	go func() {
		for range mSettings.ClickedCh {
			_ = openURL(uiBaseURL(a.opts.Port) + "/settings")
		}
	}()

	go func() {
		for range mDoctor.ClickedCh {
			if exe, err := os.Executable(); err == nil {
				_ = spawnNewConsole(exe, "doctor")
			}
		}
	}()

	go func() {
		for range mLog.ClickedCh {
			a.revealLog()
		}
	}()

	go func() {
		for range mUninstall.ClickedCh {
			a.beginUninstall()
		}
	}()

	go func() {
		for range mQuit.ClickedCh {
			a.shutdown()
		}
	}()
}

// setState is the single writer of icon, tooltip and toggle title, so the
// three can never disagree. A blue/grey flip also appends one line to
// logs/tray.log - the human-facing trail the acceptance plan reads when a
// status exit code alone cannot explain what happened.
//
// Tooltip red line: only the three fixed templates plus the port number.
// The library truncates silently past 127 UTF-16 units and drops the NUL
// terminator doing it - dynamic text (counts, errors) belongs to toasts,
// which this release deliberately does not have.
func (a *app) setState(s iconState) {
	prev := iconState(a.state.Swap(int32(s)))
	a.applyState(s)

	if colourOf(s) != colourOf(prev) {
		a.logTransition(s)
	}
}

// colourOf maps the three states onto the two colours the icon actually has.
func colourOf(s iconState) string {
	if s == stateRunning {
		return "blue"
	}

	return "grey"
}

// logTransition appends one line per blue/grey flip to logs/tray.log, next to
// server.log. Format matches the acceptance plan's example; the port is the
// only variable because the pid would need a process sweep nobody should run
// on every poll tick.
func (a *app) logTransition(s iconState) {
	path, err := trayLogPath()
	if err != nil {
		return
	}

	if mkErr := os.MkdirAll(parentDir(path), 0o755); mkErr != nil {
		return
	}

	// #nosec G304 -- fixed path from our own config directory.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}

	defer f.Close()

	if s == stateRunning {
		_, _ = fmt.Fprintf(f, "%s icon blue: service reachable (port %d)\n",
			time.Now().Format(time.RFC3339), a.opts.Port)
	} else {
		_, _ = fmt.Fprintf(f, "%s icon grey: service unreachable (port %d)\n",
			time.Now().Format(time.RFC3339), a.opts.Port)
	}
}

// poll refreshes the icon on a fixed cadence. Icon colour is a pure function
// of "does /healthz answer" - never of "is the tray process alive".
func (a *app) poll() {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for range ticker.C {
		if a.stopping.Load() {
			return
		}

		a.refresh()
	}
}

// refresh recomputes the state from the port. A starting state is kept while
// the spawned service has not answered yet; the watcher in ensureService is
// the one that demotes a dead spawn to stopped.
func (a *app) refresh() {
	if healthy(a.opts.Port) {
		a.setState(stateRunning)
	}
}

func (a *app) currentState() iconState { return iconState(a.state.Load()) }

func (a *app) applyState(s iconState) {
	switch s {
	case stateRunning:
		systray.SetIcon(assets.Running())
		systray.SetTooltip(fmt.Sprintf("webhook-zq 运行中 · :%d", a.opts.Port))
		a.setToggleTitle("停止服务")
	case stateStarting:
		systray.SetIcon(assets.Stopped())
		systray.SetTooltip("webhook-zq 启动中…")
		a.setToggleTitle("停止服务")
	case stateStopped:
		systray.SetIcon(assets.Stopped())
		systray.SetTooltip("webhook-zq 已停止")
		a.setToggleTitle("启动服务")
	}
}

func (a *app) setToggleTitle(title string) {
	if a.mToggle != nil {
		a.mToggle.SetTitle(title)
	}
}

// toggle maps the one start/stop menu item onto the current state.
func (a *app) toggle() {
	a.toggleMu.Lock()
	defer a.toggleMu.Unlock()

	if healthy(a.opts.Port) {
		stopService() // grey follows on the next poll beat
		a.spawned.Store(false)
		a.setState(stateStopped)

		return
	}

	a.ensureService()
}

// ensureService adopts a healthy server or spawns one. Adopting is what
// keeps a user-started `start` and the tray from fighting: the tray never
// spawns a second server onto a busy port.
func (a *app) ensureService() {
	if healthy(a.opts.Port) {
		a.setState(stateRunning)

		return
	}

	if !a.spawned.CompareAndSwap(false, true) {
		return // a spawn is already in flight
	}

	a.spawnService()
}

// spawnService starts `start` hidden with stdout/stderr appended to
// logs/server.log and ties it to a kill-on-close job object.
func (a *app) spawnService() {
	exe, err := os.Executable()
	if err != nil {
		return
	}

	logPath, err := serverLogPath()
	if err != nil {
		return
	}

	if mkErr := os.MkdirAll(parentDir(logPath), 0o755); mkErr != nil {
		return
	}

	// #nosec G30 -- a per-user log file is exactly as private as the config
	// directory it lives in, and 0644 matches what the logger would create.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}

	defer logFile.Close()

	job, err := newJobObject()
	if err != nil {
		return
	}

	// #nosec G204 -- fixed binary (ourselves) with fixed arguments. The env
	// marker is the loop guard: the spawned `start` sees it and refrains
	// from spawning a tray of its own.
	cmd, err := spawnHidden(exe, []string{"start", "--port", fmt.Sprint(a.opts.Port)},
		[]string{FromTrayEnv}, logFile, logFile)
	if err != nil {
		job.close()

		return
	}

	// os.Process hides its handle, so take one of our own for the job
	// assignment. PROCESS_SET_QUOTA is exactly what AssignProcessToJobObject
	// needs - no broader rights on our own child.
	// #nosec G115 -- Pid comes from the OS as a DWORD already.
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA, false, uint32(cmd.Process.Pid))
	if err == nil {
		defer func() { _ = windows.CloseHandle(proc) }() // crash net only

		if err := job.assign(proc); err != nil {
			// The job is only the crash net; the stop path kills by exe
			// path. A failed assign is survivable.
			job.close()
		}
	}

	a.setState(stateStarting)

	go func() {
		_ = cmd.Wait()

		// The spawned service is gone. Whatever the port says now is
		// the truth; if nothing answers, show stopped.
		time.Sleep(500 * time.Millisecond)

		if a.stopping.Load() {
			return
		}

		if healthy(a.opts.Port) {
			a.setState(stateRunning) // somebody else took over
		} else {
			a.spawned.Store(false) // allow the menu start to try again
			a.setState(stateStopped)
		}
	}()
}

// watchExit handles `webhook-zq tray --exit`: stop the service, then quit.
func (a *app) watchExit(ev windows.Handle) {
	rc, err := windows.WaitForSingleObject(ev, windows.INFINITE)
	if err != nil || rc != windows.WAIT_OBJECT_0 {
		return
	}

	stopService()
	a.shutdown()
}

// watchUninstall handles `webhook-zq tray --uninstall`.
func (a *app) watchUninstall(ev windows.Handle) {
	rc, err := windows.WaitForSingleObject(ev, windows.INFINITE)
	if err != nil || rc != windows.WAIT_OBJECT_0 {
		return
	}

	a.beginUninstall()
}

// beginUninstall is the tray's one-gate uninstall flow: confirm, stop the
// service, hand off to the interactive uninstaller in its own console, quit.
// The uninstaller runs as a sibling process so it may delete the exe file
// the tray itself is running from.
func (a *app) beginUninstall() {
	if a.stopping.Load() {
		return
	}

	if !confirmDialog(
		"确定要卸载 webhook-zq 吗？\n\n将停止服务并打开卸载向导；数据库默认保留。",
		"卸载 webhook-zq",
	) {
		return
	}

	exe, err := os.Executable()
	if err != nil {
		return
	}

	a.uninstalling.Store(true)

	stopService()

	// Interactive on purpose: no --yes. The uninstall wizard in its own
	// console is the same experience as "设置 → 应用 → 卸载".
	if err := spawnNewConsole(exe, "uninstall"); err != nil {
		a.uninstalling.Store(false)

		return
	}

	a.shutdown()
}

// shutdown quits the systray loop exactly once; Run's tail does the rest.
func (a *app) shutdown() {
	if a.stopping.CompareAndSwap(false, true) {
		systray.Quit()
	}
}

// revealLog opens Explorer on the log file, creating an empty one first so
// the selection never targets a file that does not exist yet.
func (a *app) revealLog() {
	logPath, err := serverLogPath()
	if err != nil {
		return
	}

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		if err := os.MkdirAll(parentDir(logPath), 0o755); err != nil {
			return
		}

		// #nosec G304 -- fixed path from our own config directory.
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_ = f.Close()
		}
	}

	_ = revealInExplorer(logPath)
}

// parentDir is filepath.Dir without importing path/filepath into the hot
// path - the tray only ever builds one path shape.
func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '\\' || p[i] == '/' {
			return p[:i]
		}
	}

	return "."
}

// trayLogPath resolves <app dir>\logs\tray.log - the state-transition trail
// next to the service's own server.log.
func trayLogPath() (string, error) {
	appDir, err := config.AppDir()
	if err != nil {
		return "", err
	}

	return appDir + string(os.PathSeparator) + "logs" + string(os.PathSeparator) + "tray.log", nil
}
