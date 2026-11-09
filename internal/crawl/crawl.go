// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package crawl builds and updates the index from the live documentation sites, for
// when the published snapshot can't be reached.
//
// A site the index has nothing for is read in full, as a snapshot build reads it, and
// swapped in when it's done. After that, each sync compares the sitemap with the
// index: it fetches pages that are new or whose sitemap date changed, removes pages the
// sitemap no longer lists or robots.txt now disallows, and records pages that redirect.
// Sitemap dates can lag behind the pages, so each sync also checks a seventh of the
// remaining pages with conditional requests, which cost a 304 when nothing changed;
// over a week, every page is checked. The index's change history records what each
// sync changed.
package crawl

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/build"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// Rotation is how many syncs it takes to check every page whose sitemap date didn't
// change: a week of daily syncs.
const Rotation = 7

// batchSize is how many pages are written in one transaction.
const batchSize = 200

// maxRemoveShare guards against a broken sitemap: a sync that would remove more than
// this share of a site's pages (and more than minRemoveGuard of them) removes none,
// and reports why.
const (
	maxRemoveShare = 0.2
	minRemoveGuard = 50
)

// Crawler syncs an index with the live sites.
type Crawler struct {
	Index *index.Index
	// Source fetches from the live sites (site.HTTPSource, which keeps to polite
	// limits).
	Source site.Source
	// Agent is the robots.txt user-agent token to follow rules for.
	Agent string
	// Sites are the sites to sync (default every site).
	Sites []site.Site
	// Progress, when set, reports what the crawl is doing.
	Progress func(msg string)
	// Now is the clock (default time.Now); it picks each sync's share of the rotation.
	Now func() time.Time
}

