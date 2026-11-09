// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package textdiff

import (
	"slices"
	"strings"
	"testing"
)

func TestDiff(t *testing.T) {
	a := []string{"one", "two", "three", "four"}
	b := []string{"one", "2", "three", "four", "five"}
	added, removed := Diff(a, b)
	if !slices.Equal(added, []string{"2", "five"}) || !slices.Equal(removed, []string{"two"}) {
		t.Errorf("added %q, removed %q", added, removed)
	}
	if added, removed := Diff(a, a); len(added)+len(removed) != 0 {
		t.Errorf("same: %q %q", added, removed)
	}
}

func TestComparable(t *testing.T) {
	for in, want := range map[string]string{
		"See  [this](https://docs.tenable.com/nessus/10_5/Content/X.htm).":          "See [this](https://docs.tenable.com/nessus/Content/X.htm).",
		"https://docs.tenable.com/security-center/6_9_1/Content/X.htm":              "https://docs.tenable.com/security-center/Content/X.htm",
		"https://docs.tenable.com/nessus/Content/X.htm":                             "https://docs.tenable.com/nessus/Content/X.htm",
		"Version 10_5 of the text, outside a link":                                  "Version 10_5 of the text, outside a link",
		"https://docs.tenable.com/identity-exposure/on-premises/3_77/Content/X.htm": "https://docs.tenable.com/identity-exposure/on-premises/Content/X.htm",
	} {
		if got := Comparable(in); got != want {
			t.Errorf("Comparable(%q) = %q", in, got)
		}
	}
}

func TestClip(t *testing.T) {
	var lines []string
	for range MaxLines + 3 {
		lines = append(lines, strings.Repeat("x", MaxLineChars+10))
	}
	more := 0
	got := Clip(lines, &more)
	if len(got) != MaxLines || more != 3 || !strings.HasSuffix(got[0], "…") {
		t.Errorf("%d lines, %d more", len(got), more)
	}
}
