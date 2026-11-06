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
	for f, want := range map[Filter]int{
		{}:                                   4,
		{Product: "Nessus"}:                  3,
		{Product: "Security Center"}:         1,
		{GuideType: "Reference Guide"}:       1,
		{Guide: "nessus"}:                    2, // nessus and nessus/10_8, but not nessus_x
		{Guide: "nessus", Version: "10.8"}:   1,
		{Product: "Nessus", Version: "10.8"}: 1,
	} {
		if got := count(f); got != want {
			t.Errorf("%+v: %d candidates, want %d", f, got, want)
		}
	}
	cs, _ := s.Candidates(ctx, MatchQuery("8834"), Filter{}, 10)
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

func TestWords(t *testing.T) {
	if got := Words("Scan 10.8: Tenable.io's NESSUS-cli, 8834 и Größe"); !reflect.DeepEqual(got, []string{"scan", "tenable", "io", "s", "nessus", "cli", "и", "größe"}) {
		t.Errorf("Words = %q", got)
	}
	if got := likePrefix(`a_b%c\d`); got != `a\_b\%c\\d` {
		t.Errorf("likePrefix = %s", got)
	}
}
