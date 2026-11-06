// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package extract pulls structured data out of parsed pages for the specialized
// tools: settings, requirements, release-note entries, glossary terms, and the links
// between pages.
//
// Settings and requirements are recognized by their tables' column headings, which are
// in English; on translated pages, use the English page's rows.
package extract

import (
	"regexp"
	"slices"
	"strings"

	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// Field is one labeled value from a table row.
type Field struct {
	Label string
	Text  string // in Markdown
}

// Place says where on a page something was found.
type Place struct {
	// Section is the index of the section in the page's Sections.
	Section int
	// URL is the section's URL, and Context its trail of headings.
	URL     string
	Context string
}

func placeOf(p *page.Page, section int) Place {
	if section < 0 || section >= len(p.Sections) {
		return Place{Section: section, URL: p.URL, Context: p.Title}
	}
	s := p.Sections[section]
	return Place{Section: section, URL: s.URL, Context: s.Context()}
}

// column is one column of a table, or several adjacent columns under the same heading.
type column struct {
	label string // as the page gives it
	norm  string // normalized for matching
	cols  []int
}

func columns(tb page.Table) []column {
	var out []column
	for i, h := range tb.Headers {
		n := normLabel(h)
		if len(out) > 0 && h != "" && out[len(out)-1].label == h {
			out[len(out)-1].cols = append(out[len(out)-1].cols, i)
			continue
		}
		out = append(out, column{label: h, norm: n, cols: []int{i}})
	}
	return out
}

// value returns a row's text in a column, joining the distinct values of its columns.
func (c column) value(r page.TableRow) string {
	var vals []string
	for _, i := range c.cols {
		if i < len(r.Cells) {
			if v := strings.TrimSpace(r.Cells[i]); v != "" && !slices.Contains(vals, v) {
				vals = append(vals, v)
			}
		}
	}
	return strings.Join(vals, " / ")
}

var (
	footnoteDigits = regexp.MustCompile(`(\pL)\d+$`) // "CPU Cores1" has a footnote marker
	spaces         = regexp.MustCompile(`\s+`)
)

// normLabel lowercases a column heading and drops what doesn't change its meaning.
func normLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = footnoteDigits.ReplaceAllString(s, "$1")
	s = strings.NewReplacer("(s)", "s", "?", "", ":", "", "*", "").Replace(s)
	return strings.TrimSpace(spaces.ReplaceAllString(s, " "))
}

func in(s string, set ...string) bool { return slices.Contains(set, s) }

// Setting is one row of a settings table.
type Setting struct {
	Name        string // as plain text
	Default     string
	Description string
	// Values lists the values the setting accepts, when the table gives them.
	Values   string
	Required string
	// Other holds the row's other columns.
	Other []Field
	// Group is the subheading the row comes under in its table, if any.
	Group string
	Place
}

// Column headings that name a setting (first column), and those for its details.
var (
	nameSuffixes = []string{"option", "options", "setting", "settings", "parameter", "parameters",
		"field", "fields", "argument", "arguments", "flag", "flags", "variable", "variables",
		"property", "properties", "attribute", "attributes", "preference", "preferences", "key"}
	descLabels     = []string{"description", "details", "definition", "action", "actions", "purpose", "notes", "meaning", "explanation"}
	defaultLabels  = []string{"default", "defaults", "default value", "default values", "default setting"}
	valuesLabels   = []string{"valid values", "valid value", "values", "value", "possible values", "accepted values", "allowed values", "options", "range", "valid range"}
	requiredLabels = []string{"required", "mandatory"}
)

func isNameLabel(n string) bool {
	for _, s := range nameSuffixes {
		if n == s || strings.HasSuffix(n, " "+s) {
			return true
		}
	}
	return false
}

