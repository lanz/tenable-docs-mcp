// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package extract

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

type counts struct {
	pages, settings, requirements, releases, terms, links int
	kinds                                                 map[string]int
}

type extracted struct {
	url          string
	settings     []Setting
	requirements []Requirement
	releases     []ReleaseEntry
	terms        []Term
}

func extractAll(p *page.Page) extracted {
	x := extracted{url: p.URL, settings: Settings(p), requirements: Requirements(p), terms: Glossary(p)}
	if IsReleaseNotes(p.URL) {
		x.releases = ReleaseNotes(p)
	}
	return x
}

// TestLocalExtract extracts everything from every English page in a local copy of the
// site, reports what each product yields, and logs a few random rows of each kind to
// check by eye. With TDM_LIVE=1 too, it extracts a sample of those pages from the live
// site and compares. It's skipped unless TDM_SITE_DIRS lists the folders (see
// site.ParseSiteDirs); TDM_SAMPLES sets how many rows to log (default 3).
func TestLocalExtract(t *testing.T) {
	v := os.Getenv("TDM_SITE_DIRS")
	if v == "" {
		t.Skip("set TDM_SITE_DIRS to read local copies of the sites")
	}
	dirs, err := site.ParseSiteDirs(v)
	if err != nil {
		t.Fatal(err)
	}
	if dirs["en"] == "" {
		t.Skip("TDM_SITE_DIRS has no folder for en")
	}
	ls, err := site.NewLocal(map[string]string{"en": dirs["en"]})
	if err != nil {
		t.Fatal(err)
	}
	samples := 3
	fmt.Sscan(os.Getenv("TDM_SAMPLES"), &samples)

	ctx := context.Background()
	entries, err := site.FetchSitemap(ctx, ls, site.Sites[0])
	if err != nil {
		t.Fatal(err)
	}
	byProduct := map[string]*counts{}
	var all []extracted
	graph := NewGraph()
	for _, e := range entries {
		resp, err := ls.Fetch(ctx, site.Request{URL: e.URL})
		if err != nil {
			continue
		}
		p, err := page.Parse(resp.Body, e.URL, nil)
		if errors.Is(err, page.ErrNotAPage) {
			continue
		} else if err != nil {
			t.Errorf("%s: %v", e.URL, err)
			continue
		}
		g, _ := site.GuideOf(e.URL)
		product, _ := page.Products(e.Products, g.Path)
		if product == "" {
			product = "(general)"
		}
		c := byProduct[product]
		if c == nil {
			c = &counts{kinds: map[string]int{}}
			byProduct[product] = c
		}
		x := extractAll(p)
		links := LinksOut(p)
		graph.Add(p.URL, links)
		c.pages++
		c.settings += len(x.settings)
		c.requirements += len(x.requirements)
		c.releases += len(x.releases)
		c.terms += len(x.terms)
		c.links += len(links)
		for _, r := range x.requirements {
			c.kinds[r.Kind]++
		}
		all = append(all, x)
	}

	names := make([]string, 0, len(byProduct))
	for n := range byProduct {
		names = append(names, n)
	}
	slices.SortFunc(names, func(a, b string) int { return cmp.Compare(byProduct[b].pages, byProduct[a].pages) })
	var total counts
	total.kinds = map[string]int{}
	t.Logf("%-40s %6s %8s %12s %8s %6s %7s  %s", "product", "pages", "settings", "requirements", "releases", "terms", "links", "requirement kinds")
	for _, n := range names {
		c := byProduct[n]
		t.Logf("%-40s %6d %8d %12d %8d %6d %7d  %v", n, c.pages, c.settings, c.requirements, c.releases, c.terms, c.links, c.kinds)
		total.pages += c.pages
		total.settings += c.settings
		total.requirements += c.requirements
		total.releases += c.releases
		total.terms += c.terms
		total.links += c.links
		for k, v := range c.kinds {
			total.kinds[k] += v
		}
	}
	t.Logf("%-40s %6d %8d %12d %8d %6d %7d  %v", "all", total.pages, total.settings, total.requirements, total.releases, total.terms, total.links, total.kinds)

	// The most linked-to pages.
	type ranked struct {
		url string
		n   int
	}
	var top []ranked
	for _, x := range all {
		top = append(top, ranked{x.url, len(graph.In(x.url))})
	}
	slices.SortFunc(top, func(a, b ranked) int { return cmp.Or(cmp.Compare(b.n, a.n), cmp.Compare(a.url, b.url)) })
	for _, r := range top[:min(5, len(top))] {
		t.Logf("linked from %d pages: %s", r.n, r.url)
	}

	// Spot checks.
	rng := rand.New(rand.NewPCG(1, uint64(time.Now().UnixNano())))
	var settings []Setting
	var reqs []Requirement
	var releases []ReleaseEntry
	var terms []Term
	for _, x := range all {
		settings = append(settings, x.settings...)
		reqs = append(reqs, x.requirements...)
		releases = append(releases, x.releases...)
		terms = append(terms, x.terms...)
	}
	for range min(samples, len(settings)) {
		s := settings[rng.IntN(len(settings))]
		t.Logf("setting %q default %q values %q required %q: %.100q (%s)", s.Name, s.Default, s.Values, s.Required, s.Description, s.URL)
	}
	for range min(samples, len(reqs)) {
		r := reqs[rng.IntN(len(reqs))]
		var f []string
		for _, x := range r.Fields {
			f = append(f, fmt.Sprintf("%s=%.40q", x.Label, x.Text))
		}
		t.Logf("requirement %s: %s (%s)", r.Kind, strings.Join(f, "; "), r.URL)
	}
	for range min(samples, len(releases)) {
		e := releases[rng.IntN(len(releases))]
		t.Logf("release %q version %q date %q, %d sections, %d bytes (%s)", e.Title, e.Version, e.Date, len(e.Sections), len(e.Markdown), e.URL)
	}
	for range min(samples, len(terms)) {
		x := terms[rng.IntN(len(terms))]
		t.Logf("term %q: %.100q (%s)", x.Term, x.Definition, x.URL)
	}

	if os.Getenv("TDM_LIVE") != "1" {
		return
	}
	// Compare a sample of pages that yield data with the live site.
	var withData []extracted
	for _, x := range all {
		if len(x.settings)+len(x.requirements)+len(x.releases)+len(x.terms) > 0 {
			withData = append(withData, x)
		}
	}
	src := site.NewHTTP(site.HTTPConfig{UserAgent: "tenable-docs-mcp/live-test (+https://github.com/lanz/tenable-docs-mcp)", RatePerSecond: 2})
	same := 0
	n := min(20, len(withData))
	for range n {
		x := withData[rng.IntN(len(withData))]
		resp, err := src.Fetch(ctx, site.Request{URL: x.url})
		if err != nil {
			t.Errorf("%s: %v", x.url, err)
			continue
		}
		p, err := page.Parse(resp.Body, x.url, nil)
		if err != nil {
			t.Errorf("%s: %v", x.url, err)
			continue
		}
		if reflect.DeepEqual(extractAll(p), x) {
			same++
		} else {
			t.Logf("%s: the live page yields different data; the local copy may be out of date", x.url)
		}
	}
	t.Logf("%d of %d sampled pages yield the same data live", same, n)
}
