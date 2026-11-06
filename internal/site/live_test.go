// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package site

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLocalSiteFolders reads every configured site's sitemap from local copies of the
// sites' published files, checks each entry has a file, and compares one page with the
// live site. It's skipped unless TDM_SITE_DIRS lists the folders as code=folder pairs
// (see ParseSiteDirs); the comparison also needs TDM_LIVE=1.
func TestLocalSiteFolders(t *testing.T) {
	v := os.Getenv("TDM_SITE_DIRS")
	if v == "" {
		t.Skip("set TDM_SITE_DIRS to read local copies of the sites")
	}
	dirs, err := ParseSiteDirs(v)
	if err != nil {
		t.Fatal(err)
	}
	ls, err := NewLocal(dirs)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, s := range ls.Sites() {
		entries, err := FetchSitemap(ctx, ls, s)
		if err != nil {
			t.Errorf("%s sitemap: %v", s.Code, err)
			continue
		}
		missing := 0
		for _, e := range entries {
			if _, err := ls.Fetch(ctx, Request{URL: e.URL, LastModified: "probe"}); errors.Is(err, ErrNotFound) {
				missing++
			}
		}
		t.Logf("%s: %d sitemap entries, %d without a local file", s.Code, len(entries), missing)
	}
	if os.Getenv("TDM_LIVE") != "1" || dirs["en"] == "" {
		return
	}
	const page = "https://docs.tenable.com/agent/Content/AgentDeploymentWorkflow.htm"
	local, err := ls.Fetch(ctx, Request{URL: page})
	if err != nil {
		t.Fatal(err)
	}
	live, err := NewHTTP(HTTPConfig{UserAgent: "tenable-docs-mcp/live-test (+https://github.com/lanz/tenable-docs-mcp)"}).Fetch(ctx, Request{URL: page})
	if err != nil {
		t.Fatal(err)
	}
	if string(local.Body) != string(live.Body) {
		t.Logf("%s differs between the local copy and the live site (%d and %d bytes)", page, len(local.Body), len(live.Body))
	}
}

// TestLive reads every documentation site: its robots.txt, its sitemap, one current page
// of one product, and that page's table of contents, then refetches the page
// conditionally. It's skipped unless TDM_LIVE=1, and sends about 40 requests in all.
func TestLive(t *testing.T) {
	if os.Getenv("TDM_LIVE") != "1" {
		t.Skip("set TDM_LIVE=1 to read the live documentation sites")
	}
	src := NewHTTP(HTTPConfig{UserAgent: "tenable-docs-mcp/live-test (+https://github.com/lanz/tenable-docs-mcp)", RatePerSecond: 2})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for _, s := range Sites {
		t.Run(s.Code, func(t *testing.T) {
			rb, err := FetchRobots(ctx, src, s, "tenable-docs-mcp")
			if err != nil {
				t.Fatalf("robots.txt: %v", err)
			}
			entries, err := FetchSitemap(ctx, src, s)
			if err != nil {
				t.Fatalf("sitemap: %v", err)
			}
			var page Entry
			var guide Guide
			allowed, versioned := 0, 0
			for _, e := range entries {
				u, err := url.Parse(e.URL)
				if err != nil || !rb.Allowed(u.Path) {
					continue
				}
				allowed++
				g, ok := GuideOf(e.URL)
				if ok && g.Version != "" {
					versioned++
				}
				if page.URL == "" && ok && g.Version == "" && len(e.Products) > 0 && strings.HasSuffix(e.URL, ".htm") {
					page, guide = e, g
				}
			}
			if len(entries) == 0 || page.URL == "" {
				t.Fatalf("%d sitemap entries, and no current page with a product", len(entries))
			}
			resp, err := src.Fetch(ctx, Request{URL: page.URL})
			if err != nil || len(resp.Body) == 0 {
				t.Fatalf("page %s: %v", page.URL, err)
			}
			again, err := src.Fetch(ctx, Request{URL: resp.URL, ETag: resp.ETag, LastModified: resp.LastModified})
			if err != nil {
				t.Fatalf("conditional refetch: %v", err)
			}
			// Some guides are published without FullToc.xml (on 2026-11-06, the Korean site's
			// had only Flare's JavaScript tables of contents), so a missing one is logged.
			toc, err := FetchToc(ctx, src, guide)
			if errors.Is(err, ErrNotFound) {
				t.Logf("%s has no FullToc.xml", guide.RootURL)
			} else if err != nil {
				t.Fatalf("table of contents %s: %v", guide.TocURL(), err)
			}
			t.Logf("%d sitemap entries (%d allowed by robots.txt, %d older versions); %s (%s): %d bytes, refetch not modified: %v; %d top-level TOC entries",
				len(entries), allowed, versioned, page.URL, strings.Join(page.Products, ", "), len(resp.Body), again.NotModified, len(toc))
		})
	}
}
