// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/search"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

type guideRef struct {
	key  index.Key
	path string
	root string
	toc  []*site.TocEntry
}

// findGuide finds a guide by its path or by the URL of any of its pages.
func findGuide(ctx context.Context, env *Env, guide, language string) (*guideRef, error) {
	if err := env.ready(); err != nil {
		return nil, err
	}
	guide = strings.TrimSpace(guide)
	code := "en"
	if language != "" {
		c, ok := search.SiteFor(language)
		if !ok {
			return nil, fmt.Errorf("no documentation in %q", language)
		}
		code = c
	}
	path := strings.Trim(guide, "/")
	if strings.Contains(guide, ".tenable.com") || strings.Contains(guide, "://") {
		u, err := normalizeURL(guide)
		if err != nil {
			return nil, err
		}
		g, ok := site.GuideOf(u)
		if !ok {
			return nil, fmt.Errorf("%s isn't in a guide", u)
		}
		k, _ := index.KeyFor(u)
		code, path = k.Site, g.Path
	}
	g, _ := site.GuideOf("https://docs.tenable.com/" + path + "/Content/x.htm")
	k := index.Key{Site: code, Older: g.Version != ""}
	if env.Index.Shard(k) == nil {
		return nil, fmt.Errorf("the index has no %s documentation", k)
	}
	ref := &guideRef{key: k, path: path}
	var guides []index.GuideInfo
	err := env.Index.With(k, func(s *index.Shard) error {
		toc, root, ok, err := s.Toc(ctx, path)
		if err != nil {
			return err
		}
		if ok {
			ref.toc, ref.root = toc, root
			return nil
		}
		guides, err = s.Guides(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	if ref.toc == nil {
		var near []string
		for _, gi := range guides {
			if strings.Contains(strings.ToLower(gi.Path), strings.ToLower(path)) && !slices.Contains(near, gi.Path) {
				near = append(near, gi.Path)
			}
		}
		msg := fmt.Sprintf("no guide %q in the %s documentation", path, k)
		if len(near) > 0 {
			msg += "; did you mean " + strings.Join(near[:min(len(near), 8)], ", ") + "?"
		} else {
			msg += "; list_products lists the guides"
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return ref, nil
}

type browseIn struct {
	Guide    string `json:"guide" jsonschema:"a guide's path (nessus, security-center/director, nessus/10_8) or the URL of any page in it"`
	Language string `json:"language,omitempty" jsonschema:"the site code of the guide's language, when guide is a path (default en)"`
	Depth    int    `json:"depth,omitempty" jsonschema:"how many levels of the table of contents to show, from 1 to 10 (default 3)"`
}

// tocNode is one entry of a table of contents, in a flat list in reading order.
type tocNode struct {
	Depth int    `json:"depth" jsonschema:"1 for the top level; an entry belongs to the closest one above it with a smaller depth"`
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	// More counts the entries below this one that depth left out.
	More int `json:"more,omitempty" jsonschema:"how many entries below this one the depth left out"`
}

type browseOut struct {
	Guide    string    `json:"guide"`
	Language string    `json:"language"`
	RootURL  string    `json:"root_url"`
	Entries  []tocNode `json:"entries" jsonschema:"the table of contents in reading order, each entry with its depth"`
	Total    int       `json:"total" jsonschema:"every entry in the table of contents, shown or not"`
}

type relatedIn struct {
	URL string `json:"url" jsonschema:"a docs page"`
}

type productPage struct {
	Product string `json:"product"`
	URL     string `json:"url"`
	Version string `json:"version,omitempty"`
}

type relatedOut struct {
	URL       string        `json:"url"`
	Title     string        `json:"title"`
	Product   string        `json:"product,omitempty"`
	Parent    *link         `json:"parent,omitempty" jsonschema:"the entry above this page in the table of contents"`
	Previous  *link         `json:"previous,omitempty"`
	Next      *link         `json:"next,omitempty"`
	LinksOut  []link        `json:"links_out,omitempty" jsonschema:"docs pages this page links to"`
	LinksIn   []link        `json:"links_in,omitempty" jsonschema:"docs pages that link to this page"`
	SameTitle []productPage `json:"same_title,omitempty" jsonschema:"pages with the same title in other products or guides"`
	variantsOut
}

type productsIn struct{}

type guideOut struct {
	Path      string   `json:"path"`
	GuideType string   `json:"guide_type,omitempty"`
	Pages     int      `json:"pages"`
	Versions  []string `json:"older_versions,omitempty" jsonschema:"older versions with their own guides"`
}

type productOut struct {
	Name      string     `json:"name"`
	Pages     int        `json:"pages" jsonschema:"current-version pages in every language"`
	Languages []string   `json:"languages" jsonschema:"site codes of the languages with pages"`
	Guides    []guideOut `json:"guides,omitempty" jsonschema:"English guides"`
}

type languageOut struct {
	Language    string `json:"language" jsonschema:"the site code"`
	Lang        string `json:"lang" jsonschema:"the BCP 47 tag"`
	Pages       int    `json:"pages"`
	OlderPages  int    `json:"older_version_pages"`
	LastIndexed string `json:"last_indexed,omitempty"`
}

type productsOut struct {
	Products  []productOut  `json:"products"`
	Languages []languageOut `json:"languages"`
}

func registerBrowse(s *mcp.Server, env *Env) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "browse_guide",
		Description: "Show a guide's table of contents, with links, to see what it covers or find a page by its place. " +
			"Give the guide's path (nessus, security-center/director, nessus/10_8) or any page's URL.",
		Annotations: readOnly("Browse a guide"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in browseIn) (*mcp.CallToolResult, browseOut, error) {
		g, err := findGuide(ctx, env, in.Guide, in.Language)
		if err != nil {
			return nil, browseOut{}, err
		}
		depth := in.Depth
		switch {
		case depth <= 0:
			depth = 3
		case depth > 10:
			depth = 10
		}
		out := browseOut{Guide: g.path, Language: g.key.Site, RootURL: g.root, Entries: []tocNode{}}
		var convert func([]*site.TocEntry, int)
		count := func(es []*site.TocEntry) int {
			n := 0
			var walk func([]*site.TocEntry)
			walk = func(es []*site.TocEntry) {
				for _, e := range es {
					n++
					walk(e.Children)
				}
			}
			walk(es)
			return n
		}
		convert = func(es []*site.TocEntry, d int) {
			for _, e := range es {
				n := tocNode{Depth: d, Title: e.Title, URL: e.URL}
				if d >= depth {
					n.More = count(e.Children)
				}
				out.Entries = append(out.Entries, n)
				if d < depth {
					convert(e.Children, d+1)
				}
			}
		}
		convert(g.toc, 1)
		out.Total = count(g.toc)
		var b strings.Builder
		fmt.Fprintf(&b, "%s (%s), %d entries:\n", g.path, g.root, out.Total)
		for _, n := range out.Entries {
			line := strings.Repeat("  ", n.Depth-1) + "- " + n.Title
			if n.URL != "" {
				line += " " + n.URL
			}
			if n.More > 0 {
				line += fmt.Sprintf(" (+%d more)", n.More)
			}
			b.WriteString(line + "\n")
		}
		return summary("%s", strings.TrimRight(b.String(), "\n")), out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "related_pages",
		Description: "Show what's around a docs page: its place in the table of contents (parent, previous, next), the " +
			"pages it links to and that link to it, same-titled pages in other products, and the page in other " +
			"languages and versions.",
		Annotations: readsLive("Related pages"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in relatedIn) (*mcp.CallToolResult, relatedOut, error) {
		u, err := normalizeURL(in.URL)
		if err != nil {
			return nil, relatedOut{}, err
		}
		pageURL, _, _ := strings.Cut(u, "#")
		var out relatedOut
		err = env.withPage(ctx, pageURL, func(sh *index.Shard, info *index.PageInfo) error {
			pageURL = info.URL // the page found, which may be where a redirect led
			out = relatedOut{URL: pageURL, Title: info.Title, Product: info.Product}
			var err error
			if out.Previous, out.Next, err = tocNeighbors(ctx, sh, info); err != nil {
				return err
			}
			if out.Parent, err = tocParent(ctx, sh, info); err != nil {
				return err
			}
			titled := func(urls []string) []link {
				var ls []link
				for _, x := range urls[:min(len(urls), 30)] {
					l := link{URL: x}
					if k, ok := index.KeyFor(x); ok && k == sh.Key {
						if p, ok, _ := sh.PageInfo(ctx, x); ok {
							l.Title = p.Title
						}
					}
					ls = append(ls, l)
				}
				return ls
			}
			outs, err := sh.LinksFrom(ctx, pageURL)
			if err != nil {
				return err
			}
			ins, err := sh.LinksTo(ctx, pageURL)
			if err != nil {
				return err
			}
			out.LinksOut, out.LinksIn = titled(outs), titled(ins)
			refs, products, err := sh.SameTitle(ctx, info.Title, pageURL)
			if err != nil {
				return err
			}
			for i, r := range refs {
				out.SameTitle = append(out.SameTitle, productPage{Product: products[i], URL: r.URL, Version: r.Version})
			}
			return nil
		})
		if err != nil {
			return nil, relatedOut{}, err
		}
		v, err := env.Engine.Variants(ctx, pageURL)
		if err != nil {
			return nil, relatedOut{}, err
		}
		out.variantsOut = toVariants(v)
		var b strings.Builder
		fmt.Fprintf(&b, "%s (%s)\n", out.Title, out.URL)
		for _, x := range []struct {
			label string
			l     *link
		}{{"In", out.Parent}, {"Previous", out.Previous}, {"Next", out.Next}} {
			if x.l != nil {
				fmt.Fprintf(&b, "%s: %s %s\n", x.label, x.l.Title, x.l.URL)
			}
		}
		fmt.Fprintf(&b, "Links to %d pages; linked from %d.", len(out.LinksOut), len(out.LinksIn))
		if len(out.SameTitle) > 0 {
			fmt.Fprintf(&b, " Same title in %d other places.", len(out.SameTitle))
		}
		if n := len(out.Languages); n > 0 {
			fmt.Fprintf(&b, " In %d other languages.", n)
		}
		if n := len(out.Versions); n > 0 {
			fmt.Fprintf(&b, " %d other versions.", n)
		}
		return summary("%s", b.String()), out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_products",
		Description: "List the products and languages the documentation index covers, with page counts, each " +
			"product's English guides and older versions, and when each language was last indexed.",
		Annotations: readOnly("List products"),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in productsIn) (*mcp.CallToolResult, productsOut, error) {
		if err := env.ready(); err != nil {
			return nil, productsOut{}, err
		}
		products := map[string]*productOut{}
		get := func(name string) *productOut {
			if products[name] == nil {
				products[name] = &productOut{Name: name}
			}
			return products[name]
		}
		langs := map[string]*languageOut{}
		olderGuides := map[string][]string{} // current guide path → older versions
		for _, k := range env.Index.Keys() {
			err := env.Index.With(k, func(s *index.Shard) error {
				lo := langs[k.Site]
				if lo == nil {
					st, _ := site.ByCode(k.Site)
					lo = &languageOut{Language: k.Site, Lang: st.Lang}
					langs[k.Site] = lo
				}
				counts, err := s.ProductCounts(ctx)
				if err != nil {
					return err
				}
				for name, n := range counts {
					if k.Older {
						lo.OlderPages += n
						continue
					}
					lo.Pages += n
					if name == "" {
						continue
					}
					p := get(name)
					p.Pages += n
					if !slices.Contains(p.Languages, k.Site) {
						p.Languages = append(p.Languages, k.Site)
					}
				}
				if t, err := s.LastIndexed(ctx); err == nil && t > lo.LastIndexed {
					lo.LastIndexed = t
				}
				if k.Site != "en" {
					return nil
				}
				guides, err := s.Guides(ctx)
				if err != nil {
					return err
				}
				for _, g := range guides {
					if k.Older {
						cur := site.CurrentURL("https://docs.tenable.com/" + g.Path + "/Content/x.htm")
						cg, _ := site.GuideOf(cur)
						olderGuides[cg.Path] = append(olderGuides[cg.Path], g.Version)
						continue
					}
					if g.Product == "" {
						continue
					}
					p := get(g.Product)
					p.Guides = append(p.Guides, guideOut{Path: g.Path, GuideType: g.GuideType, Pages: g.Pages})
				}
				return nil
			})
			if err != nil {
				return nil, productsOut{}, err
			}
		}
		out := productsOut{}
		for _, p := range products {
			for i := range p.Guides {
				vs := olderGuides[p.Guides[i].Path]
				sort.Slice(vs, func(a, b int) bool { return index.CompareVersions(vs[a], vs[b]) > 0 })
				p.Guides[i].Versions = vs
			}
			sort.Strings(p.Languages)
			out.Products = append(out.Products, *p)
		}
		sort.Slice(out.Products, func(a, b int) bool { return out.Products[a].Pages > out.Products[b].Pages })
		for _, s := range site.Sites {
			if lo := langs[s.Code]; lo != nil {
				out.Languages = append(out.Languages, *lo)
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d products in %d languages:\n", len(out.Products), len(out.Languages))
		for _, p := range out.Products {
			fmt.Fprintf(&b, "- %s: %d pages (%s), %d guides\n", p.Name, p.Pages, strings.Join(p.Languages, ", "), len(p.Guides))
		}
		for _, l := range out.Languages {
			fmt.Fprintf(&b, "Language %s: %d pages, %d in older versions, indexed %s\n", l.Language, l.Pages, l.OlderPages, l.LastIndexed)
		}
		return summary("%s", strings.TrimRight(b.String(), "\n")), out, nil
	})
}

// tocParent returns the entry above a page in its guide's table of contents.
func tocParent(ctx context.Context, sh *index.Shard, info *index.PageInfo) (*link, error) {
	toc, _, ok, err := sh.Toc(ctx, info.Guide)
	if err != nil || !ok {
		return nil, err
	}
	var found *link
	var walk func([]*site.TocEntry, *site.TocEntry) bool
	walk = func(es []*site.TocEntry, parent *site.TocEntry) bool {
		for _, e := range es {
			if u, _, _ := strings.Cut(e.URL, "#"); strings.EqualFold(u, info.URL) {
				if parent != nil {
					found = &link{Title: parent.Title, URL: parent.URL}
				}
				return true
			}
			if walk(e.Children, e) {
				return true
			}
		}
		return false
	}
	walk(toc, nil)
	return found, nil
}
