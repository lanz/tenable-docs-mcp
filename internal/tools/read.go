// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

const (
	defaultMaxChars = 8000
	maxMaxChars     = 30000
)

type readIn struct {
	URL      string `json:"url" jsonschema:"a docs page or section link, as search_docs returns it; a link with #anchor reads that section, one without reads the whole page"`
	Part     int    `json:"part,omitempty" jsonschema:"which part of a long section or page to read, from 1 (default 1)"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"the most characters to return in one part, from 1000 to 30000 (default 8000)"`
}

type link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type readOut struct {
	URL           string   `json:"url" jsonschema:"the link to cite for what was read"`
	Title         string   `json:"title"`
	Heading       string   `json:"heading,omitempty" jsonschema:"the section read; empty for a whole page"`
	Product       string   `json:"product,omitempty"`
	GuideType     string   `json:"guide_type,omitempty"`
	Version       string   `json:"version,omitempty" jsonschema:"the guide version; empty for the current one"`
	Language      string   `json:"language"`
	Location      string   `json:"location" jsonschema:"where the page is in its guide's table of contents"`
	RequiredRoles []string `json:"required_roles,omitempty"`
	LastModified  string   `json:"last_modified,omitempty"`
	Markdown      string   `json:"markdown" jsonschema:"the content, starting with a context header"`
	Part          int      `json:"part"`
	Parts         int      `json:"parts" jsonschema:"how many parts the content has; read the next with part"`
	Previous      *link    `json:"previous,omitempty" jsonschema:"the section or page before this one"`
	Next          *link    `json:"next,omitempty" jsonschema:"the section or page after this one"`
	variantsOut
}

type outlineIn struct {
	URL      string `json:"url,omitempty" jsonschema:"a page, for its headings"`
	Guide    string `json:"guide,omitempty" jsonschema:"a guide's path, such as nessus or security-center/6_8, for its pages; give url or guide"`
	Language string `json:"language,omitempty" jsonschema:"the site code of the guide's language (default en)"`
}

type outlineItem struct {
	Level  int    `json:"level" jsonschema:"1 for the page title, 2 and 3 for headings; for a guide, the depth in its table of contents"`
	Title  string `json:"title"`
	URL    string `json:"url,omitempty"`
	Anchor string `json:"anchor,omitempty"`
	Words  int    `json:"words,omitempty"`
}

type outlineOut struct {
	Title string        `json:"title"`
	URL   string        `json:"url,omitempty"`
	Items []outlineItem `json:"items"`
	Words int           `json:"words" jsonschema:"the total words"`
}

func registerRead(s *mcp.Server, env *Env) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "read_doc",
		Description: "Read a documentation section or page as Markdown, with a header saying where it is, the roles it " +
			"needs, and when it changed. Pass a link from search_docs: with an #anchor it reads that section, without " +
			"one the whole page. Long content comes in parts; previous and next point to the neighboring sections. " +
			"Cite the url it returns.",
		Annotations: readOnly("Read a doc"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, readOut, error) {
		u, err := normalizeURL(in.URL)
		if err != nil {
			return nil, readOut{}, err
		}
		maxChars := in.MaxChars
		switch {
		case maxChars == 0:
			maxChars = defaultMaxChars
		case maxChars < 1000:
			maxChars = 1000
		case maxChars > maxMaxChars:
			maxChars = maxMaxChars
		}
		out, err := env.read(ctx, u, in.Part, maxChars)
		if err != nil {
			return nil, readOut{}, err
		}
		return summary("%s", out.Markdown), out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "outline",
		Description: "List a page's headings, with links to each and their word counts, or a guide's pages from its " +
			"table of contents. Use it to find the right section of a long page, or to see what a guide covers.",
		Annotations: readOnly("Outline"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in outlineIn) (*mcp.CallToolResult, outlineOut, error) {
		if in.URL == "" && in.Guide == "" {
			return nil, outlineOut{}, fmt.Errorf("give url (a page) or guide (a guide's path)")
		}
		if in.URL != "" {
			u, err := normalizeURL(in.URL)
			if err != nil {
				return nil, outlineOut{}, err
			}
			pageURL, _, _ := strings.Cut(u, "#")
			var out outlineOut
			err = env.withPage(ctx, pageURL, func(sh *index.Shard, info *index.PageInfo) error {
				sections, err := sh.Sections(ctx, pageURL)
				if err != nil {
					return err
				}
				out = outlineOut{Title: info.Title, URL: pageURL, Items: []outlineItem{}}
				for _, sec := range sections {
					w := len(index.Words(sec.Body))
					out.Items = append(out.Items, outlineItem{Level: sec.Level, Title: sec.Heading, URL: sec.URL, Anchor: sec.Anchor, Words: w})
					out.Words += w
				}
				return nil
			})
			if err != nil {
				return nil, outlineOut{}, err
			}
			return summary("%s", outlineSummary(out)), out, nil
		}
		out, err := env.guideOutline(ctx, in.Guide, in.Language)
		if err != nil {
			return nil, outlineOut{}, err
		}
		return summary("%s", outlineSummary(out)), out, nil
	})
}

