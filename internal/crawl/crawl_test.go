// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package crawl

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

var ctx = context.Background()

const en = "https://docs.tenable.com/"

// fakeSite is a made-up docs.tenable.com: pages with sitemap dates and ETags, robots.txt
// rules, and redirects. It answers conditional requests with a 304 when the ETag
// matches, and logs every request.
type fakeSite struct {
	mu        sync.Mutex
	pages     map[string]string // URL → body
	lastmod   map[string]string
	etags     map[string]string
	redirects map[string]string // from → to
	disallow  []string
	sitemap   []string // URLs to list, when set; else every page
	requests  []string
}

func newSite() *fakeSite {
	return &fakeSite{pages: map[string]string{}, lastmod: map[string]string{}, etags: map[string]string{}, redirects: map[string]string{}}
}

func (f *fakeSite) put(path, title, text, lastmod string) {
	u := en + path
	f.pages[u] = `<html lang="en-us"><head><title>` + title + `</title></head><body><div id="mc-main-content"><h1>` + title + `</h1><p>` + text + `</p></div></body></html>`
	f.lastmod[u] = lastmod
	f.etags[u] = fmt.Sprintf(`"%x"`, len(text)*7919+len(title)+len(lastmod))
}

func (f *fakeSite) Fetch(_ context.Context, req site.Request) (*site.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req.URL)
	switch req.URL {
	case en + "robots.txt":
		body := "User-agent: *\n"
		for _, d := range f.disallow {
			body += "Disallow: " + d + "\n"
		}
		return &site.Response{URL: req.URL, Status: 200, Body: []byte(body)}, nil
	case en + "Sitemap.xml":
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
		urls := f.sitemap
		if urls == nil {
			for u := range f.pages {
				urls = append(urls, u)
			}
			for u := range f.redirects {
				urls = append(urls, u)
			}
			sort.Strings(urls)
		}
		for _, u := range urls {
			fmt.Fprintf(&b, `<url><loc>%s</loc><lastmod>%s</lastmod><metadata><docproductname>Widget Manager</docproductname><docguidetype>User Guide</docguidetype></metadata></url>`, u, f.lastmod[u])
		}
		b.WriteString(`</urlset>`)
		return &site.Response{URL: req.URL, Status: 200, Body: []byte(b.String())}, nil
	}
	u := req.URL
	if to, ok := f.redirects[u]; ok {
		u = to
	}
	body, ok := f.pages[u]
	if !ok {
		return &site.Response{URL: u, Status: 404}, fmt.Errorf("fetch %s: %w", u, site.ErrNotFound)
	}
	etag := f.etags[u]
	if u == req.URL && etag != "" && req.ETag == etag {
		return &site.Response{URL: u, Status: 304, NotModified: true, ETag: etag}, nil
	}
	return &site.Response{URL: u, Status: 200, Body: []byte(body), ETag: etag, LastModified: "Sun, 08 Nov 2026 12:00:00 GMT"}, nil
}

// pageRequests returns the page fetches since the last call, without robots.txt, the
// sitemap, and tables of contents.
func (f *fakeSite) pageRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		if strings.HasSuffix(r, ".htm") {
			out = append(out, r)
		}
	}
	f.requests = nil
	return out
}

var day0 = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func newCrawler(t *testing.T, f *fakeSite) (*Crawler, *index.Index) {
	t.Helper()
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	en, _ := site.ByCode("en")
	return &Crawler{Index: ix, Source: f, Sites: []site.Site{en}, Now: func() time.Time { return day0 }}, ix
}

