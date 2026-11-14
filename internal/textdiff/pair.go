// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package textdiff

import "strings"

// A changed line is a paragraph or table row in which a few words differ. Listing it
// once as removed and once as added makes the reader find the difference in two long,
// nearly equal lines. Pair shows it once instead, with only what changed marked, in
// git's word-diff notation: [-removed words-] and {+added words+}.

const (
	// minSimilarity is how alike a removed and an added line must be to be shown as
	// one changed line: the share of their words they have in common.
	minSimilarity = 0.5
	// diffContext is how many unchanged words are kept on each side of a change; the
	// rest of an unchanged stretch is shown as "…".
	diffContext = 6
)

// Pair matches each removed line with the added line it most likely became, and
// shows each pair once, as a word diff, in Changed. Lines that don't pair stay in
// Added and Removed.
func Pair(c Change) Change {
	if len(c.Added) == 0 || len(c.Removed) == 0 {
		return c
	}
	added := make([][]string, len(c.Added))
	for i, a := range c.Added {
		added[i] = strings.Fields(a)
	}
	used := make([]bool, len(c.Added))
	var removed, changed []string
	for _, r := range c.Removed {
		rw := strings.Fields(r)
		best, bestScore := -1, 0.0
		for j, aw := range added {
			if used[j] {
				continue
			}
			if s := similarity(rw, aw); s > bestScore {
				best, bestScore = j, s
			}
		}
		if best < 0 || bestScore < minSimilarity {
			removed = append(removed, r)
			continue
		}
		used[best] = true
		changed = append(changed, WordDiff(r, c.Added[best]))
	}
	var rest []string
	for j, a := range c.Added {
		if !used[j] {
			rest = append(rest, a)
		}
	}
	c.Added, c.Removed, c.Changed = rest, removed, changed
	return c
}

// similarity is the share of two lines' words they have in common (their Dice
// coefficient, counting repeated words), plus 1 for table rows with the same first
// cell, which name the row, so a row pairs with itself before a merely similar one.
func similarity(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	count := map[string]int{}
	for _, w := range a {
		count[w]++
	}
	common := 0
	for _, w := range b {
		if count[w] > 0 {
			count[w]--
			common++
		}
	}
	s := 2 * float64(common) / float64(len(a)+len(b))
	if ca, cb := firstCell(a), firstCell(b); ca > 0 && ca == cb && strings.Join(a[:ca], " ") == strings.Join(b[:cb], " ") {
		s++
	}
	return s
}

// firstCell is how many of a table row's words are its first cell, with the pipes
// around it, or 0 for a line that isn't a table row.
func firstCell(words []string) int {
	if len(words) == 0 || words[0] != "|" {
		return 0
	}
	for i := 1; i < len(words); i++ {
		if words[i] == "|" {
			return i + 1
		}
	}
	return 0
}

// WordDiff shows how a line changed: the new line, with [-removed words-] and
// {+added words+} where it differs from the old one. Unchanged stretches longer than
// diffContext words on each side of a change are shortened to "…", except that a
// table row keeps its first cell, so the row can still be told apart.
func WordDiff(old, new string) string {
	a, b := strings.Fields(old), strings.Fields(new)
	runs := wordRuns(a, b)
	head := firstCell(b)
	var parts []string
	for i, r := range runs {
		words := r.words
		switch r.op {
		case '-':
			parts = append(parts, "[-"+strings.Join(words, " ")+"-]")
			continue
		case '+':
			parts = append(parts, "{+"+strings.Join(words, " ")+"+}")
			continue
		}
		first, last := i == 0, i == len(runs)-1
		switch {
		case first && last:
			// Nothing changed in the words themselves.
		case first:
			keep := min(head, len(words))
			if len(words) > keep+diffContext+1 {
				prefix := words[:keep:keep]
				words = append(append(prefix, "…"), words[len(words)-diffContext:]...)
			}
		case last:
			if len(words) > diffContext+1 {
				words = append(words[:diffContext:diffContext], "…")
			}
		default:
			if len(words) > 2*diffContext+1 {
				words = append(append(words[:diffContext:diffContext], "…"), words[len(words)-diffContext:]...)
			}
		}
		parts = append(parts, strings.Join(words, " "))
	}
	return strings.Join(parts, " ")
}

// run is a stretch of words that are the same ('='), only in the old line ('-'), or
// only in the new one ('+').
type run struct {
	op    byte
	words []string
}

// wordRuns aligns two lines' words by their longest common subsequence.
func wordRuns(a, b []string) []run {
	var runs []run
	add := func(op byte, w string) {
		if n := len(runs); n > 0 && runs[n-1].op == op {
			runs[n-1].words = append(runs[n-1].words, w)
			return
		}
		runs = append(runs, run{op, []string{w}})
	}
	n, m := len(a), len(b)
	if n*m > 250000 {
		// Too long to align (lines are at most MaxLineChars): show the whole change.
		for _, w := range a {
			add('-', w)
		}
		for _, w := range b {
			add('+', w)
		}
		return runs
	}
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
			add('=', a[i])
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			add('-', a[i])
			i++
		default:
			add('+', b[j])
			j++
		}
	}
	for ; i < n; i++ {
		add('-', a[i])
	}
	for ; j < m; j++ {
		add('+', b[j])
	}
	return runs
}