// read reads a page, or the section its #anchor names, in parts of at most
// maxChars characters.
func (e *Env) read(ctx context.Context, u string, part, maxChars int) (readOut, error) {
	pageURL, anchor, _ := strings.Cut(u, "#")
	var out readOut
	err := e.withPage(ctx, pageURL, func(sh *index.Shard, info *index.PageInfo) error {
		sections, err := sh.Sections(ctx, pageURL)
		if err != nil {
			return err
		}
		out = readOut{URL: pageURL, Title: info.Title, Product: info.Product, GuideType: info.GuideType,
			Version: info.Version, Language: info.Site, Location: strings.Join(info.TocTrail, " › "),
			RequiredRoles: roleTexts(info.Roles), LastModified: info.LastMod}
		body := ""
		chosen := -1
		if anchor != "" {
			for i, sec := range sections {
				if strings.EqualFold(sec.Anchor, anchor) {
					chosen = i
					break
				}
			}
		}
		if chosen >= 0 {
			sec := sections[chosen]
			out.URL, out.Heading, body = sec.URL, sec.Heading, sec.Body
			if chosen > 0 {
				out.Previous = &link{Title: sections[chosen-1].Heading, URL: sections[chosen-1].URL}
			}
			if chosen+1 < len(sections) {
				out.Next = &link{Title: sections[chosen+1].Heading, URL: sections[chosen+1].URL}
			}
		} else {
			var parts []string
			for _, sec := range sections {
				parts = append(parts, sec.Body)
			}
			body = strings.Join(parts, "\n\n")
		}
		if out.Previous == nil || out.Next == nil {
			prev, next, err := tocNeighbors(ctx, sh, info)
			if err != nil {
				return err
			}
			if out.Previous == nil && (chosen <= 0) {
				out.Previous = prev
			}
			if out.Next == nil && (chosen < 0 || chosen == len(sections)-1) {
				out.Next = next
			}
		}
		chunks := splitText(body, maxChars)
		out.Parts = len(chunks)
		out.Part = max(part, 1)
		if out.Part > out.Parts {
			return fmt.Errorf("part %d doesn't exist: %s has %d parts", out.Part, out.URL, out.Parts)
		}
		out.Markdown = contextHeader(out, anchor != "" && chosen < 0) + chunks[out.Part-1]
		return nil
	})
	if err != nil {
		return readOut{}, err
	}
	if e.Engine != nil {
		v, err := e.Engine.Variants(ctx, pageURL)
		if err != nil {
			return readOut{}, err
		}
		out.variantsOut = toVariants(v)
		if v.English != "" && anchor != "" {
			out.English = v.English + "#" + anchor
		}
	}
	return out, nil
}

// guideOutline lists a guide's pages from its table of contents.
func (e *Env) guideOutline(ctx context.Context, guide, language string) (outlineOut, error) {
	g, err := findGuide(ctx, e, guide, language)
	if err != nil {
		return outlineOut{}, err
	}
	out := outlineOut{Title: g.path, URL: g.root, Items: []outlineItem{}}
	var walk func([]*site.TocEntry, int)
	walk = func(entries []*site.TocEntry, depth int) {
		for _, t := range entries {
			out.Items = append(out.Items, outlineItem{Level: depth, Title: t.Title, URL: t.URL})
			walk(t.Children, depth+1)
		}
	}
	walk(g.toc, 1)
	return out, nil
}

