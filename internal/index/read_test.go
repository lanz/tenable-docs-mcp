// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"reflect"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

func TestReading(t *testing.T) {
	s := openTest(t, Key{Site: "en"})
	en, _ := site.ByCode("en")
	add := func(u string, products []string, body string) {
		p, err := page.Parse([]byte(`<div id="mc-main-content">`+body+`</div>`), u, nil)
		if err != nil {
			t.Fatal(err)
		}
		put(t, s, NewDoc(p, en, site.Entry{URL: u, Products: products, GuideType: "User Guide", LastMod: "2026-10-26"}, []string{"Welcome", "Install"}))
	}
	const a, b = "https://docs.tenable.com/nessus/Content/A.htm", "https://docs.tenable.com/security-center/Content/A.htm"
	add(a, []string{"Nessus", "Tenable Agent"}, `<h1>Install</h1><p class="Permission">Required User Role: Administrator</p>
		<h2 id="x">Steps</h2><p>See <a href="B.htm">B</a>.</p>`)
	add(b, []string{"Security Center"}, `<h1>Install</h1><p>Elsewhere.</p>`)

	info, ok, err := s.PageInfo(ctx, a)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if info.Title != "Install" || info.Product != "Nessus" || !reflect.DeepEqual(info.Related, []string{"Tenable Agent"}) ||
		info.Guide != "nessus" || info.Site != "en" || info.CurrentURL != a || len(info.Roles) != 1 ||
		!reflect.DeepEqual(info.TocTrail, []string{"Welcome", "Install"}) || info.LastMod != "2026-10-26" || info.IndexedAt == "" {
		t.Errorf("info = %+v", info)
	}
	if _, ok, _ := s.PageInfo(ctx, "https://docs.tenable.com/missing.htm"); ok {
		t.Error("missing page found")
	}
	secs, err := s.Sections(ctx, a)
	if err != nil || len(secs) != 2 || secs[1].Anchor != "x" || secs[1].URL != a+"#x" || secs[1].Ord != 1 {
		t.Errorf("sections = %+v, %v", secs, err)
	}
	if links, err := s.LinksFrom(ctx, a); err != nil || !reflect.DeepEqual(links, []string{"https://docs.tenable.com/nessus/Content/B.htm"}) {
		t.Errorf("links = %q, %v", links, err)
	}
	refs, products, err := s.SameTitle(ctx, "install", a)
	if err != nil || len(refs) != 1 || refs[0].URL != b || products[0] != "Security Center" {
		t.Errorf("same title = %+v %q %v", refs, products, err)
	}
	guides, err := s.Guides(ctx)
	if err != nil || len(guides) != 2 || guides[0].Path != "nessus" || guides[0].Product != "Nessus" || guides[0].Pages != 1 || guides[0].GuideType != "User Guide" {
		t.Errorf("guides = %+v, %v", guides, err)
	}
	if last, err := s.LastIndexed(ctx); err != nil || last == "" {
		t.Errorf("last indexed = %q, %v", last, err)
	}

	g, _ := site.GuideOf(a)
	toc := []*site.TocEntry{{Title: "Welcome", URL: a, Children: []*site.TocEntry{{Title: "Child"}}}}
	if err := s.Update(ctx, func(tx *Tx) error { return tx.PutToc(g, toc) }); err != nil {
		t.Fatal(err)
	}
	got, root, ok, err := s.Toc(ctx, "nessus")
	if err != nil || !ok || root != "https://docs.tenable.com/nessus" || len(got) != 1 || got[0].Children[0].Title != "Child" {
		t.Errorf("toc = %+v %q %v %v", got, root, ok, err)
	}
	if _, _, ok, _ := s.Toc(ctx, "missing"); ok {
		t.Error("missing toc found")
	}
}

func TestKeyFor(t *testing.T) {
	for u, want := range map[string]Key{
		"https://docs.tenable.com/nessus/Content/A.htm":         {Site: "en"},
		"https://jp.docs.tenable.com/nessus/10_8/Content/A.htm": {Site: "jp", Older: true},
		"http://FR.docs.tenable.com/x.htm":                      {Site: "fr"},
	} {
		if got, ok := KeyFor(u); !ok || got != want {
			t.Errorf("KeyFor(%q) = %v %v", u, got, ok)
		}
	}
	if _, ok := KeyFor("https://example.com/x"); ok {
		t.Error("off-site key")
	}
}
