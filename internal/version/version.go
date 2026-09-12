// Package version holds build metadata injected via -ldflags.
package version

// Version and Commit are set at build time:
//
//	-ldflags "-X github.com/parsoFish/healarr/internal/version.Version=v0.1.0 -X ...Commit=abc123"
var (
	Version = "dev"
	Commit  = "unknown"
)
