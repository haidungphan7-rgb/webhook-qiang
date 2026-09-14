//go:build !windows

package tray

import "fmt"

// Run is the tray entry point; on this platform there is no tray to run and
// the answer is a plain refusal, not a half-working shim. The hidden `tray`
// subcommand still parses so scripts never hit "unknown command".
func Run(opts Options) error {
	_ = opts

	return fmt.Errorf("tray is not supported on this platform")
}