func pagesIn(t *testing.T, ix *index.Index, k index.Key) []string {
	t.Helper()
	var out []string
	if ix.Shard(k) == nil {
		return nil
	}
	err := ix.With(k, func(sh *index.Shard) error {
		dates, err := sh.PageDates(ctx)
		for u := range dates {
			out = append(out, strings.TrimPrefix(u, en))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func changes(t *testing.T, ix *index.Index, k index.Key) int {
	t.Helper()
	var n int
	_ = ix.With(k, func(sh *index.Shard) error {
		c, err := sh.Counts(ctx)
		n = c["changes"]
		return err
	})
	return n
}

// widgets makes a site with n current pages and one older-version page.
func widgets(n int) *fakeSite {
	f := newSite()
	for i := range n {
		f.put(fmt.Sprintf("widget/Content/Page%02d.htm", i), fmt.Sprintf("Page %d", i), fmt.Sprintf("Widget text %d", i), "2026-10-01")
	}
	f.put("widget/10_1/Content/Start.htm", "Old start", "Older widget text", "2025-04-01")
	return f
}

var (
	current = index.Key{Site: "en"}
	older   = index.Key{Site: "en", Older: true}
)

func TestFirstSyncReadsTheSiteInFull(t *testing.T) {
	f := widgets(3)
	c, ix := newCrawler(t, f)
	rep, err := c.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.Built, []string{"en-current", "en-older"}) || rep.Added != 4 {
		t.Errorf("report = %+v", rep)
	}
	if got := pagesIn(t, ix, current); len(got) != 3 {
		t.Errorf("current pages = %v", got)
	}
	var src string
	_ = ix.With(current, func(sh *index.Shard) error { src, _ = sh.Meta(ctx, "source"); return nil })
	if src != "live" {
		t.Errorf("source meta = %q", src)
	}
}

func TestSyncFetchesOnlyWhatChanged(t *testing.T) {
	f := widgets(40)
	c, ix := newCrawler(t, f)
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	f.pageRequests()
	base := changes(t, ix, current)

	// Nothing changed: only today's share of the rotation is checked, and each check
	// is a 304.
	rep, err := c.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reqs := f.pageRequests()
	turn := c.turn()
	for _, r := range reqs {
		if share(r) != turn {
			t.Errorf("fetched %s, which isn't today's turn", r)
		}
	}
	if len(reqs) == 0 || len(reqs) > 20 || rep.Changed() != 0 || rep.Unchanged != len(reqs) {
		t.Errorf("no-change sync: %d requests, report %+v", len(reqs), rep)
	}

	// One page edited (with a new sitemap date), one added, one dropped.
	f.put("widget/Content/Page05.htm", "Page 5", "Widget text 5, edited", "2026-11-07")
	f.put("widget/Content/New.htm", "New page", "Brand new widget text", "2026-11-07")
	delete(f.pages, en+"widget/Content/Page07.htm")
	rep, err = c.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Updated != 1 || rep.Added != 1 || rep.Removed != 1 {
		t.Errorf("changed sync: %+v", rep)
	}
	got := pagesIn(t, ix, current)
	if !slices.Contains(got, "widget/Content/New.htm") || slices.Contains(got, "widget/Content/Page07.htm") {
		t.Errorf("pages = %v", got)
	}
	if n := changes(t, ix, current); n != base+3 {
		t.Errorf("change history grew by %d, want 3", n-base)
	}
}

func TestRotationChecksEveryPageInAWeek(t *testing.T) {
	f := widgets(30)
	c, _ := newCrawler(t, f)
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	f.pageRequests()
	seen := map[string]int{}
	for day := range Rotation {
		c.Now = func() time.Time { return day0.Add(time.Duration(day+1) * 24 * time.Hour) }
		if _, err := c.Sync(ctx); err != nil {
			t.Fatal(err)
		}
		for _, r := range f.pageRequests() {
			seen[r]++
		}
	}
	for u := range f.pages {
		if seen[u] != 1 {
			t.Errorf("%s checked %d times in a week", u, seen[u])
		}
	}
}

func TestSyncSitemapDateUpdatedOn304(t *testing.T) {
	f := widgets(2)
	c, ix := newCrawler(t, f)
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	// The sitemap date changes but the page doesn't: a 304, and the new date is kept,
	// so the next sync doesn't fetch it again.
	u := en + "widget/Content/Page00.htm"
	f.lastmod[u] = "2026-11-07"
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var d index.PageDate
	_ = ix.With(current, func(sh *index.Shard) error {
		dates, _ := sh.PageDates(ctx)
		d = dates[u]
		return nil
	})
	if d.LastMod != "2026-11-07" {
		t.Errorf("lastmod = %q", d.LastMod)
	}
}

func TestSyncRecordsRedirects(t *testing.T) {
	f := widgets(3)
	c, ix := newCrawler(t, f)
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	// Page01 moves to Moved.htm; the sitemap lists both for now.
	from, to := en+"widget/Content/Page01.htm", en+"widget/Content/Moved.htm"
	f.pages[to] = f.pages[from]
	f.lastmod[to], f.etags[to] = "2026-11-07", `"moved"`
	delete(f.pages, from)
	f.redirects[from] = to
	f.lastmod[from] = "2026-11-07"
	rep, err := c.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Redirected != 1 || rep.Added != 1 || rep.Removed != 1 {
		t.Errorf("report = %+v", rep)
	}
	var got string
	_ = ix.With(current, func(sh *index.Shard) error { got, _, _ = sh.Redirect(ctx, from); return nil })
	if got != to {
		t.Errorf("redirect = %q", got)
	}
	if pages := pagesIn(t, ix, current); slices.Contains(pages, "widget/Content/Page01.htm") || !slices.Contains(pages, "widget/Content/Moved.htm") {
		t.Errorf("pages = %v", pages)
	}
}

func TestSyncRemovesDisallowedPages(t *testing.T) {
	f := widgets(3)
	c, ix := newCrawler(t, f)
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	f.disallow = []string{"/widget/10_1/"}
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if got := pagesIn(t, ix, older); len(got) != 0 {
		t.Errorf("older pages = %v", got)
	}
}

func TestBrokenSitemapRemovesNothing(t *testing.T) {
	f := widgets(80)
	c, ix := newCrawler(t, f)
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	f.sitemap = []string{en + "widget/Content/Page00.htm"}
	rep, err := c.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Removed != 0 || len(rep.Failed) != 1 || !strings.Contains(rep.Failed[0], "broken sitemap") {
		t.Errorf("report = %+v", rep)
	}
	if got := pagesIn(t, ix, current); len(got) != 80 {
		t.Errorf("%d current pages left", len(got))
	}
}

func TestSyncCreatesAMissingShard(t *testing.T) {
	f := newSite()
	f.put("widget/Content/Start.htm", "Start", "Current widget text", "2026-10-01")
	c, ix := newCrawler(t, f)
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if ix.Shard(older) != nil {
		t.Fatal("an older shard with no pages")
	}
	// The first older version is published.
	f.put("widget/10_1/Content/Start.htm", "Old start", "Older widget text", "2026-11-07")
	rep, err := c.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Added != 1 || !slices.Equal(pagesIn(t, ix, older), []string{"widget/10_1/Content/Start.htm"}) {
		t.Errorf("report %+v; older pages %v", rep, pagesIn(t, ix, older))
	}
}

func TestFailedPagesAreReported(t *testing.T) {
	f := widgets(2)
	c, ix := newCrawler(t, f)
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	broken := &failing{fakeSite: f, url: en + "widget/Content/Page00.htm"}
	c.Source = broken
	f.lastmod[broken.url] = "2026-11-07"
	rep, err := c.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Failed) != 1 || !strings.Contains(rep.Failed[0], "Page00.htm") {
		t.Errorf("report = %+v", rep)
	}
	// A page that failed to fetch stays in the index.
	if got := pagesIn(t, ix, current); !slices.Contains(got, "widget/Content/Page00.htm") {
		t.Errorf("pages = %v", got)
	}
}

type failing struct {
	*fakeSite
	url string
}

func (f *failing) Fetch(ctx context.Context, req site.Request) (*site.Response, error) {
	if req.URL == f.url {
		return nil, fmt.Errorf("fetch %s: server error", req.URL)
	}
	return f.fakeSite.Fetch(ctx, req)
}

func TestEntriesThatArentPagesAreRemembered(t *testing.T) {
	f := widgets(20)
	gone := en + "widget/Content/Gone.htm"
	f.sitemap = nil
	f.lastmod[gone] = "2026-10-01"
	// The sitemap lists Gone.htm, but the site doesn't have it.
	listed := func() []string {
		var urls []string
		for u := range f.pages {
			urls = append(urls, u)
		}
		urls = append(urls, gone)
		sort.Strings(urls)
		return urls
	}
	f.sitemap = listed()
	c, _ := newCrawler(t, f)
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	f.pageRequests()
	// The first sync after the full read fetches it once and remembers it.
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	f.pageRequests()
	fetched := func() bool {
		for _, r := range f.pageRequests() {
			if r == gone {
				return true
			}
		}
		return false
	}
	// Later syncs leave it alone, unless it's their turn in the rotation.
	if share(gone) == c.turn() {
		c.Now = func() time.Time { return day0.Add(24 * time.Hour) }
	}
	if _, err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if fetched() {
		t.Error("fetched again with the same sitemap date")
	}
	// A new sitemap date: fetched again, and once it's published, indexed.
	f.lastmod[gone] = "2026-11-08"
	f.put("widget/Content/Gone.htm", "Back", "Published at last", "2026-11-08")
	rep, err := c.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !fetched() || rep.Added != 1 {
		t.Errorf("after the date changed: report %+v", rep)
	}
}
