// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ReleasesURL is where releases are published.
const ReleasesURL = "https://github.com/lanz/tenable-docs-mcp/releases"

// semver matches vMAJOR.MINOR.PATCH, with an optional prerelease and build suffix.
var semver = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)

// pseudo matches the timestamp and commit of a Go pseudo-version
// (v0.0.0-20261109043837-e7da37b02d0d), which a build from a commit reports.
var pseudo = regexp.MustCompile(`(^|\.)\d{14}-[0-9a-f]{12}$`)

type parsed struct {
	n   [3]int
	pre string
}

// parse reads a release or prerelease version. Development builds ("dev", Go
// pseudo-versions, and versions with build metadata, such as "+dirty") don't parse.
func parse(v string) (parsed, bool) {
	m := semver.FindStringSubmatch(v)
	if m == nil || m[5] != "" || pseudo.MatchString(m[4]) {
		return parsed{}, false
	}
	var p parsed
	for i := range p.n {
		p.n[i], _ = strconv.Atoi(m[i+1])
	}
	p.pre = m[4]
	return p, true
}

// IsRelease reports whether v is a release: vMAJOR.MINOR.PATCH, with no prerelease
// or build suffix.
func IsRelease(v string) bool {
	p, ok := parse(v)
	return ok && p.pre == ""
}

// compare orders two parsed versions by semantic versioning: a prerelease comes
// before its release.
func compare(a, b parsed) int {
	for i := range a.n {
		if a.n[i] != b.n[i] {
			if a.n[i] < b.n[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.pre == b.pre:
		return 0
	case a.pre == "":
		return 1
	case b.pre == "":
		return -1
	}
	return comparePre(a.pre, b.pre)
}

// comparePre orders prerelease suffixes field by field: numbers numerically and
// before words, words alphabetically, and a shorter list first.
func comparePre(a, b string) int {
	af, bf := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(af) && i < len(bf); i++ {
		an, aErr := strconv.Atoi(af[i])
		bn, bErr := strconv.Atoi(bf[i])
		switch {
		case aErr == nil && bErr == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aErr == nil:
			return -1
		case bErr == nil:
			return 1
		default:
			if c := strings.Compare(af[i], bf[i]); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(af) < len(bf):
		return -1
	case len(af) > len(bf):
		return 1
	}
	return 0
}

// Update says that a newer release is available.
type Update struct {
	Current string `json:"current" jsonschema:"the version running"`
	Latest  string `json:"latest" jsonschema:"the newest release"`
	URL     string `json:"url" jsonschema:"the release's page, with its downloads"`
}

// Available reports whether latest is a release newer than current. current can be a
// release or a prerelease (v1.0.0-rc.1 is older than v1.0.0); a development build
// never reports an update, and neither does an empty or malformed latest.
func Available(latest, current string) (Update, bool) {
	if !IsRelease(latest) {
		return Update{}, false
	}
	cur, ok := parse(current)
	if !ok {
		return Update{}, false
	}
	l, _ := parse(latest)
	if compare(l, cur) <= 0 {
		return Update{}, false
	}
	return Update{Current: current, Latest: latest, URL: ReleasesURL + "/tag/" + latest}, true
}

// Message tells a user about the update, and how to install it whichever way they
// installed this one.
func (u Update) Message() string {
	return fmt.Sprintf("Tenable Docs MCP %s is available; this is %s. Get it from %s: "+
		"open the new .mcpb file to update the Claude Desktop extension, or download the archive for your platform. "+
		"Installed with go install, run `go install github.com/lanz/tenable-docs-mcp/cmd/tenable-docs-mcp@latest`; "+
		"with the container image, run `docker pull ghcr.io/lanz/tenable-docs-mcp`.", u.Latest, u.Current, u.URL)
}
