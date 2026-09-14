//go:build windows

package console

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32DLL               = windows.NewLazySystemDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32DLL.NewProc("GetConsoleProcessList")
)

// Owned reports whether the current process solely owns its console - the
// "window created just for me" case. x/sys/windows does not wrap
// GetConsoleProcessList, hence the lazy syscall.
func Owned() bool {
	return consoleProcessCount() == 1
}

// consoleProcessCount reports how many processes share the current console.
// 0 means the process has no console at all (detached, or pipes only).
func consoleProcessCount() uint32 {
	// #nosec G103 -- the pointer is the documented calling convention of the API.
	n, _, _ := procGetConsoleProcessList.Call(2, uintptr(unsafe.Pointer(&[2]uint32{})))
	return uint32(n)
}