// contextHeader says where content comes from, for quoting it.
func contextHeader(o readOut, missingAnchor bool) string {
	var b strings.Builder
	where := o.Title
	if o.Heading != "" && o.Heading != o.Title {
		where += " › " + o.Heading
	}
	fmt.Fprintf(&b, "> **%s**\n", where)
	var about []string
	for _, x := range []string{o.Product, o.GuideType} {
		if x != "" {
			about = append(about, x)
		}
	}
	if o.Version != "" {
		about = append(about, "version "+o.Version)
	}
	if o.Location != "" {
		about = append(about, "in "+o.Location)
	}
	if len(about) > 0 {
		fmt.Fprintf(&b, "> %s\n", strings.Join(about, ", "))
	}
	if len(o.RequiredRoles) > 0 {
		fmt.Fprintf(&b, "> %s\n", strings.Join(o.RequiredRoles, "; "))
	}
	src := "> Source: " + o.URL
	if o.LastModified != "" {
		src += " (updated " + o.LastModified + ")"
	}
	b.WriteString(src + "\n")
	if o.Parts > 1 {
		fmt.Fprintf(&b, "> Part %d of %d\n", max(o.Part, 1), o.Parts)
	}
	if missingAnchor {
		b.WriteString("> The page has no section with that anchor, so this is the whole page.\n")
	}
	return b.String() + "\n"
}

// splitText splits text into parts of at most n characters, between paragraphs where
// it can, then between lines, then anywhere.
func splitText(s string, n int) []string {
	if utf8.RuneCountInString(s) <= n {
		return []string{s}
	}
	var parts []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, strings.TrimRight(cur.String(), "\n"))
			cur.Reset()
		}
	}
	add := func(piece, sep string) {
		if cur.Len() > 0 && utf8.RuneCountInString(cur.String())+utf8.RuneCountInString(sep+piece) > n {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteString(sep)
		}
		cur.WriteString(piece)
	}
	for _, para := range strings.Split(s, "\n\n") {
		if utf8.RuneCountInString(para) <= n {
			add(para, "\n\n")
			continue
		}
		for _, line := range strings.Split(para, "\n") {
			for utf8.RuneCountInString(line) > n {
				r := []rune(line)
				add(string(r[:n]), "\n")
				line = string(r[n:])
			}
			add(line, "\n")
		}
	}
	flush()
	return parts
}

// tocNeighbors returns the pages before and after a page in its guide's table of
// contents.
func tocNeighbors(ctx context.Context, sh *index.Shard, info *index.PageInfo) (prev, next *link, err error) {
	toc, _, ok, err := sh.Toc(ctx, info.Guide)
	if err != nil || !ok {
		return nil, nil, err
	}
	var flat []link
	var walk func([]*site.TocEntry)
	walk = func(es []*site.TocEntry) {
		for _, e := range es {
			if u, _, _ := strings.Cut(e.URL, "#"); u != "" && (len(flat) == 0 || flat[len(flat)-1].URL != u) {
				flat = append(flat, link{Title: e.Title, URL: u})
			}
			walk(e.Children)
		}
	}
	walk(toc)
	for i, l := range flat {
		if strings.EqualFold(l.URL, info.URL) {
			if i > 0 {
				prev = &flat[i-1]
			}
			if i+1 < len(flat) {
				next = &flat[i+1]
			}
			break
		}
	}
	return prev, next, nil
}

func outlineSummary(o outlineOut) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s", o.Title)
	if o.URL != "" {
		fmt.Fprintf(&b, " (%s)", o.URL)
	}
	if o.Words > 0 {
		fmt.Fprintf(&b, ", %d words", o.Words)
	}
	b.WriteString(":\n")
	for _, it := range o.Items {
		indent := strings.Repeat("  ", max(it.Level-1, 0))
		line := indent + "- " + it.Title
		if it.Words > 0 {
			line += fmt.Sprintf(" (%d words)", it.Words)
		}
		if it.URL != "" {
			line += " " + it.URL
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