// Settings returns the rows of the page's settings tables: tables whose first column
// names options, settings, parameters, or fields, with a description or default column.
func Settings(p *page.Page) []Setting {
	var out []Setting
	for _, tb := range p.Tables {
		cols := columns(tb)
		if !isSettingsTable(cols) {
			continue
		}
		first := cols[0]
		place := placeOf(p, tb.Section)
		for _, r := range tb.Rows {
			s := Setting{Name: Plain(first.value(r)), Group: r.Group, Place: place}
			if s.Name == "" {
				continue
			}
			for _, c := range cols[1:] {
				v := c.value(r)
				if v == "" {
					continue
				}
				switch {
				case in(c.norm, defaultLabels...) && s.Default == "":
					s.Default = v
				case in(c.norm, descLabels...) && s.Description == "":
					s.Description = v
				case in(c.norm, valuesLabels...) && s.Values == "":
					s.Values = v
				case in(c.norm, requiredLabels...) && s.Required == "":
					s.Required = v
				default:
					s.Other = append(s.Other, Field{Label: c.label, Text: v})
				}
			}
			out = append(out, s)
		}
	}
	return out
}

func isSettingsTable(cols []column) bool {
	if len(cols) < 2 {
		return false
	}
	hasDefault, hasDetail := false, false
	for _, c := range cols[1:] {
		hasDefault = hasDefault || in(c.norm, defaultLabels...)
		hasDetail = hasDetail || in(c.norm, descLabels...) || in(c.norm, defaultLabels...) || in(c.norm, valuesLabels...)
	}
	return hasDetail && (isNameLabel(cols[0].norm) || cols[0].norm == "name" && hasDefault)
}

// Kinds of requirements.
const (
	Ports        = "ports"
	Hardware     = "hardware"
	OS           = "operating-systems"
	Browsers     = "browsers"
	Dependencies = "dependencies"
	Other        = "other"
)

// Requirement is one row of a requirements table.
type Requirement struct {
	Kind   string
	Fields []Field
	Group  string
	Place
}

var (
	portWord     = regexp.MustCompile(`\bports?\b`)
	hardwareWord = regexp.MustCompile(`\b(cpus?|cores?|memory|ram|disk|disks|storage|processors?|hardware|instance types?|vcpus?)\b`)
	osWord       = regexp.MustCompile(`\b(operating systems?|os)\b`)
	osDetailWord = regexp.MustCompile(`\b(supported|versions?|architectures?|distributions?|releases?)\b`)
	browserWord  = regexp.MustCompile(`\bbrowsers?\b`)
	dependWord   = regexp.MustCompile(`\b(dependency|dependencies|prerequisites?|packages?|software requirements?|software name|librar(y|ies))\b`)
	// Column headings, and section headings, that mark a table of requirements.
	requireLabel   = regexp.MustCompile(`\b(requirements?|minimum|compatible|compatibility|tested versions?)\b`)
	requireContext = regexp.MustCompile(`(?i)\b(requirements?|prerequisites?|supported|support matrix|compatibility)\b`)
	// Tables of error and status messages (a code or message, and what to do about it)
	// aren't requirements, even on requirements pages.
	messageLabel = regexp.MustCompile(`\b(recommended actions?|error codes?|status codes?|status messages?|messages?|warnings?)\b`)
)

func isMessageTable(cols []column) bool {
	n := 0
	for _, c := range cols {
		if messageLabel.MatchString(c.norm) {
			n++
		}
	}
	return n > 0 && (n > 1 || slices.ContainsFunc(cols, func(c column) bool { return strings.HasPrefix(c.norm, "recommended action") }))
}

// requirementKind classifies a table by its column headings.
func requirementKind(cols []column) string {
	labels := make([]string, len(cols))
	for i, c := range cols {
		labels[i] = c.norm
	}
	all := strings.Join(labels, " | ")
	switch {
	case slices.ContainsFunc(labels, portWord.MatchString):
		return Ports
	case slices.ContainsFunc(labels, browserWord.MatchString):
		return Browsers
	case slices.ContainsFunc(labels, osWord.MatchString) && osDetailWord.MatchString(all):
		return OS
	case slices.ContainsFunc(labels, hardwareWord.MatchString):
		return Hardware
	case slices.ContainsFunc(labels, dependWord.MatchString):
		return Dependencies
	case requireLabel.MatchString(all):
		return Other
	}
	return ""
}

