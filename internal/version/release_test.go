// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package version

import (
	"strings"
	"testing"
)

func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"v1.0.0": true, "v1.12.3": true, "v0.9.9": true,
		"v1.0.0-rc.1": false, "v1.0.0+dirty": false, "1.0.0": false, "v1.0": false, "v01.0.0": false,
		"v0.0.0-20261109043837-e7da37b02d0d": false, "v1.0.1-0.20261109043837-e7da37b02d0d": false,
		"dev": false, "": false,
	} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q) = %v", v, got)
		}
	}
}

func TestAvailable(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"v1.1.0", "v1.0.0", true},
		{"v1.0.10", "v1.0.9", true},
		{"v2.0.0", "v1.9.9", true},
		{"v1.0.0", "v1.0.0-rc.1", true},
		{"v1.0.0", "v1.0.0", false},
		{"v1.0.0", "v1.1.0", false},
		{"v1.0.0", "v1.1.0-rc.1", false},
		// Prereleases aren't advertised, and development builds don't report updates.
		{"v1.1.0-rc.1", "v1.0.0", false},
		{"v1.1.0", "dev", false},
		{"v1.1.0", "v0.0.0-20261109043837-e7da37b02d0d", false},
		{"v1.1.0", "v1.0.0+dirty", false},
		{"", "v1.0.0", false},
		{"latest", "v1.0.0", false},
	} {
		u, ok := Available(c.latest, c.current)
		if ok != c.want {
			t.Errorf("Available(%q, %q) = %v", c.latest, c.current, ok)
		}
		if ok && (u.Latest != c.latest || u.Current != c.current || u.URL != ReleasesURL+"/tag/"+c.latest) {
			t.Errorf("Available(%q, %q) = %+v", c.latest, c.current, u)
		}
	}
}

func TestComparePrereleases(t *testing.T) {
	order := []string{"v1.0.0-alpha", "v1.0.0-alpha.1", "v1.0.0-beta", "v1.0.0-rc.1", "v1.0.0-rc.2", "v1.0.0-rc.10", "v1.0.0"}
	for i := 1; i < len(order); i++ {
		a, _ := parse(order[i-1])
		b, _ := parse(order[i])
		if compare(a, b) >= 0 || compare(b, a) <= 0 {
			t.Errorf("%s should come before %s", order[i-1], order[i])
		}
	}
}

func TestUpdateMessage(t *testing.T) {
	u, _ := Available("v1.1.0", "v1.0.0")
	m := u.Message()
	for _, want := range []string{"v1.1.0", "v1.0.0", u.URL, ".mcpb", "go install", "docker pull"} {
		if !strings.Contains(m, want) {
			t.Errorf("message lacks %q: %s", want, m)
		}
	}
}
