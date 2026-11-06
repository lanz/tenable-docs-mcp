// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package site

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// Entry is one page in a sitemap.
type Entry struct {
	URL string
	// Products are the page's products, from the sitemap's Coveo metadata. A page can
	// belong to several.
	Products []string
	// GuideType is the kind of guide ("User Guide", "Release Notes", …).
	GuideType string
	// LastMod is the sitemap's last-modified date (YYYY-MM-DD). It can lag behind the
	// page itself, so updates rely on conditional requests instead.
	LastMod string
}

type urlset struct {
	URLs []struct {
		Loc     string `xml:"loc"`
		LastMod string `xml:"lastmod"`
		Meta    struct {
			Product   string `xml:"docproductname"`
			GuideType string `xml:"docguidetype"`
		} `xml:"metadata"`
	} `xml:"url"`
}

// ParseSitemap reads a sitemap's entries, in order.
func ParseSitemap(r io.Reader) ([]Entry, error) {
	var set urlset
	dec := xml.NewDecoder(r)
	dec.Strict = false
	if err := dec.Decode(&set); err != nil {
		return nil, fmt.Errorf("sitemap: %w", err)
	}
	entries := make([]Entry, 0, len(set.URLs))
	for _, u := range set.URLs {
		loc := strings.TrimSpace(u.Loc)
		if loc == "" {
			continue
		}
		entries = append(entries, Entry{
			URL:       loc,
			Products:  splitProducts(u.Meta.Product),
			GuideType: strings.TrimSpace(u.Meta.GuideType),
			LastMod:   strings.TrimSpace(u.LastMod),
		})
	}
	return entries, nil
}

// FetchSitemap gets and parses a site's sitemap.
func FetchSitemap(ctx context.Context, src Source, s Site) ([]Entry, error) {
	resp, err := src.Fetch(ctx, Request{URL: s.SitemapURL()})
	if err != nil {
		return nil, err
	}
	return ParseSitemap(bytes.NewReader(resp.Body))
}

// splitProducts splits "A;B; A" into ["A", "B"].
func splitProducts(v string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(v, ";") {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
