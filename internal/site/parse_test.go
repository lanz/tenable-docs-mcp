// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package site

import (
	"reflect"
	"strings"
	"testing"
)

func TestSites(t *testing.T) {
	if len(Sites) != 8 || Sites[0].Host != "docs.tenable.com" {
		t.Fatalf("Sites = %v", Sites)
	}
	for _, c := range []struct {
		host string
		code string
		ok   bool
	}{
		{"docs.tenable.com", "en", true},
		{"JP.Docs.Tenable.com", "jp", true},
		{"kr.docs.tenable.com:443", "kr", true},
		{"docs.tenable.com.example", "", false},
		{"www.tenable.com", "", false},
	} {
		s, ok := ByHost(c.host)
		if ok != c.ok || s.Code != c.code {
			t.Errorf("ByHost(%q) = %v %v", c.host, s, ok)
		}
	}
	if s, _ := ByCode("zh-tw"); s.Lang != "zh-Hant" {
		t.Errorf("zh-tw language = %q", s.Lang)
	}
	for raw, want := range map[string]bool{
		"https://docs.tenable.com/nessus/Content/A.htm":    true,
		"http://docs.tenable.com/nessus/Content/A.htm":     false,
		"https://docs.tenable.com.evil.example/x":          false,
		"https://user@fr.docs.tenable.com/x":               true,
		"javascript:alert(1)":                              false,
		"https://developer.tenable.com/reference/anything": false,
	} {
		if got := IsDocsURL(raw); got != want {
			t.Errorf("IsDocsURL(%q) = %v", raw, got)
		}
	}
}

// A made-up robots.txt with the same kinds of rules as the real sites.
const robotsFixture = `# comment
User-agent: *
Disallow: /preview
Disallow: /preview/*
Disallow: /*/Draft
Disallow: /*/Draft/*
Disallow: /old.htm$
Allow: /preview/public-notes.htm
Disallow:

User-agent: other-bot
Disallow: /

Sitemap: https://docs.example/Sitemap.xml
`

func TestRobots(t *testing.T) {
	rb, err := ParseRobots(strings.NewReader(robotsFixture), "tenable-docs-mcp")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"/":                                  true,
		"/guide/Content/Topic.htm":           true,
		"/preview":                           false,
		"/preview/":                          false,
		"/previewer/Content/A.htm":           false, // a prefix match, as robots.txt defines it
		"/preview/x/Content/A.htm":           false,
		"/preview/public-notes.htm":          true, // the longer Allow wins
		"/guide/Draft":                       false,
		"/guide/Content/Draft/A.htm":         false,
		"/guide/Drafting.htm":                false, // still a prefix match on "/guide/Draft"
		"/old.htm":                           false,
		"/old.htm?x=1":                       true, // $ anchors the end
		"/guide/old.htm":                     true,
		"/guide/Content/Classic/Topic.htm":   true,
		"/release-notes/Content/2026-11.htm": true,
	} {
		if got := rb.Allowed(path); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", path, got, want)
		}
	}
	other, _ := ParseRobots(strings.NewReader(robotsFixture), "other-bot")
	if other.Allowed("/guide/Content/Topic.htm") {
		t.Error("a group for the client's own token should apply instead of *")
	}
	var none *Robots
	if !AllowAll().Allowed("/anything") || !none.Allowed("/anything") {
		t.Error("AllowAll and nil rules should allow everything")
	}
}

const sitemapFixture = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:coveo="https://www.coveo.com/schemas/metadata">
	<url>
		<loc>https://docs.example/widget/Content/Start.htm</loc>
		<lastmod>2026-10-26</lastmod>
		<changefreq>weekly</changefreq>
		<coveo:metadata>
			<docproductname>Widget Manager; Widget Agent;Widget Manager</docproductname>
			<docguidetype>User Guide</docguidetype>
		</coveo:metadata>
	</url>
	<url>
		<loc>  https://docs.example/widget/2_1/Content/Start.htm  </loc>
	</url>
	<url><loc></loc></url>
</urlset>`

func TestParseSitemap(t *testing.T) {
	entries, err := ParseSitemap(strings.NewReader(sitemapFixture))
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{URL: "https://docs.example/widget/Content/Start.htm", Products: []string{"Widget Manager", "Widget Agent"}, GuideType: "User Guide", LastMod: "2026-10-26"},
		{URL: "https://docs.example/widget/2_1/Content/Start.htm"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %#v\nwant      %#v", entries, want)
	}
	if _, err := ParseSitemap(strings.NewReader("<urlset><url>")); err == nil {
		t.Error("a truncated sitemap should be an error")
	}
}

func TestGuideOf(t *testing.T) {
	for raw, want := range map[string]Guide{
		"https://docs.example/widget/Content/A/B.htm":       {RootURL: "https://docs.example/widget", Path: "widget"},
		"https://docs.example/widget/10_8/Content/B.htm":    {RootURL: "https://docs.example/widget/10_8", Path: "widget/10_8", Version: "10.8"},
		"https://docs.example/a/widget/6_4_1/content/x.htm": {RootURL: "https://docs.example/a/widget/6_4_1", Path: "a/widget/6_4_1", Version: "6.4.1"},
	} {
		got, ok := GuideOf(raw)
		if !ok || got != want {
			t.Errorf("GuideOf(%q) = %+v %v, want %+v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"https://docs.example/widget.htm", "https://docs.example/Content/x.htm", "::"} {
		if _, ok := GuideOf(raw); ok {
			t.Errorf("GuideOf(%q) should be false", raw)
		}
	}
	g, _ := GuideOf("https://docs.example/widget/Content/A.htm")
	if g.TocURL() != "https://docs.example/widget/Data/FullToc.xml" {
		t.Errorf("TocURL = %s", g.TocURL())
	}
}

const tocFixture = "\xef\xbb\xbf" + `<?xml version="1.0" encoding="utf-8"?>
<CatapultToc Version="1" DescendantCount="4">
    <TocEntry Title="Welcome" Link="/Content/Welcome.htm" DescendantCount="3">
        <TocEntry Title="Release Notes" Link="https://docs.example/release-notes/Content/widget.htm" DescendantCount="0" />
        <TocEntry Title=" Get Started " DescendantCount="1">
            <TocEntry Title="Requirements" Link="/Content/start/Requirements.htm#Ports" DescendantCount="0" />
        </TocEntry>
    </TocEntry>
</CatapultToc>`

func TestParseToc(t *testing.T) {
	g, _ := GuideOf("https://docs.example/widget/10_8/Content/Welcome.htm")
	toc, err := ParseToc(strings.NewReader(tocFixture), g)
	if err != nil {
		t.Fatal(err)
	}
	if len(toc) != 1 || toc[0].URL != "https://docs.example/widget/10_8/Content/Welcome.htm" {
		t.Fatalf("top = %+v", toc)
	}
	kids := toc[0].Children
	if len(kids) != 2 || kids[0].URL != "https://docs.example/release-notes/Content/widget.htm" {
		t.Fatalf("children = %+v", kids)
	}
	if kids[1].Title != "Get Started" || kids[1].URL != "" {
		t.Errorf("group entry = %+v", kids[1])
	}
	if got := kids[1].Children[0].URL; got != "https://docs.example/widget/10_8/Content/start/Requirements.htm#Ports" {
		t.Errorf("nested link = %s", got)
	}
	if _, err := ParseToc(strings.NewReader("<CatapultToc><TocEntry"), g); err == nil {
		t.Error("a truncated table of contents should be an error")
	}
}
