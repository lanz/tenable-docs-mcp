// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package refresh updates pages in the index from the live documentation sites, with
// conditional requests so unchanged pages cost little. It updates pages the index
// already has, and removes those the sites no longer publish; new pages come with a
// full update of the index.
package refresh

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// MaxPages caps how many pages one refresh checks.
const MaxPages = 500

// Report says what a refresh did.
type Report struct {
	Checked   int `json:"checked"`
	Unchanged int `json:"unchanged"`
	Updated   int `json:"updated"`
	Removed   int `json:"removed"`
	// Skipped counts pages robots.txt doesn't allow, pages the index doesn't have, and
	// documents that aren't pages.
	Skipped int `json:"skipped"`
	// Failed lists pages that couldn't be fetched or stored, with why.
	Failed []string `json:"failed,omitempty"`
	// TocUpdated is set when a guide's table of contents was fetched again.
	TocUpdated bool `json:"toc_updated,omitempty"`
}

// Refresher refreshes pages from a source (the live sites).
type Refresher struct {
	Index  *index.Index
	Source site.Source
	// Agent is the robots.txt user-agent token to follow rules for.
	Agent string
	// Progress, when set, is called as each page is checked or skipped, with how many
	// of how many have been reached.
	Progress func(done, total int)

	robots map[string]*site.Robots
}

