// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package page

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// Markup that must never reach a section. (Pages can show HTML as examples, so this
// looks for Flare's own markers rather than any tag.)
var leftovers = []string{"<script", "mc-variable", "MCDropDown", "MCToggler", "initPrintButton", "data-mc-", "codeSnippetCopyButton", "masterFooter"}

// TestLocalCorpus parses every page in every site's sitemap from local copies of the
// sites' files, checks that no markup reaches a section, and reports how the pages
// split. It's skipped unless TDM_SITE_DIRS lists the folders (see site.ParseSiteDirs).
func TestLocalCorpus(t *testing.T) {
	v := os.Getenv("TDM_SITE_DIRS")
	if v == "" {
		t.Skip("set TDM_SITE_DIRS to read local copies of the sites")
	}
	dirs, err := site.ParseSiteDirs(v)
	if err != nil {
		t.Fatal(err)
	}
	ls, err := site.NewLocal(dirs)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start := time.Now()
	for _, s := range ls.Sites() {
		entries, err := site.FetchSitemap(ctx, ls, s)
		if err != nil {
			t.Errorf("%s: %v", s.Code, err)
			continue
		}
		pages, skipped, sections, bytes, untitled, single, big, reqs := 0, 0, 0, 0, 0, 0, 0, 0
		for _, e := range entries {
			resp, err := ls.Fetch(ctx, site.Request{URL: e.URL})
			if err != nil {
				continue
			}
			p, err := Parse(resp.Body, e.URL, nil)
			if errors.Is(err, ErrNotAPage) {
				skipped++
				continue
			} else if err != nil {
				t.Errorf("%s: %v", e.URL, err)
				continue
			}
			pages++
			sections += len(p.Sections)
			reqs += len(p.Requirements)
			if p.Title == "" {
				untitled++
			}
			if len(p.Sections) == 1 {
				single++
			}
			for _, sec := range p.Sections {
				bytes += len(sec.Markdown)
				if len(sec.Markdown) > 20000 {
					big++
				}
				for _, bad := range leftovers {
					if strings.Contains(sec.Markdown, bad) {
						t.Errorf("%s: section %q contains %q", e.URL, sec.Heading, bad)
					}
				}
			}
		}
		if pages > 0 {
			t.Logf("%s: %d pages (%d other documents skipped), %d sections (%.1f a page, %d pages with one), %d KB of Markdown, %d sections over 20 KB, %d untitled pages, %d requirements",
				s.Code, pages, skipped, sections, float64(sections)/float64(pages), single, bytes/1024, big, untitled, reqs)
		}
	}
	t.Logf("%s in all", time.Since(start).Round(time.Second))
}

// TestLiveSample parses pages from every documentation site: a few current pages from
// each, and pages from an older version of an English guide. With TDM_SITE_DIRS set
// too, it parses the same pages from the local folders and compares the sections. It's
// skipped unless TDM_LIVE=1.
func TestLiveSample(t *testing.T) {
	if os.Getenv("TDM_LIVE") != "1" {
		t.Skip("set TDM_LIVE=1 to read the live documentation sites")
	}
	const perSite = 3
	src := site.NewHTTP(site.HTTPConfig{UserAgent: "tenable-docs-mcp/live-test (+https://github.com/lanz/tenable-docs-mcp)", RatePerSecond: 2})
	var local *site.LocalSource
	if v := os.Getenv("TDM_SITE_DIRS"); v != "" {
		dirs, err := site.ParseSiteDirs(v)
		if err != nil {
			t.Fatal(err)
		}
		if local, err = site.NewLocal(dirs); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for _, s := range site.Sites {
		t.Run(s.Code, func(t *testing.T) {
			entries, err := site.FetchSitemap(ctx, src, s)
			if err != nil {
				t.Fatal(err)
			}
			var pages []site.Entry
			seen := map[string]bool{}
			older := 0
			for _, e := range entries {
				g, ok := site.GuideOf(e.URL)
				if !ok || !strings.HasSuffix(e.URL, ".htm") || seen[g.Path] {
					continue
				}
				switch {
				case g.Version == "" && len(pages)-older < perSite:
				case g.Version != "" && s.Code == "en" && older < 2:
					older++
				default:
					continue
				}
				seen[g.Path] = true
				pages = append(pages, e)
			}
			for _, e := range pages {
				resp, err := src.Fetch(ctx, site.Request{URL: e.URL})
				if err != nil {
					t.Errorf("%s: %v", e.URL, err)
					continue
				}
				p, err := Parse(resp.Body, resp.URL, nil)
				if err != nil {
					t.Errorf("%s: %v", e.URL, err)
					continue
				}
				if p.Title == "" || len(p.Sections) == 0 {
					t.Errorf("%s: title %q, %d sections", e.URL, p.Title, len(p.Sections))
				}
				size := 0
				for _, sec := range p.Sections {
					size += len(sec.Markdown)
					for _, bad := range leftovers {
						if strings.Contains(sec.Markdown, bad) {
							t.Errorf("%s: section %q contains %q", e.URL, sec.Heading, bad)
						}
					}
				}
				g, _ := site.GuideOf(e.URL)
				var trail []string
				toc, err := site.FetchToc(ctx, src, g)
				if err != nil {
					t.Errorf("%s: table of contents: %v", g.RootURL, err)
				} else {
					trail = site.TocTrail(toc, e.URL)
				}
				product, _ := Products(e.Products, g.Path)
				t.Logf("%s: %q (%s), %d sections, %d bytes, %d requirements, product %q, version %q, %d TOC levels",
					e.URL, p.Title, p.Lang, len(p.Sections), size, len(p.Requirements), product, g.Version, len(trail))
				if local == nil {
					continue
				}
				lr, err := local.Fetch(ctx, site.Request{URL: e.URL})
				if errors.Is(err, site.ErrNotFound) {
					t.Logf("%s: no local file", e.URL)
					continue
				} else if err != nil {
					t.Errorf("%s: local: %v", e.URL, err)
					continue
				}
				lp, err := Parse(lr.Body, e.URL, nil)
				if err != nil {
					t.Errorf("%s: local: %v", e.URL, err)
					continue
				}
				p.URL = lp.URL // the live response may have been redirected
				if !reflect.DeepEqual(lp, p) {
					t.Logf("%s: the local copy parses differently (%d and %d sections); it may be out of date", e.URL, len(lp.Sections), len(p.Sections))
				}
			}
		})
	}
}
