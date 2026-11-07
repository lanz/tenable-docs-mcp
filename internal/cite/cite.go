// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cite checks the documentation links in a text: an AI assistant's answer, or a
// writer's draft topic in Markdown or HTML. It finds each link, checks the page and
// anchor against the index (and optionally the live site), maps older versions and
// translations to the current English page, and suggests pages for links that don't
// resolve.
package cite

import (
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// Found is a documentation link found in a text.
type Found struct {
	// URL is the absolute link, as written apart from resolving it against the base.
	URL string
	// Raw is the link as first written in the text.
	Raw string
	// Lines lists the lines it appears on, from 1.
	Lines []int
}

// Skipped counts the links that aren't checked.
type Skipped struct {
	// Other counts links to other sites.
	Other int `json:"other_sites"`
	// Relative counts relative links when there was no base URL to resolve them.
	Relative int `json:"relative_without_base"`
	// Files counts links to images and other files that aren't pages.
	Files int `json:"files"`
}

var (
	markdownLink = regexp.MustCompile(`\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	hrefAttr     = regexp.MustCompile(`(?i)\bhref\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	bareDocsURL  = regexp.MustCompile(`(?i)(?:https?://)?(?:[a-z-]+\.)?docs\.tenable\.com(?:/[^\s<>"'()\[\]{}|\\^` + "`" + `]*)?`)
)

// Extract finds the links in a text. Relative links resolve against base, the page's
// own published URL, when it's given; links to other sites, and to files that aren't
// pages, are counted but not returned.
func Extract(text, base string) ([]Found, Skipped) {
	var baseURL *url.URL
	if base != "" {
		if u, err := url.Parse(strings.TrimSpace(base)); err == nil && u.IsAbs() {
			baseURL = u
		}
	}
	lineStarts := []int{0}
	for i, c := range text {
		if c == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	lineOf := func(pos int) int { return sort.SearchInts(lineStarts, pos+1) }

	type hit struct {
		raw        string
		start, end int
	}
	var hits []hit
	taken := func(start, end int) bool {
		for _, h := range hits {
			if start < h.end && h.start < end {
				return true
			}
		}
		return false
	}
	for _, m := range markdownLink.FindAllStringSubmatchIndex(text, -1) {
		hits = append(hits, hit{text[m[2]:m[3]], m[2], m[3]})
	}
	for _, m := range hrefAttr.FindAllStringSubmatchIndex(text, -1) {
		g := 2
		if m[2] < 0 {
			g = 4
		}
		if !taken(m[g], m[g+1]) {
			hits = append(hits, hit{text[m[g]:m[g+1]], m[g], m[g+1]})
		}
	}
	for _, m := range bareDocsURL.FindAllStringIndex(text, -1) {
		raw := strings.TrimRight(text[m[0]:m[1]], ".,;:!?*_")
		if !taken(m[0], m[0]+len(raw)) {
			hits = append(hits, hit{raw, m[0], m[0] + len(raw)})
		}
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].start < hits[j].start })
	var skipped Skipped
	byURL := map[string]*Found{}
	var order []string
	for _, h := range hits {
		u, kind := resolve(strings.TrimSpace(h.raw), baseURL)
		switch kind {
		case linkOther:
			skipped.Other++
			continue
		case linkRelative:
			skipped.Relative++
			continue
		case linkFile:
			skipped.Files++
			continue
		case linkNone:
			continue
		}
		f := byURL[u]
		if f == nil {
			f = &Found{URL: u, Raw: strings.TrimSpace(h.raw)}
			byURL[u] = f
			order = append(order, u)
		}
		if l := lineOf(h.start); len(f.Lines) == 0 || f.Lines[len(f.Lines)-1] != l {
			f.Lines = append(f.Lines, l)
		}
	}
	out := make([]Found, 0, len(order))
	for _, u := range order {
		out = append(out, *byURL[u])
	}
	return out, skipped
}

type linkKind int

const (
	linkNone linkKind = iota
	linkDocs
	linkOther
	linkRelative
	linkFile
)

// schemeless matches a docs link written without https:// (docs.tenable.com/…).
var schemeless = regexp.MustCompile(`(?i)^(?:[a-z-]+\.)?docs\.tenable\.com(?:[/?#]|$)`)

// Extensions of pages; links to anything else with an extension are files.
var pageExts = map[string]bool{"": true, ".htm": true, ".html": true}

func resolve(raw string, base *url.URL) (string, linkKind) {
	if raw == "" || strings.HasPrefix(raw, "javascript:") || strings.HasPrefix(raw, "mailto:") || strings.HasPrefix(raw, "tel:") {
		return "", linkNone
	}
	if schemeless.MatchString(raw) {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", linkNone
	}
	if !u.IsAbs() {
		if base == nil {
			return "", linkRelative
		}
		u = base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", linkNone
	}
	if !site.IsDocsHost(u.Host) {
		return "", linkOther
	}
	if !pageExts[strings.ToLower(path.Ext(u.Path))] {
		return "", linkFile
	}
	u.Scheme, u.Host = "https", strings.ToLower(u.Hostname())
	if u.Fragment == "" {
		u.RawFragment = ""
	}
	return u.String(), linkDocs
}
