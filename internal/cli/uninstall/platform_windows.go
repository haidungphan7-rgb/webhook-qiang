//go:build windows

package uninstallcmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/yuandzhang/webhook-zq/internal/config"
)

// appListKey mirrors the constant in internal/cli/install - the two packages
// are deliberately independent so either can evolve without dragging the
// other along.
const appListKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\webhook-zq`

// proc is one running process of ours.
type proc struct {
	pid  uint32
	path string
}

// platformItems collects the Windows-specific traces: processes, service,
// autostart task and the "Apps & features" entry. Only what actually exists
// is reported, so a partial install still uninstalls cleanly.
func platformItems() []item {
	var items []item

	// First: the tray/manager windows. tray.ps1 restarts the server within
	// 500ms of it disappearing, so every removal below only sticks if these
	// are gone first - an uninstaller that skips them reports success while
	// the service comes right back.
	if trays := findTrayWindows(); len(trays) > 0 {
		items = append(items, item{
			label:  "托盘 / 管理窗口",
			detail: fmt.Sprintf("%d 个 PowerShell 进程（会在后台拉活服务）", len(trays)),
			remove: func() error { return killProcesses(trays) },
		})
	}

	if procs := findOurProcesses(); len(procs) > 0 {
		items = append(items, item{
			label:  "正在运行的进程",
			detail: fmt.Sprintf("%d 个 webhook-zq.exe", len(procs)),
			remove: func() error { return killProcesses(procs) },
		})
	}

	if serviceExists() {
		items = append(items, item{
			label:  "Windows 服务",
			detail: "webhook-zq",
			remove: removeService,
		})
	}

	if autostartExists() {
		items = append(items, item{
			label:  "开机自启任务",
			detail: "webhook-zq（登录时运行）",
			remove: removeAutostart,
		})
	}

	if appListEntryExists() {
		items = append(items, item{
			label:  `"应用和功能"条目`,
			detail: "webhook-zq",
			remove: func() error { return registry.DeleteKey(registry.CURRENT_USER, appListKey) },
		})
	}

	return items
}

// scanFiles reports the per-user application directory (binary, config with
// the encryption key) when it exists.
func scanFiles() []item {
	appDir, err := config.AppDir()
	if err != nil {
		return nil
	}

	if _, err := os.Stat(appDir); os.IsNotExist(err) {
		return nil
	}

	return []item{{
		label:  "程序文件",
		detail: appDir + "（含 config.json 配置与加密密钥）",
		remove: func() error { return removeAppDir(appDir) },
	}}
}

// findTrayWindows lists the PowerShell processes running our desktop scripts
// (tray, server manager, launcher GUI). The command line is what identifies
// them - the process name alone is just "powershell" - and pulling command
// lines out of Win32_Process via PowerShell itself is the one approach that
// works on every Windows build (wmic is gone from current ones). This is also
// the one place the uninstaller may kill processes it did not install: the
// scripts are recognized by command line, so a failure to enumerate simply
// means the item is not offered, never that a wrong process dies.
func findTrayWindows() []proc {
	// #nosec G204 -- fixed query.
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"Get-CimInstance Win32_Process -Filter \"Name='powershell.exe' OR Name='pwsh.exe'\" | "+
			"Select-Object ProcessId,CommandLine | ConvertTo-Json -Compress").Output()
	if err != nil {
		return nil
	}

	var procs []proc

	for _, row := range parsePSProcessList(out) {
		if row.CommandLine == nil || !isTrayCommandLine(*row.CommandLine) {
			continue
		}

		procs = append(procs, proc{pid: row.ProcessID, path: *row.CommandLine})
	}

	return procs
}

// findOurProcesses lists running webhook-zq.exe processes whose image lives
// inside the installed application directory. Process enumeration uses the
// native API rather than parsing `tasklist` or `wmic` output: wmic is being
// removed from current Windows builds, and the API gives the full image path
// for free, which is what makes the "only kill our own processes" check
// reliable.
func findOurProcesses() []proc {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}

	defer func() { _ = windows.CloseHandle(snapshot) }()

	appDir, appDirErr := config.AppDir()

	var ourDir string
	if self, selfErr := os.Executable(); selfErr == nil {
		ourDir = filepath.Dir(self)
	}

	var procs []proc

	var pe windows.ProcessEntry32

	pe.Size = uint32(unsafe.Sizeof(pe)) // #nosec G103 -- the fixed size of the struct, the documented way to initialize it.

	if err := windows.Process32First(snapshot, &pe); err != nil {
		return nil
	}

	for {
		if strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), "webhook-zq.exe") {
			if p, ok := ourProcess(pe.ProcessID, appDir, appDirErr, ourDir); ok {
				procs = append(procs, p)
			}
		}

		if err := windows.Process32Next(snapshot, &pe); err != nil {
			break
		}
	}

	return procs
}

// ourProcess resolves the image path of one pid and decides whether it is
// ours: the path must live inside the installed app directory (or, if that
// directory cannot be resolved, next to the running uninstaller). A process
// we cannot prove to be ours is never killed.
func ourProcess(pid uint32, appDir string, appDirErr error, ourDir string) (proc, bool) {
	// Never the uninstaller itself. The uninstaller is a webhook-zq.exe that
	// routinely runs from inside the very directories it is sweeping - the
	// "Apps & features" entry points UninstallString at the installed binary,
	// and the downloaded copy matches the ourDir fallback. Without this check
	// the removal list contains the process doing the removing, the taskkill
	// kills it mid-run, and every step after it (registry, files, task) never
	// executes while the user watches an uninstall that appears to finish.
	if pid == uint32(os.Getpid()) {
		return proc{}, false
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return proc{}, false
	}

	defer func() { _ = windows.CloseHandle(handle) }()

	var buf [windows.MAX_PATH]uint16

	size := uint32(len(buf))

	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return proc{}, false
	}

	path := windows.UTF16ToString(buf[:size])

	if appDirErr == nil && underDir(path, appDir) {
		return proc{pid: pid, path: path}, true
	}

	if ourDir != "" && underDir(path, ourDir) {
		return proc{pid: pid, path: path}, true
	}

	return proc{}, false
}

// underDir is a case-insensitive "path is inside dir" check for Windows paths.
func underDir(path, dir string) bool {
	p := strings.ToLower(filepath.Clean(path))
	d := strings.ToLower(filepath.Clean(dir))

	return p == d || strings.HasPrefix(p, d+string(os.PathSeparator))
}

func killProcesses(procs []proc) error {
	for _, p := range procs {
		// #nosec G204 -- fixed command; the pid comes from the verified process list above.
		if out, err := exec.Command("taskkill", "/PID",
			fmt.Sprintf("%d", p.pid), "/F").CombinedOutput(); err != nil {
			return fmt.Errorf("cannot stop pid %d (%s): %w: %s", p.pid, p.path, err, out)
		}
	}

	return nil
}

func serviceExists() bool {
	// #nosec G204 -- fixed command with a fixed service name.
	return exec.Command("sc", "query", "webhook-zq").Run() == nil
}

func removeService() error {
	// The stop is asynchronous: poll until the service reports a stopped-ish
	// state so the file removals that follow cannot race a live process.
	// #nosec G204 -- fixed command with a fixed service name.
	_ = exec.Command("sc", "stop", "webhook-zq").Run()

	waitServiceStopped("webhook-zq", 15*time.Second)

	// #nosec G204 -- fixed command with a fixed service name.
	out, err := exec.Command("sc", "delete", "webhook-zq").CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot delete the service: %w: %s", err, out)
	}

	return nil
}

func waitServiceStopped(name string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		// #nosec G204 -- fixed command with a fixed service name.
		out, err := exec.Command("sc", "query", name).Output()
		if err != nil {
			return // gone already
		}

		s := string(out)
		if !strings.Contains(s, "RUNNING") && !strings.Contains(s, "STOP_PENDING") {
			return
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func autostartExists() bool {
	// #nosec G204 -- fixed command with a fixed task name.
	return exec.Command("schtasks", "/Query", "/TN", "webhook-zq").Run() == nil
}

func removeAutostart() error {
	// #nosec G204 -- fixed command with a fixed task name.
	out, err := exec.Command("schtasks", "/Delete", "/TN", "webhook-zq", "/F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot delete the logon task: %w: %s", err, out)
	}

	return nil
}

func appListEntryExists() bool {
	_, err := registry.OpenKey(registry.CURRENT_USER, appListKey, registry.QUERY_VALUE)

	return err == nil
}

var (
	kernel32DLL               = windows.NewLazySystemDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32DLL.NewProc("GetConsoleProcessList")
)

// consoleProcessCount reports how many processes share the current console.
// x/sys/windows does not wrap GetConsoleProcessList, hence the lazy syscall.
// 0 means the process has no console at all (detached, or pipes only).
func consoleProcessCount() uint32 {
	// #nosec G103 -- the pointer is the documented calling convention of the API.
	n, _, _ := procGetConsoleProcessList.Call(2, uintptr(unsafe.Pointer(&[2]uint32{})))
	return uint32(n)
}

// maybePause keeps the uninstaller's console open when Windows created it just
// for this process - which is what happens when the entry is launched from
// "设置 → 应用 → 安装的应用". Without a pause the window vanishes with the
// summary unread. The user's own terminal (the shell shares the console, so
// the process list has two entries) and piped runs (no console) close as
// before, and --yes never pauses: scripts own the console's lifetime.
func maybePause(yes bool) {
	if yes || consoleProcessCount() != 1 {
		return
	}

	fmt.Println("")
	fmt.Print("按回车键关闭窗口…")

	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

// removeAppDir deletes the application directory. When the uninstaller itself
// runs from that directory, Windows locks the image; renaming a running
// process's file is allowed, so the exe is moved to the temp directory first
// (where it lands after exit and is eventually swept by the OS's temp
// cleanup) and the directory can be removed in full.
func removeAppDir(appDir string) error {
	if self, err := os.Executable(); err == nil && underDir(self, appDir) {
		moved := filepath.Join(os.TempDir(), "webhook-zq-uninstalled.exe")
		if err := os.Rename(self, moved); err != nil {
			return fmt.Errorf("cannot move the running binary out of %s: %w", appDir, err)
		}

		defer func() { _ = os.Remove(moved) }() // usually still locked; harmless.
	}

	if err := os.RemoveAll(appDir); err != nil {
		return fmt.Errorf("cannot remove %s: %w", appDir, err)
	}

	return nil
}
