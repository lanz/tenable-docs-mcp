// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package build reads the documentation from a source, the live sites or local copies of
// them, for the index. For each site, it reads the sitemap and robots.txt, parses every
// page robots.txt allows, and hands the pages and their guides' tables of contents to a
// Sink: an index, or a snapshot for others to download.
package build

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// Page is a parsed page, with what the index keeps about it besides its content.
type Page struct {
	Page  *page.Page `json:"page"`
	Entry site.Entry `json:"entry"`
	// Trail is the page's place in its guide's table of contents.
	Trail []string `json:"trail,omitempty"`
	// LastModified is the page's Last-Modified time, for conditional requests later.
	LastModified string `json:"last_modified,omitempty"`
}

// Sink receives what a build reads, shard by shard.
type Sink interface {
	Page(ctx context.Context, k index.Key, p Page) error
	Toc(ctx context.Context, k index.Key, g site.Guide, toc []*site.TocEntry) error
}

// Options configure a build.
type Options struct {
	Source site.Source
	// Sites are the sites to read; each needs its sitemap in the source.
	Sites []site.Site
	// Agent is the robots.txt user-agent token to follow rules for.
	Agent string
	// Progress, when set, is called after each page, with the site and how many of its
	// sitemap's entries are done.
	Progress func(siteCode string, done, total int)
}

// Report says what a build read.
type Report struct {
	// Pages counts the pages given to the sink, by shard.
	Pages map[string]int `json:"pages"`
	Tocs  int            `json:"tocs"`
	// Blocked counts sitemap entries robots.txt disallows; Skipped, entries that
	// aren't pages or aren't in the source.
	Blocked int      `json:"blocked"`
	Skipped int      `json:"skipped"`
	Failed  []string `json:"failed,omitempty"`
}

// Total is how many pages the build gave the sink.
func (r Report) Total() int {
	n := 0
	for _, c := range r.Pages {
		n += c
	}
	return n
}

// maxFailures stops a build whose source is clearly broken.
const maxFailures = 100

// Run reads every site and gives its pages to sink.
func Run(ctx context.Context, opts Options, sink Sink) (Report, error) {
	rep := Report{Pages: map[string]int{}}
	agent := opts.Agent
	if agent == "" {
		agent = "tenable-docs-mcp"
	}
	for _, s := range opts.Sites {
		if err := buildSite(ctx, opts, s, agent, sink, &rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func buildSite(ctx context.Context, opts Options, s site.Site, agent string, sink Sink, rep *Report) error {
	rules, err := site.FetchRobots(ctx, opts.Source, s, agent)
	if err != nil {
		return fmt.Errorf("robots.txt for %s: %w", s.Host, err)
	}
	entries, err := site.FetchSitemap(ctx, opts.Source, s)
	if err != nil {
		return fmt.Errorf("sitemap for %s: %w", s.Host, err)
	}
	tocs := map[string][]*site.TocEntry{}
	for i, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := buildPage(ctx, opts.Source, s, e, rules, tocs, sink, rep); err != nil {
			return err
		}
		if opts.Progress != nil {
			opts.Progress(s.Code, i+1, len(entries))
		}
		if len(rep.Failed) > maxFailures {
			return fmt.Errorf("more than %d pages failed; the first: %s", maxFailures, rep.Failed[0])
		}
	}
	return nil
}

// isPagePath reports whether a sitemap entry is a page. Sitemaps also list documents such
// as PDFs, which aren't read.
func isPagePath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".htm", ".html":
		return true
	}
	return false
}

func buildPage(ctx context.Context, src site.Source, s site.Site, e site.Entry, rules *site.Robots,
	tocs map[string][]*site.TocEntry, sink Sink, rep *Report) error {
	u, err := url.Parse(e.URL)
	if err != nil || site.OnSite(e.URL, s) == "" || !isPagePath(u.Path) {
		rep.Skipped++
		return nil
	}
	if !rules.Allowed(u.EscapedPath()) {
		rep.Blocked++
		return nil
	}
	resp, err := src.Fetch(ctx, site.Request{URL: e.URL})
	switch {
	case errors.Is(err, site.ErrNotFound):
		rep.Skipped++
		return nil
	case err != nil:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rep.Failed = append(rep.Failed, e.URL+": "+err.Error())
		return nil
	}
	p, err := page.Parse(resp.Body, e.URL, nil)
	if errors.Is(err, page.ErrNotAPage) {
		rep.Skipped++
		return nil
	} else if err != nil {
		rep.Failed = append(rep.Failed, e.URL+": "+err.Error())
		return nil
	}
	g, _ := site.GuideOf(e.URL)
	k := index.Key{Site: s.Code, Older: g.Version != ""}
	toc, seen := tocs[g.RootURL]
	if !seen && g.RootURL != "" {
		toc, err = site.FetchToc(ctx, src, g)
		if err != nil && !errors.Is(err, site.ErrNotFound) {
			rep.Failed = append(rep.Failed, g.TocURL()+": "+err.Error())
		}
		tocs[g.RootURL] = toc
		if toc != nil {
			if err := sink.Toc(ctx, k, g, toc); err != nil {
				return err
			}
			rep.Tocs++
		}
	}
	if err := sink.Page(ctx, k, Page{Page: p, Entry: e, Trail: site.TocTrail(toc, e.URL), LastModified: resp.LastModified}); err != nil {
		return err
	}
	rep.Pages[k.String()]++
	return nil
}