// Report says what a sync did.
type Report struct {
	// Built lists the shards read in full, for sites the index had nothing for.
	Built []string `json:"built,omitempty"`
	// Checked counts pages fetched; Added, Updated, Unchanged, and Removed what came of
	// them and of the sitemap comparison.
	Checked   int `json:"checked"`
	Added     int `json:"added"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Removed   int `json:"removed"`
	// Redirected counts pages found to redirect to another address.
	Redirected int `json:"redirected"`
	// Failed lists pages that couldn't be fetched or stored, with why.
	Failed []string `json:"failed,omitempty"`
}

// Changed counts the pages a sync added, updated, or removed.
func (r Report) Changed() int { return r.Added + r.Updated + r.Removed }

func (c *Crawler) agent() string {
	if c.Agent == "" {
		return "tenable-docs-mcp"
	}
	return c.Agent
}

func (c *Crawler) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Crawler) progress(format string, args ...any) {
	if c.Progress != nil {
		c.Progress(fmt.Sprintf(format, args...))
	}
}

// Sync brings the index up to date with the live sites.
func (c *Crawler) Sync(ctx context.Context) (Report, error) {
	var rep Report
	sites := c.Sites
	if len(sites) == 0 {
		sites = site.Sites
	}
	for _, s := range sites {
		current, older := index.Key{Site: s.Code}, index.Key{Site: s.Code, Older: true}
		var err error
		if c.Index.Shard(current) == nil && c.Index.Shard(older) == nil {
			err = c.buildSite(ctx, s, &rep)
		} else {
			err = c.syncSite(ctx, s, &rep)
		}
		if err != nil {
			return rep, fmt.Errorf("%s: %w", s.Host, err)
		}
	}
	return rep, nil
}

// buildSite reads a whole site into new shards, and swaps them in when it's done.
func (c *Crawler) buildSite(ctx context.Context, s site.Site, rep *Report) error {
	c.progress("Reading %s in full; this takes a while", s.Host)
	sink := build.NewIndexSink(c.Index, c.now().UTC().Format(time.RFC3339))
	opts := build.Options{Source: c.Source, Sites: []site.Site{s}, Agent: c.agent(), Live: true,
		Progress: func(code string, done, total int) {
			if done%500 == 0 || done == total {
				c.progress("Reading %s: %d of %d sitemap entries", s.Host, done, total)
			}
		}}
	br, err := build.Run(ctx, opts, sink)
	rep.Failed = append(rep.Failed, br.Failed...)
	if err != nil {
		sink.Discard()
		return err
	}
	for _, k := range sink.Keys() {
		sink.SetMeta(k, "source", "live")
	}
	if err := sink.Finish(ctx); err != nil {
		sink.Discard()
		return err
	}
	for k, n := range br.Pages {
		rep.Built = append(rep.Built, k)
		rep.Added += n
		rep.Checked += n
	}
	slices.Sort(rep.Built)
	return nil
}

// known is a page the index has.
type known struct {
	key  index.Key
	date index.PageDate
}

// syncSite compares a site's sitemap with the index and updates what differs.
func (c *Crawler) syncSite(ctx context.Context, s site.Site, rep *Report) error {
	c.progress("Checking %s for changes", s.Host)
	rules, err := site.FetchRobots(ctx, c.Source, s, c.agent())
	if err != nil {
		return fmt.Errorf("robots.txt: %w", err)
	}
	entries, err := site.FetchSitemap(ctx, c.Source, s)
	if err != nil {
		return fmt.Errorf("sitemap: %w", err)
	}
	have := map[string]known{}
	skipped := map[string]string{}
	for _, k := range []index.Key{{Site: s.Code}, {Site: s.Code, Older: true}} {
		if c.Index.Shard(k) == nil {
			continue
		}
		err := c.Index.With(k, func(sh *index.Shard) error {
			dates, err := sh.PageDates(ctx)
			for u, d := range dates {
				have[u] = known{key: k, date: d}
			}
			if err != nil {
				return err
			}
			skip, err := sh.Skipped(ctx)
			for u, lastmod := range skip {
				skipped[u] = lastmod
			}
			return err
		})
		if err != nil {
			return err
		}
	}

	w := newWriter(c, s, rep)
	listed := map[string]bool{}
	turn := c.turn()
	for i, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 && i%1000 == 0 {
			c.progress("Checking %s: %d of %d sitemap entries", s.Host, i, len(entries))
		}
		u, err := url.Parse(e.URL)
		if err != nil || site.OnSite(e.URL, s) == "" || !isPagePath(u.Path) || !rules.Allowed(u.EscapedPath()) {
			continue
		}
		listed[e.URL] = true
		h, ok := have[e.URL]
		lastmod, wasSkipped := skipped[e.URL]
		switch {
		case !ok && wasSkipped && lastmod == e.LastMod && share(e.URL) != turn:
			continue // not a page last time, and its sitemap date hasn't changed
		case !ok, h.date.LastMod != e.LastMod:
		case share(e.URL) == turn:
		default:
			continue // unchanged, and not this sync's turn
		}
		if err := w.fetch(ctx, e, h, ok); err != nil {
			return err
		}
	}
	// Pages the sitemap no longer lists, or robots.txt now disallows.
	var gone []string
	for u := range have {
		if !listed[u] {
			gone = append(gone, u)
		}
	}
	if len(gone) > minRemoveGuard && float64(len(gone)) > maxRemoveShare*float64(len(have)) {
		rep.Failed = append(rep.Failed, fmt.Sprintf("%s: the sitemap leaves out %d of the %d pages indexed, which looks like a broken sitemap; none were removed",
			s.Host, len(gone), len(have)))
	} else {
		slices.Sort(gone)
		for _, u := range gone {
			w.remove(have[u].key, u)
			if err := w.flushIfFull(ctx); err != nil {
				return err
			}
		}
	}
	return w.finish(ctx)
}

// turn is which share of the rotation this sync checks: one per day.
func (c *Crawler) turn() uint32 {
	return uint32(c.now().UTC().Unix()/(24*60*60)) % Rotation
}

// share is which sync in the rotation checks a page.
func share(pageURL string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(pageURL))
	return h.Sum32() % Rotation
}

// isPagePath reports whether a path is a page; sitemaps also list PDFs.
func isPagePath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".htm", ".html":
		return true
	}
	return false
}

// writer batches a site's changes into its shards.
type writer struct {
	c    *Crawler
	s    site.Site
	rep  *Report
	tocs map[string][]*site.TocEntry // by guide root URL, fetched once per sync
	work map[index.Key][]func(*index.Tx) error
	n    int
	// dirty holds the shards whose pages changed, whose vocabulary is rebuilt.
	dirty map[index.Key]bool
}

func newWriter(c *Crawler, s site.Site, rep *Report) *writer {
	return &writer{c: c, s: s, rep: rep, tocs: map[string][]*site.TocEntry{}, work: map[index.Key][]func(*index.Tx) error{},
		dirty: map[index.Key]bool{}}
}

func (w *writer) add(k index.Key, fn func(*index.Tx) error) {
	w.work[k] = append(w.work[k], fn)
	w.n++
}

// fetch gets a sitemap entry's page, conditionally when the index has it, and queues
// what to write.
func (w *writer) fetch(ctx context.Context, e site.Entry, h known, have bool) error {
	req := site.Request{URL: e.URL}
	if have {
		req.ETag, req.LastModified = h.date.ETag, h.date.LastModified
	}
	w.rep.Checked++
	resp, err := w.c.Source.Fetch(ctx, req)
	switch {
	case errors.Is(err, site.ErrNotFound):
		if have {
			w.remove(h.key, e.URL)
		}
		w.skip(e)
		return w.flushIfFull(ctx)
	case err != nil:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.rep.Failed = append(w.rep.Failed, e.URL+": "+err.Error())
		return nil
	}
	if final := strings.SplitN(resp.URL, "#", 2)[0]; final != "" && final != e.URL {
		// The page moved. Its new address is indexed from its own sitemap entry.
		w.rep.Redirected++
		k, _ := index.KeyFor(e.URL)
		w.add(k, func(tx *index.Tx) error { return tx.PutRedirect(e.URL, final) })
		if have {
			w.remove(h.key, e.URL)
		}
		return w.flushIfFull(ctx)
	}
	if resp.NotModified {
		w.rep.Unchanged++
		d := index.PageDate{LastMod: e.LastMod, ETag: firstNonEmpty(resp.ETag, h.date.ETag), LastModified: firstNonEmpty(resp.LastModified, h.date.LastModified)}
		w.add(h.key, func(tx *index.Tx) error {
			_, err := tx.SetPageDates(e.URL, d)
			return err
		})
		return w.flushIfFull(ctx)
	}
	p, err := page.Parse(resp.Body, e.URL, nil)
	if errors.Is(err, page.ErrNotAPage) {
		if have {
			w.remove(h.key, e.URL)
		}
		w.skip(e)
		return w.flushIfFull(ctx)
	} else if err != nil {
		w.rep.Failed = append(w.rep.Failed, e.URL+": "+err.Error())
		return nil
	}
	g, _ := site.GuideOf(e.URL)
	toc := w.toc(ctx, g)
	d := index.NewDoc(p, w.s, e, site.TocTrail(toc, e.URL))
	d.ETag, d.LastModified = resp.ETag, resp.LastModified
	k := index.Key{Site: w.s.Code, Older: g.Version != ""}
	w.add(k, func(tx *index.Tx) error {
		if err := tx.ForgetSkipped(e.URL); err != nil {
			return err
		}
		changed, err := tx.Put(d)
		switch {
		case err != nil:
			return fmt.Errorf("%s: %w", e.URL, err)
		case !have:
			w.rep.Added++
			w.dirty[k] = true
		case changed:
			w.rep.Updated++
			w.dirty[k] = true
		default:
			w.rep.Unchanged++
		}
		return nil
	})
	return w.flushIfFull(ctx)
}

// toc returns a guide's table of contents, fetching it (and storing it) the first time
// a sync needs it.
func (w *writer) toc(ctx context.Context, g site.Guide) []*site.TocEntry {
	if g.RootURL == "" {
		return nil
	}
	if toc, ok := w.tocs[g.RootURL]; ok {
		return toc
	}
	toc, err := site.FetchToc(ctx, w.c.Source, g)
	if err != nil && !errors.Is(err, site.ErrNotFound) {
		w.rep.Failed = append(w.rep.Failed, g.TocURL()+": "+err.Error())
	}
	w.tocs[g.RootURL] = toc
	if toc != nil {
		k := index.Key{Site: w.s.Code, Older: g.Version != ""}
		w.add(k, func(tx *index.Tx) error { return tx.PutToc(g, toc) })
	}
	return toc
}

// skip records a sitemap entry that isn't a page.
func (w *writer) skip(e site.Entry) {
	if k, ok := index.KeyFor(e.URL); ok {
		w.add(k, func(tx *index.Tx) error { return tx.PutSkipped(e.URL, e.LastMod) })
	}
}

func (w *writer) remove(k index.Key, pageURL string) {
	w.add(k, func(tx *index.Tx) error {
		removed, err := tx.Remove(pageURL)
		if removed {
			w.rep.Removed++
			w.dirty[k] = true
		}
		return err
	})
}

func (w *writer) flushIfFull(ctx context.Context) error {
	if w.n < batchSize {
		return nil
	}
	return w.flush(ctx)
}

// flush writes what's queued, one transaction per shard.
func (w *writer) flush(ctx context.Context) error {
	for k, fns := range w.work {
		if len(fns) == 0 {
			continue
		}
		if err := w.ensureShard(ctx, k); err != nil {
			return err
		}
		err := w.c.Index.With(k, func(sh *index.Shard) error {
			return sh.Update(ctx, func(tx *index.Tx) error {
				for _, fn := range fns {
					if err := fn(tx); err != nil {
						return err
					}
				}
				return nil
			})
		})
		if err != nil {
			return err
		}
		w.work[k] = nil
	}
	w.n = 0
	return nil
}

// ensureShard creates an empty shard when a sync finds the first page for it, such as
// the first older version of a guide on a site that had none.
func (w *writer) ensureShard(ctx context.Context, k index.Key) error {
	if w.c.Index.Shard(k) != nil {
		return nil
	}
	b, err := w.c.Index.Build(k)
	if err != nil {
		return err
	}
	err = b.Update(ctx, func(tx *index.Tx) error {
		if err := tx.SetMeta("source", "live"); err != nil {
			return err
		}
		return tx.StartHistory()
	})
	if err != nil {
		w.c.Index.Discard(b)
		return err
	}
	return w.c.Index.Install(b)
}

// finish writes what's left and, when anything changed, rebuilds the shards' spelling
// vocabulary and records when they were synced.
func (w *writer) finish(ctx context.Context) error {
	if err := w.flush(ctx); err != nil {
		return err
	}
	at := w.c.now().UTC().Format(time.RFC3339)
	for _, k := range []index.Key{{Site: w.s.Code}, {Site: w.s.Code, Older: true}} {
		if w.c.Index.Shard(k) == nil {
			continue
		}
		err := w.c.Index.With(k, func(sh *index.Shard) error {
			if err := sh.Update(ctx, func(tx *index.Tx) error { return tx.SetMeta("synced_at", at) }); err != nil {
				return err
			}
			if w.dirty[k] {
				return sh.RebuildVocabulary(ctx)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
