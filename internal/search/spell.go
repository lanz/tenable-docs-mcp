// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
)

// suggest offers the question with its unknown words replaced by the closest words in
// the index's vocabulary. It works for languages written with spaces between words.
func (s *Searcher) suggest(ctx context.Context, terms []string) []string {
	s.vocabOnce.Do(func() { s.vocab, s.vocabErr = s.Shard.Vocabulary(ctx) })
	if s.vocabErr != nil || len(s.vocab) == 0 {
		return nil
	}
	changed := false
	out := make([]string, len(terms))
	for i, t := range terms {
		out[i] = t
		if ctx.Err() != nil {
			continue // the caller has given up; leave the rest as they are
		}
		if s.vocab[t] > 0 || utf8.RuneCountInString(t) < 4 || !latin(t) {
			continue
		}
		if w := closest(t, s.vocab); w != "" {
			out[i], changed = w, true
		}
	}
	if !changed {
		return nil
	}
	return []string{strings.Join(out, " ")}
}

func latin(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && !unicode.In(r, unicode.Latin) {
			return false
		}
	}
	return true
}

// closest returns the vocabulary word nearest to w: the fewest edits (at most two, or
// one for short words), then the most frequent.
func closest(w string, vocab map[string]int) string {
	wr := []rune(w)
	maxDist := 2
	if len(wr) < 6 {
		maxDist = 1
	}
	best, bestDist, bestFreq := "", maxDist+1, 0
	for v, freq := range vocab {
		vr := []rune(v)
		if abs(len(vr)-len(wr)) > maxDist {
			continue
		}
		d := editDistance(wr, vr, bestDist)
		if d < bestDist || d == bestDist && (freq > bestFreq || freq == bestFreq && v < best) {
			best, bestDist, bestFreq = v, d, freq
		}
	}
	if bestDist > maxDist {
		return ""
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// editDistance counts the insertions, deletions, substitutions, and swaps of adjacent
// letters that turn a into b. It stops early, returning limit+1, once the distance is
// sure to be more than limit.
func editDistance(a, b []rune, limit int) int {
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
			rowMin = min(rowMin, cur[j])
		}
		if rowMin > limit {
			return limit + 1
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}