// Pages refreshes the given pages (fragments are ignored), at most MaxPages of them.
func (r *Refresher) Pages(ctx context.Context, urls []string) (Report, error) {
	var rep Report
	seen := map[string]bool{}
	byKey := map[index.Key][]string{}
	var keys []index.Key
	for _, raw := range urls {
		u, _, _ := strings.Cut(strings.TrimSpace(raw), "#")
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		k, ok := index.KeyFor(u)
		if !ok {
			return rep, fmt.Errorf("%s isn't on the docs sites", u)
		}
		if _, ok := byKey[k]; !ok {
			keys = append(keys, k)
		}
		byKey[k] = append(byKey[k], u)
	}
	if len(seen) > MaxPages {
		return rep, fmt.Errorf("%d pages is more than one refresh checks (%d)", len(seen), MaxPages)
	}
	done := 0
	tick := func(n int) {
		done += n
		if r.Progress != nil {
			r.Progress(done, len(seen))
		}
	}
	for _, k := range keys {
		if r.Index.Shard(k) == nil {
			rep.Skipped += len(byKey[k])
			tick(len(byKey[k]))
			continue
		}
		if err := r.refreshShard(ctx, k, byKey[k], &rep, tick); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// Guide refreshes a guide's table of contents and every page of it the index has.
func (r *Refresher) Guide(ctx context.Context, k index.Key, guidePath string) (Report, error) {
	if r.Index.Shard(k) == nil {
		return Report{}, fmt.Errorf("the index has no %s documentation", k)
	}
	var urls []string
	err := r.Index.With(k, func(s *index.Shard) error {
		var err error
		urls, err = s.GuidePages(ctx, guidePath)
		return err
	})
	if err != nil {
		return Report{}, err
	}
	if len(urls) == 0 {
		return Report{}, fmt.Errorf("the %s index has no pages in guide %q", k, guidePath)
	}
	if len(urls) > MaxPages {
		return Report{}, fmt.Errorf("guide %s has %d pages, more than one refresh checks (%d)", guidePath, len(urls), MaxPages)
	}
	rep, err := r.Pages(ctx, urls)
	if err != nil {
		return rep, err
	}
	s, _ := site.ByCode(k.Site)
	g, ok := site.GuideOf(s.BaseURL() + guidePath + "/Content/x.htm")
	if !ok {
		return rep, nil
	}
	toc, err := site.FetchToc(ctx, r.Source, g)
	switch {
	case errors.Is(err, site.ErrNotFound):
	case err != nil:
		rep.Failed = append(rep.Failed, g.TocURL()+": "+err.Error())
	default:
		err = r.Index.With(k, func(sh *index.Shard) error {
			return sh.Update(ctx, func(tx *index.Tx) error { return tx.PutToc(g, toc) })
		})
		if err != nil {
			return rep, err
		}
		rep.TocUpdated = true
	}
	return rep, nil
}

func (r *Refresher) refreshShard(ctx context.Context, k index.Key, urls []string, rep *Report, tick func(int)) error {
	s, _ := site.ByCode(k.Site)
	rules, err := r.rules(ctx, s)
	if err != nil {
		return err
	}
	type fetched struct {
		info  *index.PageInfo
		resp  *site.Response
		gone  bool
		moved string // where the page redirects to
	}
	// Read what the index has for each page, then fetch with no lock held: a fetch can
	// take minutes, and holding the shard meanwhile would stall a waiting swap, and so
	// every other tool call.
	infos := make([]*index.PageInfo, len(urls))
	err = r.Index.With(k, func(sh *index.Shard) error {
		for i, u := range urls {
			info, ok, err := sh.PageInfo(ctx, u)
			if err != nil {
				return err
			}
			if ok {
				infos[i] = info
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	var work []fetched
	for i, u := range urls {
		// Each page counts once it's checked, or skipped.
		tick(1)
		info := infos[i]
		if info == nil {
			rep.Skipped++
			continue
		}
		if pu, err := url.Parse(u); err != nil || !rules.Allowed(pu.EscapedPath()) {
			rep.Skipped++
			continue
		}
		rep.Checked++
		resp, err := r.Source.Fetch(ctx, site.Request{URL: u, ETag: info.ETag, LastModified: info.LastModified})
		switch {
		case errors.Is(err, site.ErrNotFound):
			work = append(work, fetched{info: info, gone: true})
		case err != nil:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			rep.Failed = append(rep.Failed, u+": "+err.Error())
		case resp.URL != "" && strings.SplitN(resp.URL, "#", 2)[0] != u:
			// The page moved. As in a crawl, the old address becomes a redirect, and
			// the new one is indexed from its own sitemap entry.
			work = append(work, fetched{info: info, moved: strings.SplitN(resp.URL, "#", 2)[0]})
		case resp.NotModified:
			rep.Unchanged++
		default:
			work = append(work, fetched{info: info, resp: resp})
		}
	}
	return r.Index.With(k, func(sh *index.Shard) error {
		return sh.Update(ctx, func(tx *index.Tx) error {
			for _, w := range work {
				if w.moved != "" {
					if err := tx.PutRedirect(w.info.URL, w.moved); err != nil {
						return err
					}
					if removed, err := tx.Remove(w.info.URL); err != nil {
						return err
					} else if removed {
						rep.Removed++
					}
					continue
				}
				if w.gone {
					if removed, err := tx.Remove(w.info.URL); err != nil {
						return err
					} else if removed {
						rep.Removed++
					}
					continue
				}
				p, err := page.Parse(w.resp.Body, w.info.URL, nil)
				if errors.Is(err, page.ErrNotAPage) {
					rep.Skipped++
					continue
				} else if err != nil {
					rep.Failed = append(rep.Failed, w.info.URL+": "+err.Error())
					continue
				}
				// The sitemap's details for the page, as stored when it was indexed.
				products := append([]string{w.info.Product}, w.info.Related...)
				if w.info.Product == "" {
					products = w.info.Related
				}
				entry := site.Entry{URL: w.info.URL, Products: products, GuideType: w.info.GuideType, LastMod: w.info.LastMod}
				d := index.NewDoc(p, s, entry, w.info.TocTrail)
				d.ETag, d.LastModified = w.resp.ETag, w.resp.LastModified
				changed, err := tx.Put(d)
				if err != nil {
					return err
				}
				if changed {
					rep.Updated++
				} else {
					rep.Unchanged++
				}
			}
			return nil
		})
	})
}

func (r *Refresher) rules(ctx context.Context, s site.Site) (*site.Robots, error) {
	if r.robots == nil {
		r.robots = map[string]*site.Robots{}
	}
	if rb, ok := r.robots[s.Code]; ok {
		return rb, nil
	}
	agent := r.Agent
	if agent == "" {
		agent = "tenable-docs-mcp"
	}
	rb, err := site.FetchRobots(ctx, r.Source, s, agent)
	if err != nil {
		return nil, fmt.Errorf("robots.txt for %s: %w", s.Host, err)
	}
	r.robots[s.Code] = rb
	return rb, nil
}
