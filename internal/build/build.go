// Package build holds the version metadata that -ldflags sets at link time.
package build

import (
	"fmt"
	"runtime"
)

// The build sets these variables. See the Makefile for the -ldflags targets,
// such as github.com/nccloud/hyperlift-cli/internal/build.Version.
var (
	// Version is the released semantic version, or "dev" for a local build.
	Version = "dev"
	// Commit is the git commit the binary comes from.
	Commit = "none"
	// Date is the build timestamp, in RFC3339.
	Date = "unknown"
)

// UserAgent returns the User-Agent header value of every API call.
func UserAgent() string {
	return fmt.Sprintf("hyperlift-cli/%s (%s; %s)", Version, runtime.GOOS, runtime.GOARCH)
}
