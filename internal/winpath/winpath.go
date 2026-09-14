// Package winpath owns the user's PATH environment variable for install and
// uninstall. Windows has no supported CLI for this (setx truncates at 1024
// characters and must not be used), so the registry is the interface: the
// value under HKCU\Environment is read raw, one entry is added or removed,
// and a WM_SETTINGCHANGE broadcast tells running shells a new value exists
// without a logoff.
package winpath

// Add appends dir to the user PATH. Idempotent: an entry already present is
// left untouched and reported as not-added. The boolean says whether the
// value changed; the error says whether it could not.
func Add(dir string) (bool, error) { return add(dir) }

// Remove drops dir from the user PATH. Idempotent in the same sense.
func Remove(dir string) (bool, error) { return remove(dir) }

// Contains reports whether the user PATH lists dir.
func Contains(dir string) bool { return contains(dir) }
