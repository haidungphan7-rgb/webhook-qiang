// Package assets embeds the tray icons. They are two renderings of the same
// lightning bolt (same geometry as the web header logo): brand blue #0052d9
// while the service answers, grey while it does not. Keeping both in one
// place makes the "same shape, only the colour differs" contract visible.
package assets

import _ "embed"

//go:embed icon-running.ico
var running []byte

//go:embed icon-stopped.ico
var stopped []byte

// Running returns the blue icon used while the service is reachable.
func Running() []byte { return running }

// Stopped returns the grey icon used in every other state.
func Stopped() []byte { return stopped }
