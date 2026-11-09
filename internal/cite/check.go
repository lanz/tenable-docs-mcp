// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package cite

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/search"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// Statuses of a checked link.
const (
	// OK: the page is in the index (and its anchor is one of its headings).
	OK = "ok"
	// OlderVersion: the page is for an older version; Current is the current one.
	OlderVersion = "older_version"
	// Translation: the page is a translation; English is the current English page.
	Translation = "translation"
	// AnchorUnknown: the anchor isn't one of the page's headings. It may be on a
	// smaller element; a live check finds out.
	AnchorUnknown = "anchor_unknown"
	// AnchorMissing: the live page has no element with that anchor.
	AnchorMissing = "anchor_missing"
	// Moved: the live site redirects the link; Current is where it goes.
	Moved = "moved"
	// NotFound: the page isn't in the index (or, with a live check, on the site).
	NotFound = "not_found"
	// NotIndexed: the index has no documentation for the link's language or versions,
	// so it couldn't be checked.
	NotIndexed = "not_indexed"
)

// Problem reports whether a status means the link is broken.
func Problem(status string) bool {
	return status == NotFound || status == AnchorMissing
}

// Result is a checked link.
type Result struct {
	Found
	Status string
	// Title is the page's title, when it's known.
	Title string
	// Current is the current version of the page in its language, for an older version
	// or a moved page.
	Current string
	// English is the current English page (with the link's anchor), to cite instead of a
	// translation or an older version.
	English string
	// Suggestion is a page that may be what a broken link meant.
	Suggestion *Suggestion
	Note       string
}

// Suggestion is a page to link instead.
type Suggestion struct {
	Title string
	URL   string
}

// Checker checks links against an index, and optionally the live site.
type Checker struct {
	Index  *index.Index
	Engine *search.Engine
	// Live, if set, is used to check anchors that aren't headings, and pages the index
	// doesn't have.
	Live site.Source

	live *liveRun
}

// MaxLiveFetches is how many pages one Check fetches from the live site at most.
const MaxLiveFetches = 50

// liveRun is one Check's live fetches: each page is fetched once, and only so many.
type liveRun struct {
	pages map[string]liveResult
	left  int
}

type liveResult struct {
	resp *site.Response
	err  error
}

// fetchLive fetches a page from the live site, once per Check.
func (c *Checker) fetchLive(ctx context.Context, pageURL string) (*site.Response, error) {
	if r, ok := c.live.pages[pageURL]; ok {
		return r.resp, r.err
	}
	if c.live.left <= 0 {
		return nil, fmt.Errorf("not checked: this call reached its limit of %d live pages", MaxLiveFetches)
	}
	c.live.left--
	resp, err := c.Live.Fetch(ctx, site.Request{URL: pageURL})
	if ctx.Err() == nil {
		c.live.pages[pageURL] = liveResult{resp, err}
	}
	return resp, err
}

