// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"regexp"
	"strings"
)

// Intent is what kind of answer a question is after.
type Intent string

// Intents.
const (
	General      Intent = ""
	HowTo        Intent = "how-to"
	WhatsNew     Intent = "whats-new"
	Requirements Intent = "requirements"
)

// Parsed is a question broken down for searching.
type Parsed struct {
	// Terms are the words to search for, without filler words or product names.
	Terms []string
	// Phrases are the quoted phrases, each searched as written.
	Phrases []string
	// Products are the products the question names, in order.
	Products []string
	Intent   Intent
	// Install is set for questions about installing, which also favor requirements.
	Install bool
}

// Filler words dropped from English questions. Words that can matter in the docs
// ("set", "use", "new", "not") are kept.
var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an the and or of to in on for with by from at as is are was were be been
		being do does did doing how what which who whom whose why when where can could should would will shall may
		might must i me my mine we us our you your yours it its this that these those there here their they them he
		she his her him if then than so such into about over via vs versus please want wants need needs help tell
		show explain way ways also any all each just only very really much many some something anything thing things
		way get gets got getting use uses using up tenable docs documentation doc guide page find know let lets i'm i've it's what's
		how's there's don't doesn't can't`) {
		m[w] = true
	}
	return m
}()

var (
	whatsNewRe = regexp.MustCompile(`(?i)\b(what'?s new|what is new|release notes?|new features?|change ?logs?|` +
		`latest (release|version)|recent (changes|updates)|fixed in|bug ?fix(es)?|known issues|newly added|released)\b`)
	requirementsRe = regexp.MustCompile(`(?i)\b(ports?|firewall|requirements?|hardware|cpus?|ram|memory|disk space|` +
		`storage|sizing|prerequisites?|compatib\w*|supported (os|operating systems?|platforms?|browsers?|versions?|databases?)|` +
		`which (os|operating systems?|browsers?|platforms?)|minimum|recommended specs?|licens\w* requirements?)\b`)
	installRe = regexp.MustCompile(`(?i)\b(install\w*|deploy\w*|set ?up|upgrade\w*)\b`)
	howToRe   = regexp.MustCompile(`(?i)(^\s*(how|steps?|configure|set ?up|enable|disable|create|add|delete|remove|edit|` +
		`change|install|deploy|upgrade|migrate|connect|integrate|import|export|run|launch|schedule|view|assign|reset|` +
		`generate|link|unlink|restart|start|stop|update|back ?up|restore)\b)|\bhow (do|can|to|should|would)\b|\bsteps? (to|for)\b`)
	quoted = regexp.MustCompile(`"([^"]+)"|“([^”]+)”`)
)

// compounds are words that questions split and the docs write as one.
var compounds = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?i)\bback[- ]up\b`), "backup"},
	{regexp.MustCompile(`(?i)\blog[- ]in\b`), "login"},
	{regexp.MustCompile(`(?i)\bplug[- ]in(s?)\b`), "plugin$1"},
	{regexp.MustCompile(`(?i)\bwork[- ]flow(s?)\b`), "workflow$1"},
}

// Parse breaks a question down for searching.
func Parse(question string) Parsed {
	var p Parsed
	rest := question
	for _, m := range quoted.FindAllStringSubmatch(question, -1) {
		phrase := strings.TrimSpace(m[1] + m[2])
		if phrase != "" {
			p.Phrases = append(p.Phrases, phrase)
		}
		rest = strings.Replace(rest, m[0], " ", 1)
	}
	for _, c := range compounds {
		rest = c.re.ReplaceAllString(rest, c.with)
	}
	toks := tokens(rest)
	inProduct := make([]bool, len(toks))
	for _, m := range findProducts(toks) {
		p.Products = appendUnique(p.Products, m.product)
		for i := m.start; i < m.end; i++ {
			inProduct[i] = true
		}
	}
	for _, ph := range p.Phrases {
		if prod, ok := ProductNamed(ph); ok {
			p.Products = appendUnique(p.Products, prod)
		}
	}
	for i, t := range toks {
		if inProduct[i] || stopwords[t.low] {
			continue
		}
		p.Terms = appendUnique(p.Terms, t.low)
	}
	switch {
	case whatsNewRe.MatchString(question):
		p.Intent = WhatsNew
	case requirementsRe.MatchString(question):
		p.Intent = Requirements
	case howToRe.MatchString(question):
		p.Intent = HowTo
	}
	p.Install = installRe.MatchString(question)
	// Words that only state the intent don't need to be on the page.
	if p.Intent == WhatsNew {
		var kept []string
		for _, t := range p.Terms {
			if !whatsNewWords[t] {
				kept = append(kept, t)
			}
		}
		p.Terms = kept
	}
	return p
}

var whatsNewWords = map[string]bool{"new": true, "latest": true, "recent": true, "changes": true, "changed": true,
	"release": true, "releases": true, "notes": true, "changelog": true, "updates": true, "features": true}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// quote makes text a single FTS5 string, so FTS5 syntax in it is read as text. Control
// characters are dropped: SQLite reads a NUL as the end of the query.
func quote(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// match builds the FTS5 query for terms and phrases: all of them (AND).
func match(terms, phrases []string) string {
	var parts []string
	for _, ph := range phrases {
		parts = append(parts, quote(ph))
	}
	for _, t := range terms {
		parts = append(parts, quote(t))
	}
	return strings.Join(parts, " ")
}

// matchAny builds an FTS5 query for any of the terms (OR), keeping phrases required.
func matchAny(terms, phrases []string) string {
	var req []string
	for _, ph := range phrases {
		req = append(req, quote(ph))
	}
	var any []string
	for _, t := range terms {
		any = append(any, quote(t))
	}
	if len(any) > 0 {
		req = append(req, "("+strings.Join(any, " OR ")+")")
	}
	return strings.Join(req, " AND ")
}
