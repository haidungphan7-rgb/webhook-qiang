// Package version is used as a place, where application version defined.
package version

import "strings"

// version and buildTime are stamped in at link time (see Makefile / Dockerfile /
// scripts/setup.ps1). The defaults are what a plain `go build` or `go run` produces.
var (
	version   = "v0.0.0@undefined"
	buildTime = "unknown"
)

// Version returns version value (without `v` prefix).
func Version() string {
	v := strings.TrimSpace(version)

	if len(v) > 1 && ((v[0] == 'v' || v[0] == 'V') && (v[1] >= '0' && v[1] <= '9')) {
		return v[1:]
	}

	return v
}

// BuildTime returns a UTC timestamp, or "unknown" when the binary was built without
// the ldflags that stamp it.
func BuildTime() string {
	return strings.TrimSpace(buildTime)
}
