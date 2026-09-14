//go:build !windows

package console

// Owned is always false elsewhere: no window can be "created just for this
// process" when the platform has no mechanism for it.
func Owned() bool { return false }
