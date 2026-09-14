//go:build !windows

package tray

// The tray is a Windows-only affordance (fyne.io/systray there needs no cgo).
// On other platforms the hidden `tray` subcommand still exists so scripts and
// docs never hit "unknown command", but it says no instead of half-working.

func probeInstance() bool { return false }

func signalEvent(string) error { return ErrNoTray }
