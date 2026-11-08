// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package crawl

import (
	"errors"
	"slices"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/index"
)

func TestOnDemandPage(t *testing.T) {
	f := widgets(2)
	c, ix := newCrawler(t, f)
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	o := &OnDemand{Crawler: c}

	// A page published since the last sync.
	f.put("widget/Content/Fresh.htm", "Fresh", "Just published", "2026-11-08")
	got, err := o.Page(ctx, en+"widget/Content/Fresh.htm")
	if err != nil || got != en+"widget/Content/Fresh.htm" {
		t.Fatalf("Page = %q, %v", got, err)
	}
	if pages := pagesIn(t, ix, current); !slices.Contains(pages, "widget/Content/Fresh.htm") {
		t.Errorf("pages = %v", pages)
	}

	// A page that moved: stored at its new address, with the redirect recorded.
	from, to := en+"widget/Content/Old.htm", en+"widget/Content/Fresh2.htm"
	f.put("widget/Content/Fresh2.htm", "Fresh 2", "Moved here", "2026-11-08")
	f.redirects[from] = to
	if got, err := o.Page(ctx, from); err != nil || got != to {
		t.Fatalf("moved page = %q, %v", got, err)
	}
	var moved string
	_ = ix.With(current, func(sh *index.Shard) error { moved, _, _ = sh.Redirect(ctx, from); return nil })
	if moved != to {
		t.Errorf("redirect = %q", moved)
	}

	// Pages it won't add.
	f.disallow = []string{"/secret/"}
	f.put("secret/Content/Hidden.htm", "Hidden", "Not for robots", "2026-11-08")
	o.robots = nil // fetch the new rules
	for _, u := range []string{
		en + "widget/Content/Nowhere.htm", // not on the site
		en + "secret/Content/Hidden.htm",  // robots.txt disallows it
		en + "widget/Content/Guide.pdf",   // not a page
		"https://example.com/x.htm",       // not a docs site
	} {
		if _, err := o.Page(ctx, u); !errors.Is(err, ErrNotPage) {
			t.Errorf("%s: err = %v", u, err)
		}
	}
	if pages := pagesIn(t, ix, current); slices.Contains(pages, "secret/Content/Hidden.htm") {
		t.Error("a disallowed page was stored")
	}
}

// A page asked for in a version set the index has no shard for gets one.
func TestOnDemandCreatesShard(t *testing.T) {
	f := newSite()
	f.put("widget/Content/Start.htm", "Start", "Current", "2026-10-01")
	c, ix := newCrawler(t, f)
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	f.put("widget/10_1/Content/Start.htm", "Old start", "Older", "2025-04-01")
	if _, err := (&OnDemand{Crawler: c}).Page(ctx, en+"widget/10_1/Content/Start.htm"); err != nil {
		t.Fatal(err)
	}
	if got := pagesIn(t, ix, older); len(got) != 1 {
		t.Errorf("older pages = %v", got)
	}
}
