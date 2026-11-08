// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package crawl

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// ErrNotPage is returned for a URL that robots.txt disallows, the site doesn't have,
// or that isn't a page.
var ErrNotPage = errors.New("not a page the docs sites publish")

// OnDemand adds single pages to the index as they're asked for: a page published since
// the index was last updated, say.
type OnDemand struct {
	Crawler *Crawler

	mu     sync.Mutex
	robots map[string]robotsAt
}

type robotsAt struct {
	rules *site.Robots
	at    time.Time
}

// robotsTTL is how long robots.txt rules are kept before they're fetched again.
const robotsTTL = time.Hour

// Page fetches a page from its live site and stores it in the index, following a
// redirect to where it moved. It returns the address the page is stored at.
func (o *OnDemand) Page(ctx context.Context, pageURL string) (string, error) {
	c := o.Crawler
	k, ok := index.KeyFor(pageURL)
	if !ok {
		return "", fmt.Errorf("%s: %w", pageURL, ErrNotPage)
	}
	s, _ := site.ByCode(k.Site)
	u, err := url.Parse(pageURL)
	if err != nil || !isPagePath(u.Path) {
		return "", fmt.Errorf("%s: %w", pageURL, ErrNotPage)
	}
	rules, err := o.rules(ctx, s)
	if err != nil {
		return "", err
	}
	if !rules.Allowed(u.EscapedPath()) {
		return "", fmt.Errorf("%s: %w", pageURL, ErrNotPage)
	}
	resp, err := c.Source.Fetch(ctx, site.Request{URL: pageURL})
	if errors.Is(err, site.ErrNotFound) {
		return "", fmt.Errorf("%s: %w", pageURL, ErrNotPage)
	} else if err != nil {
		return "", err
	}
	final := pageURL
	if resp.URL != "" && resp.URL != pageURL {
		// Store the page where it is now, and remember where it was.
		final = resp.URL
		if err := o.write(ctx, k, func(tx *index.Tx) error { return tx.PutRedirect(pageURL, final) }); err != nil {
			return "", err
		}
		fu, err := url.Parse(final)
		fk, ok := index.KeyFor(final)
		if err != nil || !ok || !isPagePath(fu.Path) || site.OnSite(final, s) == "" || !rules.Allowed(fu.EscapedPath()) {
			return "", fmt.Errorf("%s redirects to %s: %w", pageURL, final, ErrNotPage)
		}
		k = fk
	}
	p, err := page.Parse(resp.Body, final, nil)
	if errors.Is(err, page.ErrNotAPage) {
		return "", fmt.Errorf("%s: %w", final, ErrNotPage)
	} else if err != nil {
		return "", err
	}
	g, _ := site.GuideOf(final)
	var toc []*site.TocEntry
	if g.RootURL != "" {
		toc, _ = site.FetchToc(ctx, c.Source, g)
	}
	d := index.NewDoc(p, s, site.Entry{URL: final}, site.TocTrail(toc, final))
	d.ETag, d.LastModified = resp.ETag, resp.LastModified
	err = o.write(ctx, k, func(tx *index.Tx) error {
		if toc != nil {
			if err := tx.PutToc(g, toc); err != nil {
				return err
			}
		}
		_, err := tx.Put(d)
		return err
	})
	return final, err
}

func (o *OnDemand) write(ctx context.Context, k index.Key, fn func(*index.Tx) error) error {
	w := writer{c: o.Crawler}
	if err := w.ensureShard(ctx, k); err != nil {
		return err
	}
	return o.Crawler.Index.With(k, func(sh *index.Shard) error { return sh.Update(ctx, fn) })
}

func (o *OnDemand) rules(ctx context.Context, s site.Site) (*site.Robots, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if r, ok := o.robots[s.Code]; ok && time.Since(r.at) < robotsTTL {
		return r.rules, nil
	}
	rb, err := site.FetchRobots(ctx, o.Crawler.Source, s, o.Crawler.agent())
	if err != nil {
		return nil, fmt.Errorf("robots.txt for %s: %w", s.Host, err)
	}
	if o.robots == nil {
		o.robots = map[string]robotsAt{}
	}
	o.robots[s.Code] = robotsAt{rules: rb, at: time.Now()}
	return rb, nil
}