// Requirements returns the rows of the page's requirements tables: ports and
// protocols, hardware, operating systems, browsers, and dependencies, recognized by
// their columns, plus any other table in a section about requirements. Settings tables
// aren't included.
func Requirements(p *page.Page) []Requirement {
	var out []Requirement
	for _, tb := range p.Tables {
		cols := columns(tb)
		if len(cols) == 0 || isSettingsTable(cols) || isMessageTable(cols) {
			continue
		}
		place := placeOf(p, tb.Section)
		kind := requirementKind(cols)
		if kind == "" && requireContext.MatchString(place.Context) {
			kind = Other
		}
		if kind == "" {
			continue
		}
		for _, r := range tb.Rows {
			req := Requirement{Kind: kind, Group: r.Group, Place: place}
			for _, c := range cols {
				if v := c.value(r); v != "" {
					req.Fields = append(req.Fields, Field{Label: c.label, Text: v})
				}
			}
			if len(req.Fields) > 0 {
				out = append(out, req)
			}
		}
	}
	return out
}

// Term is a glossary term.
type Term struct {
	Term       string
	Definition string // in Markdown
	// URL points at the term on its page.
	URL string
}

// Glossary returns the page's glossary entries (Flare's glossary pages) and the rows of
// tables of terms and definitions.
func Glossary(p *page.Page) []Term {
	var out []Term
	for _, t := range p.Terms {
		u := placeOf(p, t.Section).URL
		if t.Anchor != "" {
			u = p.URL + "#" + t.Anchor
		}
		out = append(out, Term{Term: t.Term, Definition: t.Definition, URL: u})
	}
	for _, tb := range p.Tables {
		cols := columns(tb)
		if len(cols) != 2 || !in(cols[0].norm, "term", "terms", "acronym", "acronyms", "abbreviation", "abbreviations") ||
			!in(cols[1].norm, "definition", "description", "meaning", "stands for") {
			continue
		}
		u := placeOf(p, tb.Section).URL
		for _, r := range tb.Rows {
			term, def := Plain(cols[0].value(r)), cols[1].value(r)
			if term != "" && def != "" {
				out = append(out, Term{Term: term, Definition: def, URL: u})
			}
		}
	}
	return out
}

// LinksOut returns the documentation pages a page links to, without fragments, each
// once, in page order.
func LinksOut(p *page.Page) []string {
	var out []string
	seen := map[string]bool{p.URL: true}
	for _, l := range p.Links {
		if !site.IsDocsURL(l.URL) {
			continue
		}
		u, _, _ := strings.Cut(l.URL, "#")
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// Graph holds the links between pages, in both directions.
type Graph struct {
	out map[string][]string
	in  map[string][]string
}

// NewGraph returns an empty graph.
func NewGraph() *Graph { return &Graph{out: map[string][]string{}, in: map[string][]string{}} }

// Add records a page's links, replacing any recorded before for it.
func (g *Graph) Add(pageURL string, links []string) {
	for _, old := range g.out[pageURL] {
		g.in[old] = slices.DeleteFunc(g.in[old], func(s string) bool { return s == pageURL })
	}
	g.out[pageURL] = slices.Clone(links)
	for _, l := range links {
		g.in[l] = append(g.in[l], pageURL)
	}
}

// Out returns the pages a page links to.
func (g *Graph) Out(pageURL string) []string { return slices.Clone(g.out[pageURL]) }

// In returns the pages that link to a page, sorted.
func (g *Graph) In(pageURL string) []string {
	s := slices.Clone(g.in[pageURL])
	slices.Sort(s)
	return s
}

var (
	mdLink   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdEscape = regexp.MustCompile(`\\([\\*\x60\[\]<_])`)
)

// Plain turns a short piece of the parser's Markdown into plain text: links become
// their text, and emphasis, code marks, and escapes are dropped.
func Plain(md string) string {
	s := mdLink.ReplaceAllString(md, "$1")
	s = strings.NewReplacer("**", "", "`", "", "<br>", " ").Replace(s)
	s = mdEscape.ReplaceAllString(s, "$1")
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	if strings.HasPrefix(s, "*") && strings.HasSuffix(s, "*") && len(s) > 2 {
		s = s[1 : len(s)-1]
	}
	return s
}
