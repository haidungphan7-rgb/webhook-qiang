// Package console answers one question every interactive command eventually
// needs: "was this console created just for me?" - which is the difference
// between a window the user is watching (never pause it) and a window Windows
// spawned for a double-clicked entry (pause it, or the user never reads the
// output). Non-Windows platforms have no such thing; the stub keeps call
// sites unconditional.
package console

import (
	"bufio"
	"fmt"
	"os"
)

// PauseIfOwned blocks for Enter before returning, but only when the current
// process solely owns its console - the signature of a window Windows opened
// for this process alone (double-click, "Apps & features", tray-spawned
// doctor). A user terminal shares its console (no pause) and piped runs have
// no console at all (no pause), so scripts are never held up.
func PauseIfOwned(prompt string) {
	if !Owned() {
		return
	}

	fmt.Println("")
	fmt.Print(prompt)

	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
