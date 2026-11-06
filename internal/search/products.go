// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"slices"
	"strings"
	"unicode"
)

// Products are the product names the index uses (the sitemaps' names), each with the
// other names customers use for it: retired names (Tenable.io), short names, and
// former company names.
var Products = []Product{
	{Name: "Vulnerability Management", Aliases: []string{"tenable.io", "tenable io", "tio", "tvm",
		"tenable vulnerability management", "tenable one vulnerability management", "vulnerability management"}},
	{Name: "Security Center", Aliases: []string{"tenable.sc", "tenable sc", "tsc", "securitycenter",
		"security center", "tenable security center", "sc director", "security center director"},
		CaseSensitive: []string{"SC"}},
	{Name: "Nessus", Aliases: []string{"nessus", "tenable nessus", "nessus professional", "nessus expert",
		"nessus manager", "tenable nessus manager", "nessus essentials"}},
	{Name: "Tenable Agent", Aliases: []string{"nessus agent", "nessus agents", "tenable agent", "tenable agents",
		"tenable nessus agent", "tenable nessus agents"}},
	{Name: "Network Monitor", Aliases: []string{"nessus network monitor", "tenable network monitor", "network monitor",
		"nnm", "pvs", "passive vulnerability scanner"}},
	{Name: "OT Exposure", Aliases: []string{"tenable.ot", "tenable ot", "ot security", "tenable ot security",
		"ot exposure", "tenable ot exposure", "tenable one ot exposure", "industrial security", "indegy"}},
	{Name: "Identity Exposure", Aliases: []string{"tenable.ad", "tenable ad", "identity exposure",
		"tenable identity exposure", "tenable one identity exposure", "alsid"}},
	{Name: "Attack Surface Management", Aliases: []string{"tenable.asm", "tenable asm", "attack surface management",
		"tenable attack surface management", "bit discovery"}},
	{Name: "Cloud Exposure", Aliases: []string{"tenable.cs", "tenable cs", "tenable cloud security", "cloud security",
		"cloud exposure", "tenable cloud exposure", "tenable one cloud exposure", "ermetic", "cnapp"}},
	{Name: "Container Security", Aliases: []string{"container security", "tenable container security"}},
	{Name: "Lumin Exposure View", Aliases: []string{"lumin", "tenable lumin", "lumin exposure view"}},
	{Name: "Exposure Management", Aliases: []string{"exposure management", "tenable exposure management",
		"tenable one exposure management"}},
	{Name: "Tenable One", Aliases: []string{"tenable one", "tenable 1"}},
	{Name: "Web App Scanning", Aliases: []string{"web app scanning", "web application scanning", "tenable web app scanning",
		"tenable.io web application scanning", "tenable one web app scanning"}, CaseSensitive: []string{"WAS"}},
	{Name: "Tenable Core", Aliases: []string{"tenable core"}},
	{Name: "Tenable Enclave Security", Aliases: []string{"enclave security", "tenable enclave security"}},
	{Name: "Tenable Patch Management", Aliases: []string{"patch management", "tenable patch management"}},
	{Name: "Tenable MSSP", Aliases: []string{"mssp", "tenable mssp", "managed security service provider"}},
	{Name: "Tenable PCI ASV", Aliases: []string{"pci asv", "tenable pci asv"}},
	{Name: "Tenable Inventory", Aliases: []string{"tenable inventory"}},
	{Name: "Log Correlation Engine", Aliases: []string{"log correlation engine", "lce"}},
	{Name: "AI Exposure", Aliases: []string{"ai exposure", "tenable ai exposure", "tenable one ai exposure"}},
}

// Product is a product and its other names.
type Product struct {
	Name string
	// Aliases match in any case; CaseSensitive ones only as written, for short names
	// that are also common words ("WAS").
	Aliases       []string
	CaseSensitive []string
}

// ProductNamed returns the product a name or alias refers to.
func ProductNamed(name string) (string, bool) {
	toks := tokens(name)
	for _, m := range findProducts(toks) {
		if m.start == 0 && m.end == len(toks) {
			return m.product, true
		}
	}
	for _, p := range Products {
		if strings.EqualFold(p.Name, strings.TrimSpace(name)) {
			return p.Name, true
		}
	}
	return "", false
}

// token is a word of a query, as written and lowercased.
type token struct {
	raw, low string
}

// tokens splits text into words, keeping dots inside words (tenable.io) and plus signs
// and apostrophes, which other punctuation would split.
func tokens(s string) []token {
	var out []token
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '\'' && r != '’' && r != '+' && r != '_'
	}) {
		f = strings.Trim(f, ".'’+")
		if f != "" {
			out = append(out, token{raw: f, low: strings.ToLower(f)})
		}
	}
	return out
}

type productMatch struct {
	product    string
	start, end int // token range
}

type aliasEntry struct {
	product string
	words   []string
	exact   bool
}

var aliasTable = func() []aliasEntry {
	var out []aliasEntry
	for _, p := range Products {
		for _, a := range p.Aliases {
			var w []string
			for _, t := range tokens(a) {
				w = append(w, t.low)
			}
			out = append(out, aliasEntry{product: p.Name, words: w})
		}
		for _, a := range p.CaseSensitive {
			var w []string
			for _, t := range tokens(a) {
				w = append(w, t.raw)
			}
			out = append(out, aliasEntry{product: p.Name, words: w, exact: true})
		}
	}
	// Longest first, so "nessus network monitor" wins over "nessus".
	slices.SortStableFunc(out, func(a, b aliasEntry) int { return len(b.words) - len(a.words) })
	return out
}()

// findProducts finds the product names in a list of words, longest first and without
// overlaps, in the order they appear.
func findProducts(toks []token) []productMatch {
	var out []productMatch
	used := make([]bool, len(toks))
	for _, a := range aliasTable {
		for i := 0; i+len(a.words) <= len(toks); i++ {
			ok := true
			for j, w := range a.words {
				t := toks[i+j]
				if used[i+j] || a.exact && t.raw != w || !a.exact && t.low != w {
					ok = false
					break
				}
			}
			if ok {
				for j := range a.words {
					used[i+j] = true
				}
				out = append(out, productMatch{product: a.product, start: i, end: i + len(a.words)})
			}
		}
	}
	slices.SortFunc(out, func(a, b productMatch) int { return a.start - b.start })
	return out
}
