// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package textdiff compares pages' sections: which were added, removed, or changed,
// and the lines that differ, for comparing versions of a page and recording what an
// update changed.
package textdiff

import (
	"regexp"
	"strings"
)

// Limits that keep a comparison short enough to read.
const (
	MaxLines     = 8
	MaxLineChars = 300
)

// Section is one section of a page, as compared.
type Section struct {
	Heading, URL string
	// Body is the section's Markdown.
	Body string
}

// Change is how a section differs.
type Change struct {
	Heading string `json:"heading"`
	// Status is changed, added (only in the later page), or removed (only in the
	// earlier one).
	Status  string   `json:"status"`
	FromURL string   `json:"from_url,omitempty"`
	ToURL   string   `json:"to_url,omitempty"`
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	// More counts changed lines left out of Added and Removed.
	More int `json:"more_lines,omitempty"`
}

// versionedLink matches a docs link's version folder, such as the 10_5 in
// docs.tenable.com/nessus/10_5/Content/x.htm.
var versionedLink = regexp.MustCompile(`(docs\.tenable\.com/[A-Za-z0-9_./-]*?)/\d+(?:_\d+)+/`)

// Comparable normalizes text for comparing: spacing, and links to the same page in
// another version, which every older page has.
func Comparable(s string) string {
	return versionedLink.ReplaceAllString(strings.Join(strings.Fields(s), " "), "$1/")
}

// Lines splits a section's Markdown into its non-empty lines, without its heading and
// normalized with Comparable, so reflowed text and version links compare equal.
func Lines(md string) []string {
	var out []string
	for _, l := range strings.Split(md, "\n") {
		l = Comparable(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// Sections matches two pages' sections by heading, in order, and returns the ones
// that differ, in the later page's order and then the removed ones, with how many are
// the same.
func Sections(before, after []Section) ([]Change, int) {
	key := func(s Section) string { return strings.ToLower(strings.Join(strings.Fields(s.Heading), " ")) }
	pending := map[string][]int{}
	for i, s := range before {
		pending[key(s)] = append(pending[key(s)], i)
	}
	matched := make([]bool, len(before))
	var changes []Change
	unchanged := 0
	for _, a := range after {
		k := key(a)
		if len(pending[k]) == 0 {
			c := Change{Heading: a.Heading, Status: "added", ToURL: a.URL}
			c.Added = Clip(Lines(a.Body), &c.More)
			changes = append(changes, c)
			continue
		}
		i := pending[k][0]
		pending[k] = pending[k][1:]
		matched[i] = true
		b := before[i]
		added, removed := Diff(Lines(b.Body), Lines(a.Body))
		if len(added) == 0 && len(removed) == 0 {
			unchanged++
			continue
		}
		c := Change{Heading: a.Heading, Status: "changed", FromURL: b.URL, ToURL: a.URL}
		c.Added, c.Removed = Clip(added, &c.More), Clip(removed, &c.More)
		changes = append(changes, c)
	}
	for i, b := range before {
		if !matched[i] {
			c := Change{Heading: b.Heading, Status: "removed", FromURL: b.URL}
			c.Removed = Clip(Lines(b.Body), &c.More)
			changes = append(changes, c)
		}
	}
	return changes, unchanged
}

// Diff returns the lines only in b (added) and only in a (removed), in order, from
// their longest common subsequence.
func Diff(a, b []string) (added, removed []string) {
	if len(a)*len(b) > 250000 {
		// Too long to align; compare as sets.
		inA, inB := map[string]bool{}, map[string]bool{}
		for _, l := range a {
			inA[l] = true
		}
		for _, l := range b {
			inB[l] = true
		}
		for _, l := range b {
			if !inA[l] {
				added = append(added, l)
			}
		}
		for _, l := range a {
			if !inB[l] {
				removed = append(removed, l)
			}
		}
		return added, removed
	}
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			removed = append(removed, a[i])
			i++
		default:
			added = append(added, b[j])
			j++
		}
	}
	removed = append(removed, a[i:]...)
	added = append(added, b[j:]...)
	return added, removed
}

// Clip keeps the first MaxLines lines, each shortened to MaxLineChars, and counts the
// rest in more.
func Clip(lines []string, more *int) []string {
	var out []string
	for i, l := range lines {
		if i == MaxLines {
			if more != nil {
				*more += len(lines) - i
			}
			break
		}
		if r := []rune(l); len(r) > MaxLineChars {
			l = string(r[:MaxLineChars]) + "…"
		}
		out = append(out, l)
	}
	return out
}
