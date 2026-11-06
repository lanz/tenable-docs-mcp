// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-documentation-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package version reports the build's version.
package version

import "runtime/debug"

// Version is set at build time for release binaries:
//
//	go build -ldflags "-X github.com/lanz/tenable-documentation-mcp/internal/version.Version=v1.2.3"
//
// Without it, Get falls back to the module version recorded by `go install …@vX.Y.Z`.
var Version = ""

// Get returns the version, or "dev" for a local build.
func Get() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
