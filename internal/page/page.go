// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package page turns a published documentation page into sections an assistant can
// quote and cite: Markdown split at the page's headings, each with the URL of its
// heading, plus the page's metadata and required roles.
package page

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Page is one parsed documentation page.
type Page struct {
	URL         string
	Title       string
	Description string
	// Lang is the page's language as its html element gives it ("en-us", "ja-jp").
	Lang string
	// Product, ProductVersion, and GuideType come from the page's meta tags, which not
	// every page has. The sitemap is the fuller source of products; see Products.
	Product        string
	ProductVersion string
	GuideType      string
	// Requirements lists the roles and permissions the page says a task needs.
	Requirements []Requirement
	Sections     []Section
	// Tables, Links, and Terms hold the page's tables, its links, and the entries of a
	// Flare glossary, in page order, each with the section it's in.
	Tables []Table
	Links  []Link
	Terms  []Term
}

// Table is a table on the page, as data.
type Table struct {
	// Section is the index in Sections of the section the table is in.
	Section int
	Caption string
	// Headers has a label for each column, as plain text: the column's heading, with
	// the headings above it when there are several header rows ("Group › Column").
	// It's empty when the table has no header row.
	Headers []string
	Rows    []TableRow
}

// TableRow is one row of a table's body.
type TableRow struct {
	// Group is the subheading the row comes under: the text of the last row before it
	// that spans the whole table.
	Group string
	// Cells has each column's content in Markdown. A merged cell repeats in every
	// column and row it covers.
	Cells []string
}

// Link is a link on the page to an http or https URL.
type Link struct {
	Section int
	URL     string
	Text    string
}

// Term is an entry in a Flare glossary.
type Term struct {
	Section    int
	Term       string
	Definition string // in Markdown
	// Anchor is the term's anchor on the page, if it has one.
	Anchor string
}

// Requirement is one statement of the roles or permissions a task needs, such as
// "Required User Role: Administrator or Custom Role".
type Requirement struct {
	// Label is the text before the colon. It can name a product ("Required user role
	// when using Tenable Nessus Manager").
	Label string
	// Text is what follows the label.
	Text string
	// Roles lists the roles in Text, when the label is about roles and the page is in
	// English.
	Roles []string
}

// Section is part of a page: its introduction, or a heading and what follows it.
type Section struct {
	Heading string
	// Level is 1 for the introduction (under the page's title), 2 or 3 for a heading.
	Level int
	// Anchor is the heading's id, if it has one; URL ends with it.
	Anchor string
	URL    string
	// Trail lists the headings from the page's title down to this section's.
	Trail []string
	// Markdown is the section's content, starting with its heading.
	Markdown string
}

// Context returns the section's place on its page, as a header line for quoting it:
// "Page title › Heading".
func (s Section) Context() string { return strings.Join(s.Trail, " › ") }

// Options adjust parsing.
type Options struct {
	// MaxSectionBytes is the size above which a section is split again at its h3
	// headings. Zero means 6,000 bytes.
	MaxSectionBytes int
}

// ErrNotAPage is returned for documents in the sitemaps that aren't pages to read: PDFs,
// and the frames Flare publishes around each guide's topics (index.htm and
// index_CSH.htm), which have no title or content of their own.
var ErrNotAPage = errors.New("not a documentation page")

