// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package textdiff

import (
	"slices"
	"strings"
	"testing"
)

func TestWordDiff(t *testing.T) {
	for _, c := range []struct{ old, new, want string }{
		{"Nessus scans 30 hosts at once.", "Nessus scans 50 hosts at once.", "Nessus scans [-30-] {+50+} hosts at once."},
		{"Uses TCP 443.", "Uses TCP 443 and 8834.", "Uses TCP [-443.-] {+443 and 8834.+}"},
		// A long unchanged stretch keeps diffContext words on each side of the change.
		{
			"one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen old end",
			"one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen new end",
			"… eleven twelve thirteen fourteen fifteen sixteen [-old-] {+new+} end",
		},
		// A table row keeps its first cell, which names the row.
		{
			"| Severity Basis (severity_basis) | You can choose whether Tenable Nessus calculates the severity of vulnerabilities using CVSSv2 or CVSSv3 scores. |",
			"| Severity Basis (severity_basis) | You can choose whether Tenable Nessus calculates the severity of vulnerabilities using CVSSv3 or CVSSv4 scores. |",
			"| Severity Basis (severity_basis) | … calculates the severity of vulnerabilities using [-CVSSv2 or-] CVSSv3 {+or CVSSv4+} scores. |",
		},
	} {
		if got := WordDiff(c.old, c.new); got != c.want {
			t.Errorf("WordDiff:\n got %q\nwant %q", got, c.want)
		}
	}
}

// Pair shows a line that changed once, and leaves lines that only one side has alone.
func TestPair(t *testing.T) {
	c := Pair(Change{
		Removed: []string{"| TCP 443 | Communicating with Tenable One. |", "Removed for good."},
		Added: []string{"A brand new paragraph.",
			"| TCP 443 | Communicating with Tenable One, and plugin updates. |"},
	})
	if !slices.Equal(c.Changed, []string{"| TCP 443 | Communicating with Tenable [-One.-] {+One, and plugin updates.+} |"}) ||
		!slices.Equal(c.Added, []string{"A brand new paragraph."}) || !slices.Equal(c.Removed, []string{"Removed for good."}) {
		t.Errorf("Pair = %+v", c)
	}

	// Rows with the same first cell pair with each other, before a merely similar row.
	c = Pair(Change{
		Removed: []string{"| Max hosts | 30 | The most hosts a scanner scans at once. |"},
		Added: []string{"| Max checks | 30 | The most hosts a scanner scans at once. |",
			"| Max hosts | 50 | The most hosts a scanner scans at once. |"},
	})
	if len(c.Changed) != 1 || !strings.Contains(c.Changed[0], "[-30-] {+50+}") || !slices.Equal(c.Added, []string{"| Max checks | 30 | The most hosts a scanner scans at once. |"}) {
		t.Errorf("rows = %+v", c)
	}

	// Lines with little in common stay apart.
	c = Pair(Change{Removed: []string{"Install it on Linux."}, Added: []string{"Configure the proxy settings first."}})
	if len(c.Changed) != 0 || len(c.Added) != 1 || len(c.Removed) != 1 {
		t.Errorf("unlike = %+v", c)
	}
}

// A change far into a long line is shown, not cut off: lines are kept whole, up to
// MaxLineChars, before they're paired.
func TestLongLineChange(t *testing.T) {
	long := strings.Repeat("The scanner reads this setting when a scan starts. ", 12)
	before := []Section{{Heading: "Scanning", Body: "| Severity Basis | " + long + "Uses CVSSv2. |"}}
	after := []Section{{Heading: "Scanning", Body: "| Severity Basis | " + long + "Uses CVSSv3. |"}}
	changes, _ := Sections(before, after)
	if len(changes) != 1 {
		t.Fatalf("changes = %+v", changes)
	}
	c := Pair(changes[0])
	if len(c.Changed) != 1 || !strings.Contains(c.Changed[0], "[-CVSSv2.-] {+CVSSv3.+}") || !strings.HasPrefix(c.Changed[0], "| Severity Basis | …") {
		t.Errorf("changed = %q", c.Changed)
	}
}

func TestSectionsAll(t *testing.T) {
	var a, b []string
	for i := range MaxLines + 5 {
		a = append(a, strings.Repeat("old ", i+1))
		b = append(b, strings.Repeat("new ", i+1))
	}
	before := []Section{{Heading: "Ports", Body: strings.Join(a, "\n")}}
	after := []Section{{Heading: "Ports", Body: strings.Join(b, "\n")}}
	if c, _ := Sections(before, after); len(c[0].Added) != MaxLines || c[0].More != 10 {
		t.Errorf("Sections: %d added, %d more", len(c[0].Added), c[0].More)
	}
	if c, _ := SectionsAll(before, after); len(c[0].Added) != MaxLines+5 || c[0].More != 0 {
		t.Errorf("SectionsAll: %d added, %d more", len(c[0].Added), c[0].More)
	}
}
