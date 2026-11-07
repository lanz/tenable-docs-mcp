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

func TestCandidates(t *testing.T) {
	s := openTest(t, Key{Site: "en"})
	en, _ := site.ByCode("en")
	add := func(u, guideType string, products []string, body string) {
		p, err := page.Parse([]byte(`<div id="mc-main-content">`+body+`</div>`), u, nil)
		if err != nil {
			t.Fatal(err)
		}
		put(t, s, NewDoc(p, en, site.Entry{URL: u, Products: products, GuideType: guideType}, nil))
	}
	add("https://docs.tenable.com/nessus/Content/Ports.htm", "User Guide", []string{"Nessus"},
		`<h1>Ports</h1><p>Scanner ports.</p><table><thead><tr><th>Port</th><th>Use</th></tr></thead><tbody><tr><td>8834</td><td>Web</td></tr></tbody></table>`)
	add("https://docs.tenable.com/nessus/10_8/Content/Ports.htm", "User Guide", []string{"Nessus"}, `<h1>Ports</h1><p>Scanner ports, older.</p>`)
	add("https://docs.tenable.com/security-center/Content/Ports.htm", "Reference Guide", []string{"Security Center", "Nessus"}, `<h1>Ports</h1><p>Scanner ports too.</p>`)
	add("https://docs.tenable.com/nessus_x/Content/Ports.htm", "User Guide", []string{"Nessus"}, `<h1>Ports</h1><p>Scanner ports elsewhere.</p>`)

	count := func(f Filter) int {
		t.Helper()
		cs, err := s.Candidates(ctx, MatchQuery("scanner ports"), f, 10)
		if err != nil {
			t.Fatal(err)
		}
		return len(cs)
	}
	for _, c := range []struct {
		f    Filter
		want int
	}{
		{Filter{}, 4},
		{Filter{Product: "Nessus"}, 3},
		{Filter{Product: "Security Center"}, 1},
		{Filter{GuideType: "Reference Guide"}, 1},
		{Filter{Guide: "nessus"}, 2}, // nessus and nessus/10_8, but not nessus_x
		{Filter{Guide: "nessus", Version: "10.8"}, 1},
		{Filter{Product: "Nessus", Version: "10.8"}, 1},
		{Filter{Contains: []string{"OLDER"}}, 1},
		{Filter{Contains: []string{"too", "scanner"}}, 1},
	} {
		if got := count(c.f); got != c.want {
			t.Errorf("%+v: %d candidates, want %d", c.f, got, c.want)
		}
	}
	// Text alone, without a full-text query, scans the sections; a title match ranks first.
	cs, err := s.Candidates(ctx, "", Filter{Contains: []string{"ports"}}, 10)
	if err != nil || len(cs) != 4 || cs[0].Score >= 0 {
		t.Errorf("scan = %d, %v", len(cs), err)
	}
	cs, _ = s.Candidates(ctx, "", Filter{Contains: []string{"elsewhere"}, Product: "Nessus"}, 10)
	if len(cs) != 1 || cs[0].PageURL != "https://docs.tenable.com/nessus_x/Content/Ports.htm" {
		t.Errorf("filtered scan = %+v", cs)
	}

	// Pages and their versions.
	if r, ok, err := s.Page(ctx, "https://docs.tenable.com/nessus/10_8/Content/Ports.htm"); !ok || err != nil || r.Version != "10.8" || r.Title != "Ports" {
		t.Errorf("page = %+v %v %v", r, ok, err)
	}
	if _, ok, _ := s.Page(ctx, "https://docs.tenable.com/missing.htm"); ok {
		t.Error("a missing page was found")
	}
	vs, err := s.Versions(ctx, "https://docs.tenable.com/nessus/Content/Ports.htm")
	if err != nil || len(vs) != 1 || vs[0].Version != "10.8" {
		t.Errorf("versions = %+v, %v", vs, err)
	}
	if gv, err := s.GuideVersions(ctx, "nessus"); err != nil || !reflect.DeepEqual(gv, []string{"10.8"}) {
		t.Errorf("guide versions = %q, %v", gv, err)
	}
	cs, _ = s.Candidates(ctx, MatchQuery("8834"), Filter{}, 10)
	if len(cs) != 1 || cs[0].Requirements != 1 || cs[0].Product != "Nessus" || cs[0].Snippet == "" || cs[0].GuideType != "User Guide" {
		t.Errorf("candidate = %+v", cs)
	}
	cs, _ = s.Candidates(ctx, MatchQuery("too"), Filter{}, 10)
	if len(cs) != 1 || !reflect.DeepEqual(cs[0].Related, []string{"Nessus"}) {
		t.Errorf("related = %+v", cs)
	}
	if cs, err := s.Candidates(ctx, " ", Filter{}, 10); cs != nil || err != nil {
		t.Errorf("empty query = %v, %v", cs, err)
	}

	counts, err := s.ProductCounts(ctx)
	if err != nil || counts["Nessus"] != 3 || counts["Security Center"] != 1 {
		t.Errorf("product counts = %v, %v", counts, err)
	}
	if err := s.Optimize(ctx); err != nil {
		t.Fatal(err)
	}
	vocab, err := s.Vocabulary(ctx)
	if err != nil || vocab["scanner"] != 4 || vocab["elsewhere"] != 0 {
		t.Errorf("vocabulary: scanner %d, elsewhere %d, %v", vocab["scanner"], vocab["elsewhere"], err)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"10.12", "10.9", 1}, {"6.3", "6.3", 0}, {"6.3", "6.3.1", -1}, {"3.77", "3.93", -1}, {"", "1", -1}} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d", c.a, c.b, got)
		}
	}
	refs := []PageRef{{Version: "10.9"}, {Version: "10.12"}, {Version: "10.10"}, {Version: "10.5"}}
	sortVersions(refs)
	if refs[0].Version != "10.12" || refs[3].Version != "10.5" {
		t.Errorf("sorted = %+v", refs)
	}
}

func TestWords(t *testing.T) {
	if got := Words("Scan 10.8: Tenable.io's NESSUS-cli, 8834 и Größe"); !reflect.DeepEqual(got, []string{"scan", "tenable", "io", "s", "nessus", "cli", "и", "größe"}) {
		t.Errorf("Words = %q", got)
	}
	if got := likePrefix(`a_b%c\d`); got != `a\_b\%c\\d` {
		t.Errorf("likePrefix = %s", got)
	}
}