// Parse reads a published Flare page. pageURL is its address, which relative links
// are resolved against and section URLs start from.
func Parse(body []byte, pageURL string, opts *Options) (*Page, error) {
	base, err := url.Parse(pageURL)
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("page URL %q: not an absolute URL", pageURL)
	}
	base.Fragment = ""
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("%PDF-")) {
		return nil, fmt.Errorf("%s: %w (a PDF)", pageURL, ErrNotAPage)
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", pageURL, err)
	}
	p := &Page{URL: base.String()}
	titleTag := readMeta(doc, p)

	main := find(doc, func(x *html.Node) bool { return attr(x, "id") == "mc-main-content" })
	if main == nil {
		main = find(doc, func(x *html.Node) bool { return x.Data == "body" })
	}
	if main == nil {
		main = doc
	}
	r := newRenderer(base, main)
	blocks := r.blocks(main, ctx{top: true})
	for _, b := range blocks {
		if b.kind == kHeading && b.level == 1 {
			p.Title = b.plain
			break
		}
	}
	if p.Title == "" {
		p.Title = titleTag
	}
	if p.Title == "" && attr(main, "id") != "mc-main-content" {
		return nil, fmt.Errorf("%s: %w (no title or topic content)", pageURL, ErrNotAPage)
	}
	for _, n := range r.reqs {
		p.Requirements = append(p.Requirements, parseRequirement(collapse(plainText(n)), p.Lang))
	}
	limit := 6000
	if opts != nil && opts.MaxSectionBytes > 0 {
		limit = opts.MaxSectionBytes
	}
	var at map[int]int
	p.Sections, at = sections(blocks, p.Title, p.URL, limit)
	in := func(heading int) int { return at[heading] }
	for _, tb := range r.tables {
		tb.Section = in(tb.Section)
		p.Tables = append(p.Tables, tb)
	}
	for _, l := range r.links {
		l.Section = in(l.Section)
		p.Links = append(p.Links, l)
	}
	for _, term := range r.terms {
		term.Section = in(term.Section)
		p.Terms = append(p.Terms, term)
	}
	return p, nil
}

// readMeta fills in the page's language and meta tags, and returns its <title>.
func readMeta(doc *html.Node, p *Page) (title string) {
	if h := find(doc, func(x *html.Node) bool { return x.Data == "html" }); h != nil {
		p.Lang = strings.ToLower(strings.TrimSpace(attr(h, "lang")))
	}
	head := find(doc, func(x *html.Node) bool { return x.Data == "head" })
	if head == nil {
		return ""
	}
	for n := head.FirstChild; n != nil; n = n.NextSibling {
		if n.Type != html.ElementNode {
			continue
		}
		switch n.Data {
		case "title":
			title = strings.TrimSpace(collapse(rawText(n)))
		case "meta":
			v := strings.TrimSpace(collapse(attr(n, "content")))
			switch strings.ToLower(attr(n, "name")) {
			case "description":
				p.Description = v
			case "product":
				p.Product = v
			case "product-version":
				p.ProductVersion = v
			case "guide-type":
				p.GuideType = v
			}
		}
	}
	return title
}

// parseRequirement splits "Label: text" and, for English role lists, the roles.
func parseRequirement(s, lang string) Requirement {
	s = strings.TrimSpace(s)
	i := strings.IndexAny(s, ":：")
	if i < 0 {
		return Requirement{Text: s}
	}
	sep := len(":")
	if strings.HasPrefix(s[i:], "：") {
		sep = len("：")
	}
	req := Requirement{Label: strings.TrimSpace(s[:i]), Text: strings.TrimSpace(s[i+sep:])}
	if strings.HasPrefix(lang, "en") && strings.Contains(strings.ToLower(req.Label), "role") {
		req.Roles = splitRoles(req.Text)
	}
	return req
}

// splitRoles splits "Basic User, Scan Operator, or Administrator" into roles. Text that
// doesn't read as a short list gives nil.
func splitRoles(s string) []string {
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	var roles []string
	for _, part := range strings.Split(s, ",") {
		for _, r := range strings.Split(part, " or ") {
			r = strings.TrimSpace(r)
			r = strings.TrimSpace(strings.TrimPrefix(r, "or "))
			if r == "" {
				continue
			}
			if len(r) > 60 || strings.ContainsAny(r, ".;:") {
				return nil
			}
			roles = append(roles, r)
		}
	}
	return roles
}

