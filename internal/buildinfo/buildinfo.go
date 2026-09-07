// Package buildinfo exposes release metadata so logs, /version and the
// jigsaw_build_info metric all report the same thing.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Version and Commit are overridable at link time:
//
//	go build -ldflags "-X github.com/reijiokito/jigsaw-backend/internal/buildinfo.Version=1.4.0"
//
// When not set, Commit falls back to the VCS revision Go stamps into the binary.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func init() {
	if Commit != "" {
		return
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			Commit = s.Value
		case "vcs.time":
			if Date == "" {
				Date = s.Value
			}
		}
	}
}

// GoVersion returns the Go toolchain version the binary was built with.
func GoVersion() string { return runtime.Version() }
