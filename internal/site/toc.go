// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package site

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

// Guide identifies the guide a page belongs to.
type Guide struct {
	// RootURL is the guide's root, the part of the page URL before /Content/
	// (for example https://docs.tenable.com/nessus/10_8).
	RootURL string
	// Path is the guide's path on its site (for example "nessus/10_8").
	Path string
	// Version is the product version the guide is for ("10.8"), or empty for the
	// current version.
	Version string
}

// TocURL returns the URL of the guide's table of contents.
func (g Guide) TocURL() string { return g.RootURL + "/Data/FullToc.xml" }

var versionSegment = regexp.MustCompile(`^\d+(?:_\d+)+$`)

// GuideOf returns the guide a page URL belongs to. Pages outside any guide (with no
// /Content/ in their path) return false.
func GuideOf(pageURL string) (Guide, bool) {
	u, err := url.Parse(pageURL)
	if err != nil || u.Host == "" {
		return Guide{}, false
	}
	i := strings.Index(strings.ToLower(u.Path), "/content/")
	if i <= 0 {
		return Guide{}, false
	}
	p := strings.Trim(u.Path[:i], "/")
	g := Guide{RootURL: u.Scheme + "://" + u.Host + "/" + p, Path: p}
	for _, seg := range strings.Split(p, "/") {
		if versionSegment.MatchString(seg) {
			g.Version = strings.ReplaceAll(seg, "_", ".")
		}
	}
	return g, true
}

// TocEntry is one entry in a guide's table of contents.
type TocEntry struct {
	Title string
	// URL is the entry's page, as an absolute URL (entries can also point into other
	// guides). Empty for an entry that only groups others.
	URL      string
	Children []*TocEntry
}

type xmlToc struct {
	Entries []xmlTocEntry `xml:"TocEntry"`
}

type xmlTocEntry struct {
	Title    string        `xml:"Title,attr"`
	Link     string        `xml:"Link,attr"`
	Children []xmlTocEntry `xml:"TocEntry"`
}

// ParseToc reads a guide's FullToc.xml. Links are resolved against the guide's root.
func ParseToc(r io.Reader, guide Guide) ([]*TocEntry, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // Flare writes a byte-order mark
	var doc xmlToc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("table of contents: %w", err)
	}
	base, err := url.Parse(guide.RootURL + "/")
	if err != nil {
		return nil, err
	}
	var convert func([]xmlTocEntry) []*TocEntry
	convert = func(in []xmlTocEntry) []*TocEntry {
		out := make([]*TocEntry, 0, len(in))
		for _, e := range in {
			out = append(out, &TocEntry{
				Title:    strings.TrimSpace(e.Title),
				URL:      resolveTocLink(base, e.Link),
				Children: convert(e.Children),
			})
		}
		return out
	}
	return convert(doc.Entries), nil
}

// resolveTocLink turns a FullToc link into an absolute URL. Links starting with / are
// relative to the guide's root, not the site's.
func resolveTocLink(base *url.URL, link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}
	ref, err := url.Parse(link)
	if err != nil {
		return ""
	}
	if ref.IsAbs() {
		return ref.String()
	}
	return base.ResolveReference(&url.URL{Path: strings.TrimPrefix(ref.Path, "/"), RawQuery: ref.RawQuery, Fragment: ref.Fragment}).String()
}

// FetchToc gets and parses a guide's table of contents: FullToc.xml, or Flare's
// JavaScript files for guides published without it.
func FetchToc(ctx context.Context, src Source, guide Guide) ([]*TocEntry, error) {
	resp, err := src.Fetch(ctx, Request{URL: guide.TocURL()})
	if errors.Is(err, ErrNotFound) {
		return FetchJSToc(ctx, src, guide)
	}
	if err != nil {
		return nil, err
	}
	return ParseToc(bytes.NewReader(resp.Body), guide)
}

// TocTrail returns the titles leading to a page in a table of contents, from the top
// level down to the page's own entry, ignoring any #fragment. It returns nil when the
// page isn't listed.
func TocTrail(toc []*TocEntry, pageURL string) []string {
	want := withoutFragment(pageURL)
	var walk func([]*TocEntry, []string) []string
	walk = func(entries []*TocEntry, trail []string) []string {
		for _, e := range entries {
			t := append(trail[:len(trail):len(trail)], e.Title)
			if e.URL != "" && strings.EqualFold(withoutFragment(e.URL), want) {
				return t
			}
			if found := walk(e.Children, t); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(toc, nil)
}

func withoutFragment(u string) string {
	if i := strings.IndexByte(u, '#'); i >= 0 {
		return u[:i]
	}
	return u
}

// CurrentURL returns the URL of the current version of a page from an older version's
// guide, by dropping the version from its path (…/nessus/10_8/Content/A.htm becomes
// …/nessus/Content/A.htm). Other URLs come back unchanged.
func CurrentURL(pageURL string) string {
	g, ok := GuideOf(pageURL)
	if !ok || g.Version == "" {
		return pageURL
	}
	segs := strings.Split(g.Path, "/")
	kept := segs[:0]
	for _, s := range segs {
		if !versionSegment.MatchString(s) {
			kept = append(kept, s)
		}
	}
	root := strings.TrimSuffix(g.RootURL, g.Path) + strings.Join(kept, "/")
	return root + pageURL[len(g.RootURL):]
}

// OnSite returns the URL of the same path on another documentation site. Translated
// pages are published at the same paths as the English ones, though not every page is
// translated.
func OnSite(pageURL string, s Site) string {
	u, err := url.Parse(pageURL)
	if err != nil || !IsDocsHost(u.Host) {
		return pageURL
	}
	u.Host = s.Host
	return u.String()
}
