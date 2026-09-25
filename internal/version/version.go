// Package version reports the version of the running gospy binary.
package version

import "runtime/debug"

// version is set at link time with
// -X github.com/hakastein/gospy/internal/version.version=<version>.
var version = "dev"

// Get returns the version of the running binary. Release builds carry it in the
// linker flag; a binary produced by `go install ...@vX.Y.Z` has no such flag, so
// the version the toolchain recorded in the build info is used instead — the
// module version there, or the commit pseudo-version for a build from a working
// copy. Only a build with no recorded version at all falls back to "dev".
func Get() string {
	if version != "dev" {
		return version
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}

	return version
}