// sections splits the page's blocks at its h2 headings, and splits a section larger
// than limit again at its h3 headings. It also returns the index of the section each
// heading ended up in, by heading id (0 for content before any heading).
func sections(blocks []block, title, pageURL string, limit int) ([]Section, map[int]int) {
	type group struct {
		head *block
		body []block
	}
	groups := []*group{{}}
	for i := range blocks {
		b := &blocks[i]
		cur := groups[len(groups)-1]
		switch {
		case b.kind == kHeading && b.level == 1 && len(groups) == 1 && cur.head == nil:
			cur.head = b
		case b.kind == kHeading && b.level <= 2:
			groups = append(groups, &group{head: b})
		default:
			cur.body = append(cur.body, *b)
		}
	}
	at := func(anchor string) string {
		if anchor == "" {
			return pageURL
		}
		return pageURL + "#" + anchor
	}
	trail := func(parts ...string) []string {
		var out []string
		for _, p := range parts {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	render := func(head *block, level int, body []block) string {
		var parts []string
		if head != nil {
			parts = append(parts, strings.Repeat("#", level)+" "+head.text)
		}
		if b := joinBlocks(body, false); b != "" {
			parts = append(parts, b)
		}
		return strings.Join(parts, "\n\n")
	}

	var out []Section
	where := map[int]int{}
	for gi, g := range groups {
		size := 0
		for _, b := range g.body {
			size += len(b.text)
		}
		level, heading, anchor := 2, "", ""
		if gi == 0 {
			level, heading = 1, title
			if g.head != nil {
				anchor = g.head.anchor
			}
		} else {
			heading, anchor = g.head.plain, g.head.anchor
		}
		h2 := ""
		if gi > 0 {
			h2 = heading
		}
		if gi == 0 && len(g.body) == 0 && len(groups) > 1 {
			where[0] = 0
			if g.head != nil {
				where[g.head.id] = 0
			}
			continue // a title with nothing under it
		}
		mark := func(b *block) {
			if b != nil {
				where[b.id] = len(out)
			} else {
				where[0] = len(out)
			}
		}
		// Split a long section at its h3 headings.
		var parts [][]block
		var heads []*block
		if size > limit {
			cur := []block{}
			for i := range g.body {
				b := g.body[i]
				if b.kind == kHeading && b.level == 3 {
					parts = append(parts, cur)
					heads = append(heads, &g.body[i])
					cur = []block{}
					continue
				}
				cur = append(cur, b)
			}
			parts = append(parts, cur)
		}
		if len(heads) == 0 {
			mark(g.head)
			for i := range g.body {
				if g.body[i].kind == kHeading {
					mark(&g.body[i])
				}
			}
			head := g.head
			if gi == 0 && head == nil && title != "" {
				head = &block{text: escapeText(title)}
			}
			out = append(out, Section{Heading: heading, Level: level, Anchor: anchor, URL: at(anchor),
				Trail: trail(title, h2), Markdown: render(head, level, g.body)})
			continue
		}
		mark(g.head)
		if len(parts[0]) > 0 {
			out = append(out, Section{Heading: heading, Level: level, Anchor: anchor, URL: at(anchor),
				Trail: trail(title, h2), Markdown: render(g.head, level, parts[0])})
		}
		for i, h := range heads {
			mark(h)
			out = append(out, Section{Heading: h.plain, Level: 3, Anchor: h.anchor, URL: at(h.anchor),
				Trail: trail(title, h2, h.plain), Markdown: render(h, 3, parts[i+1])})
		}
	}
	for id, i := range where {
		where[id] = min(i, max(len(out)-1, 0))
	}
	return out, where
}

// Products picks a page's product from the products the sitemap lists for it, in the
// sitemap's order. Tenable Core guides (under tenable-core/) list Tenable Core and the
// product the appliance runs; they belong to Tenable Core, with the other product as
// related. A page listing more than three products (general pages such as the
// glossary) has no single product.
func Products(listed []string, guidePath string) (product string, related []string) {
	core := strings.HasPrefix(strings.ToLower(guidePath)+"/", "tenable-core/")
	if core || len(listed) > 0 && listed[0] == "Tenable Core" {
		for _, p := range listed {
			if p != "Tenable Core" {
				related = append(related, p)
			}
		}
		return "Tenable Core", related
	}
	switch {
	case len(listed) == 0:
		return "", nil
	case len(listed) > 3:
		return "", listed
	}
	return listed[0], listed[1:]
}
