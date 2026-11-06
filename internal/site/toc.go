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

// FetchToc gets and parses a guide's table of contents.
func FetchToc(ctx context.Context, src Source, guide Guide) ([]*TocEntry, error) {
	resp, err := src.Fetch(ctx, Request{URL: guide.TocURL()})
	if err != nil {
		return nil, err
	}
	return ParseToc(bytes.NewReader(resp.Body), guide)
}
