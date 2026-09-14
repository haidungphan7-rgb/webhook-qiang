//go:build !windows

package winpath

// add/remove/contains are no-ops elsewhere: the non-Windows install target
// is ~/.local/bin, which the platform's own conventions put on PATH (and the
// installer tells the user when their distribution does not).
func add(string) (bool, error)    { return false, nil }
func remove(string) (bool, error) { return false, nil }
func contains(string) bool        { return false }
