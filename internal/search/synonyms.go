// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"bufio"
	_ "embed"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// The synonyms that ship with the server.
//
//go:embed synonyms.txt
var builtinSynonyms string

// maxAlternatives caps how many reworded searches one question adds.
const maxAlternatives = 6

// Synonyms are groups of terms that mean the same, such as a word customers use and the
// one the docs use.
type Synonyms struct {
	groups [][]string
	// match finds a term as whole words, by term.
	match map[string]*regexp.Regexp
}

// ParseSynonyms reads synonym groups: one group per line, its terms separated by
// commas. Blank lines and lines starting with # are skipped. Terms are matched without
// regard to case.
func ParseSynonyms(r io.Reader) (*Synonyms, error) {
	s := &Synonyms{match: map[string]*regexp.Regexp{}}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var group []string
		for _, t := range strings.Split(line, ",") {
			t = strings.Join(strings.Fields(strings.ToLower(t)), " ")
			if t != "" && !slices.Contains(group, t) {
				group = append(group, t)
			}
		}
		if len(group) < 2 {
			return nil, fmt.Errorf("line %d: a group needs at least two terms, separated by commas", n)
		}
		s.add(group)
	}
	return s, sc.Err()
}

func (s *Synonyms) add(group []string) {
	s.groups = append(s.groups, group)
	for _, t := range group {
		if s.match[t] == nil {
			s.match[t] = regexp.MustCompile(`(?i)(^|[^\pL\pN])(` + regexp.QuoteMeta(t) + `)($|[^\pL\pN])`)
		}
	}
}

// DefaultSynonyms returns the synonyms that ship with the server.
func DefaultSynonyms() *Synonyms {
	s, err := ParseSynonyms(strings.NewReader(builtinSynonyms))
	if err != nil {
		panic("synonyms.txt: " + err.Error())
	}
	return s
}

// LoadSynonyms returns the shipped synonyms with the groups in the file at path added.
// A missing file adds nothing.
func LoadSynonyms(path string) (*Synonyms, error) {
	s := DefaultSynonyms()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return s, nil
	} else if err != nil {
		return s, err
	}
	defer f.Close()
	user, err := ParseSynonyms(f)
	if err != nil {
		return s, fmt.Errorf("%s: %w", path, err)
	}
	for _, g := range user.groups {
		s.add(g)
	}
	return s, nil
}

// Of returns the terms that mean the same as term, in the order the groups list them.
func (s *Synonyms) Of(term string) []string {
	term = strings.Join(strings.Fields(strings.ToLower(term)), " ")
	var out []string
	for _, g := range s.groups {
		if !slices.Contains(g, term) {
			continue
		}
		for _, t := range g {
			if t != term && !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	return out
}

// Alternative is a question reworded with a synonym.
type Alternative struct {
	// Term is the term found in the question, and Synonym the one put in its place.
	Term, Synonym string
	// Query is the reworded question.
	Query string
}

// Alternatives returns the question reworded with each synonym of each term it
// contains, at most maxAlternatives of them.
func (s *Synonyms) Alternatives(query string) []Alternative {
	if s == nil {
		return nil
	}
	var out []Alternative
	seen := map[string]bool{strings.ToLower(query): true}
	for _, g := range s.groups {
		for _, term := range g {
			loc := s.match[term].FindStringSubmatchIndex(query)
			if loc == nil {
				continue
			}
			for _, syn := range g {
				if syn == term {
					continue
				}
				q := query[:loc[4]] + syn + query[loc[5]:]
				if seen[strings.ToLower(q)] {
					continue
				}
				seen[strings.ToLower(q)] = true
				out = append(out, Alternative{Term: term, Synonym: syn, Query: q})
				if len(out) == maxAlternatives {
					return out
				}
			}
		}
	}
	return out
}
