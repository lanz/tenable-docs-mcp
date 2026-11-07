// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// KeyFor returns the shard a page belongs in: its site's, for current or older
// versions.
func KeyFor(pageURL string) (Key, bool) {
	host := strings.TrimPrefix(strings.TrimPrefix(pageURL, "https://"), "http://")
	host, _, _ = strings.Cut(host, "/")
	s, ok := site.ByHost(host)
	if !ok {
		return Key{}, false
	}
	g, _ := site.GuideOf(pageURL)
	return Key{Site: s.Code, Older: g.Version != ""}, true
}

// PageInfo is what the index knows about a page.
type PageInfo struct {
	URL            string
	Site           string
	Lang           string
	Title          string
	Description    string
	Product        string
	Related        []string
	Guide          string
	GuideType      string
	Version        string
	ProductVersion string
	CurrentURL     string
	TocTrail       []string
	Roles          []page.Requirement
	LastMod        string
	IndexedAt      string
	// ETag and LastModified are the validators from the last fetch, for conditional
	// requests.
	ETag         string
	LastModified string
}

// Section is one stored section of a page.
type Section struct {
	Ord      int
	Level    int
	Anchor   string
	URL      string
	Heading  string
	Location string
	Body     string
}

// PageInfo returns what the index knows about a page, or false if it doesn't have it.
func (s *Shard) PageInfo(ctx context.Context, pageURL string) (*PageInfo, bool, error) {
	p := &PageInfo{}
	var related, trail, roles string
	err := s.db.QueryRowContext(ctx, `SELECT url, site, lang, title, description, product, related, guide, guide_type,
		version, product_version, current_url, toc_trail, roles, lastmod, indexed_at, etag, last_modified
		FROM pages WHERE url = ?`, pageURL).
		Scan(&p.URL, &p.Site, &p.Lang, &p.Title, &p.Description, &p.Product, &related, &p.Guide, &p.GuideType,
			&p.Version, &p.ProductVersion, &p.CurrentURL, &trail, &roles, &p.LastMod, &p.IndexedAt, &p.ETag, &p.LastModified)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	_ = json.Unmarshal([]byte(related), &p.Related)
	_ = json.Unmarshal([]byte(trail), &p.TocTrail)
	_ = json.Unmarshal([]byte(roles), &p.Roles)
	return p, true, nil
}

// Sections returns a page's sections in order.
func (s *Shard) Sections(ctx context.Context, pageURL string) ([]Section, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.ord, s.level, s.anchor, s.url, s.heading, s.location, s.body
		FROM sections s JOIN pages p ON p.id = s.page_id WHERE p.url = ? ORDER BY s.ord`, pageURL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Section
	for rows.Next() {
		var x Section
		if err := rows.Scan(&x.Ord, &x.Level, &x.Anchor, &x.URL, &x.Heading, &x.Location, &x.Body); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Toc returns a guide's table of contents and its root URL, or false if the shard
// doesn't have it.
func (s *Shard) Toc(ctx context.Context, guidePath string) ([]*site.TocEntry, string, bool, error) {
	var root, entries string
	err := s.db.QueryRowContext(ctx, "SELECT root_url, entries FROM tocs WHERE guide = ?", guidePath).Scan(&root, &entries)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", false, nil
	} else if err != nil {
		return nil, "", false, err
	}
	var toc []*site.TocEntry
	if err := json.Unmarshal([]byte(entries), &toc); err != nil {
		return nil, "", false, err
	}
	return toc, root, true, nil
}

// LinksFrom returns the documentation pages a page links to.
func (s *Shard) LinksFrom(ctx context.Context, pageURL string) ([]string, error) {
	return s.strings(ctx, `SELECT l.to_url FROM links l JOIN pages p ON p.id = l.page_id WHERE p.url = ? ORDER BY l.to_url`, pageURL)
}

// SameTitle returns the other pages with a title, ignoring case, with their products.
func (s *Shard) SameTitle(ctx context.Context, title, except string) ([]PageRef, []string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT url, title, version, product FROM pages
		WHERE title = ? COLLATE NOCASE AND url <> ? ORDER BY product, url LIMIT 50`, title, except)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var refs []PageRef
	var products []string
	for rows.Next() {
		var r PageRef
		var product string
		if err := rows.Scan(&r.URL, &r.Title, &r.Version, &product); err != nil {
			return nil, nil, err
		}
		refs = append(refs, r)
		products = append(products, product)
	}
	return refs, products, rows.Err()
}

// GuideInfo summarizes a guide in a shard.
type GuideInfo struct {
	Path      string
	Product   string
	GuideType string
	Version   string
	Pages     int
}

// Guides lists the shard's guides, with each one's most common product and guide type.
func (s *Shard) Guides(ctx context.Context) ([]GuideInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT guide, version, count(*),
		(SELECT p2.product FROM pages p2 WHERE p2.guide = p.guide GROUP BY p2.product ORDER BY count(*) DESC LIMIT 1),
		(SELECT p3.guide_type FROM pages p3 WHERE p3.guide = p.guide GROUP BY p3.guide_type ORDER BY count(*) DESC LIMIT 1)
		FROM pages p WHERE guide <> '' GROUP BY guide, version ORDER BY guide`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GuideInfo
	for rows.Next() {
		var g GuideInfo
		if err := rows.Scan(&g.Path, &g.Version, &g.Pages, &g.Product, &g.GuideType); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GuidePages returns the URLs of a guide's pages, in order.
func (s *Shard) GuidePages(ctx context.Context, guidePath string) ([]string, error) {
	return s.strings(ctx, `SELECT url FROM pages WHERE guide = ? ORDER BY url`, guidePath)
}

// LastIndexed returns when the shard's most recently indexed page was indexed.
func (s *Shard) LastIndexed(ctx context.Context) (string, error) {
	var t sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT max(indexed_at) FROM pages").Scan(&t)
	return t.String, err
}

func (s *Shard) strings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