// Check checks each link.
func (c *Checker) Check(ctx context.Context, links []Found) ([]Result, error) {
	run := *c
	run.live = &liveRun{pages: map[string]liveResult{}, left: MaxLiveFetches}
	out := make([]Result, 0, len(links))
	for _, f := range links {
		r, err := run.check(ctx, f)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (c *Checker) check(ctx context.Context, f Found) (Result, error) {
	r := Result{Found: f}
	pageURL, anchor, _ := strings.Cut(f.URL, "#")
	k, ok := index.KeyFor(pageURL)
	if !ok {
		r.Status, r.Note = NotIndexed, "not on the docs sites"
		return r, nil
	}
	if c.Index.Shard(k) == nil {
		r.Status, r.Note = NotIndexed, fmt.Sprintf("the index has no %s documentation", k)
		return r, nil
	}
	info, sections, err := c.page(ctx, k, pageURL)
	if err != nil {
		return r, err
	}
	en, _ := site.ByCode("en")
	current := site.CurrentURL(pageURL)
	englishPage := site.OnSite(current, en)
	withAnchor := func(u string) string {
		if anchor == "" {
			return u
		}
		return u + "#" + anchor
	}
	if info == nil {
		r.Status = NotFound
		if c.Live != nil {
			if done, err := c.liveMissing(ctx, &r, pageURL); err != nil || done {
				return r, err
			}
		}
		// Suggest the current or English page when the link is to an older version or a
		// translation the index has; otherwise, a search on the page's file name.
		for _, alt := range []string{current, englishPage} {
			if alt == pageURL {
				continue
			}
			if ak, ok := index.KeyFor(alt); ok && c.Index.Shard(ak) != nil {
				if ai, _, err := c.page(ctx, ak, alt); err != nil {
					return r, err
				} else if ai != nil {
					r.Suggestion = &Suggestion{Title: ai.Title, URL: withAnchor(alt)}
					return r, nil
				}
			}
		}
		if s, err := c.suggest(ctx, pageURL, k.Site); err != nil {
			return r, err
		} else if s != nil {
			r.Suggestion = s
		}
		return r, nil
	}
	r.Title, r.Status = info.Title, OK
	if info.Version != "" {
		r.Status = OlderVersion
		if ck, ok := index.KeyFor(current); ok && c.Index.Shard(ck) != nil {
			if ci, _, err := c.page(ctx, ck, current); err != nil {
				return r, err
			} else if ci != nil {
				r.Current = withAnchor(current)
			}
		}
	} else if k.Site != "en" {
		r.Status = Translation
	}
	if englishPage != pageURL {
		if ek := (index.Key{Site: "en"}); c.Index.Shard(ek) != nil {
			if ei, _, err := c.page(ctx, ek, englishPage); err != nil {
				return r, err
			} else if ei != nil {
				r.English = withAnchor(englishPage)
			}
		}
	}
	if anchor != "" && !hasAnchor(sections, anchor) {
		r.Status, r.Note = AnchorUnknown, "the anchor isn't one of the page's headings"
		// Browsers match anchors exactly, so a heading's anchor in another case is
		// a broken link, with an easy fix.
		if fixed := anchorFold(sections, anchor); fixed != "" {
			r.Note = "the page's heading anchor is #" + fixed + "; anchors are case-sensitive"
			r.Suggestion = &Suggestion{Title: info.Title, URL: pageURL + "#" + fixed}
			return r, nil
		}
		if c.Live != nil {
			found, err := c.liveAnchor(ctx, pageURL, anchor)
			switch {
			case err != nil:
				r.Note += "; the live check failed: " + err.Error()
			case found:
				r.Status, r.Note = OK, "the anchor is on the page, but not on a heading"
			default:
				r.Status, r.Note = AnchorMissing, "the live page has no element with this anchor"
			}
		}
	}
	return r, nil
}

func (c *Checker) page(ctx context.Context, k index.Key, pageURL string) (*index.PageInfo, []index.Section, error) {
	var info *index.PageInfo
	var sections []index.Section
	err := c.Index.With(k, func(s *index.Shard) error {
		i, ok, err := s.PageInfo(ctx, pageURL)
		if err != nil || !ok {
			return err
		}
		info = i
		sections, err = s.Sections(ctx, pageURL)
		return err
	})
	return info, sections, err
}

// anchorFold returns a heading's anchor that matches anchor in another case, or "".
func anchorFold(sections []index.Section, anchor string) string {
	for _, s := range sections {
		if s.Anchor != "" && strings.EqualFold(s.Anchor, anchor) {
			return s.Anchor
		}
	}
	return ""
}

func hasAnchor(sections []index.Section, anchor string) bool {
	for _, s := range sections {
		if s.Anchor == anchor {
			return true
		}
	}
	return false
}

// liveAnchor reports whether the live page has an element with the anchor as its id or
// name.
func (c *Checker) liveAnchor(ctx context.Context, pageURL, anchor string) (bool, error) {
	resp, err := c.fetchLive(ctx, pageURL)
	if err != nil {
		return false, err
	}
	// The attribute's name in any case, its value exactly, as browsers match it.
	re := regexp.MustCompile(`(?i:\b(?:id|name))\s*=\s*["']` + regexp.QuoteMeta(anchor) + `["']`)
	return re.Match(resp.Body), nil
}

// liveMissing checks a page the index doesn't have on the live site. It returns true
// when that settles the link: the page exists (the index is behind) or redirects.
func (c *Checker) liveMissing(ctx context.Context, r *Result, pageURL string) (bool, error) {
	resp, err := c.fetchLive(ctx, pageURL)
	switch {
	case errors.Is(err, site.ErrNotFound):
		r.Note = "not on the live site either"
		return false, nil
	case err != nil:
		r.Note = "the live check failed: " + err.Error()
		return false, nil
	}
	if final, _, _ := strings.Cut(resp.URL, "#"); final != pageURL {
		r.Status, r.Current, r.Note = Moved, resp.URL, "the live site redirects this link"
		return true, nil
	}
	r.Status, r.Note = OK, "on the live site, but not in the index yet"
	return true, nil
}

// suggest searches for a page like a broken link's, by the words of its file name
// (InstallNessusLinux.htm is "Install Nessus Linux").
func (c *Checker) suggest(ctx context.Context, pageURL, lang string) (*Suggestion, error) {
	if c.Engine == nil {
		return nil, nil
	}
	words := fileWords(pageURL)
	if words == "" {
		return nil, nil
	}
	res, err := c.Engine.Search(ctx, search.Request{Query: words, Language: lang, Limit: 1})
	if err != nil || len(res.Hits) == 0 {
		return nil, err
	}
	h := res.Hits[0]
	return &Suggestion{Title: h.Title, URL: h.PageURL}, nil
}

// fileWords splits a page's file name into words: at hyphens, underscores, and changes
// from lower to upper case.
func fileWords(pageURL string) string {
	u, err := url.Parse(pageURL)
	if err != nil || strings.Trim(u.Path, "/") == "" {
		return ""
	}
	base := strings.TrimSuffix(path.Base(u.Path), path.Ext(u.Path))
	var b strings.Builder
	prev := rune(0)
	for _, r := range base {
		switch {
		case r == '-' || r == '_' || r == '.':
			b.WriteByte(' ')
		case unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)):
			b.WriteByte(' ')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
		prev = r
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
