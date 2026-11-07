// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

func TestSpecificQueries(t *testing.T) {
	s := openTest(t, Key{Site: "en"})
	en, _ := site.ByCode("en")
	add := func(u string, products []string, body string) {
		p, err := page.Parse([]byte(`<div id="mc-main-content">`+body+`</div>`), u, nil)
		if err != nil {
			t.Fatal(err)
		}
		put(t, s, NewDoc(p, en, site.Entry{URL: u, Products: products, GuideType: "User Guide"}, nil))
	}
	add("https://docs.tenable.com/nessus/Content/S.htm", []string{"Nessus"}, `<h1>Scan Settings</h1>
		<table><thead><tr><th>Setting</th><th>Default</th><th>Description</th></tr></thead><tbody>
		<tr><td>Max hosts</td><td>30</td><td>Hosts at once.</td></tr><tr><td>Timeout</td><td>5</td><td>Wait for max hosts.</td></tr></tbody></table>`)
	add("https://docs.tenable.com/nessus/Content/Ports.htm", []string{"Nessus"}, `<h1>Port Requirements</h1>
		<table><thead><tr><th>Port</th><th>Traffic</th></tr></thead><tbody><tr><td>TCP 8834</td><td>Web</td></tr><tr><td>TCP 443</td><td>Updates</td></tr></tbody></table>`)
	add("https://docs.tenable.com/release-notes/Content/nessus/2026.htm", []string{"Nessus"}, `<h1>Release Notes</h1>
		<h2>Nessus 10.12.6 (2026-10-22)</h2><p>Fixed a bug.</p><h2>October 2026</h2><p>A monthly entry.</p><h2>Nessus 10.11.1 (2026-01-15)</h2><p>Older.</p>`)
	add("https://docs.tenable.com/glossary/Content/glossary.htm", []string{"A", "B", "C", "D"}, `<h1>Glossary</h1>
		<table><thead><tr><th>Term</th><th>Definition</th></tr></thead><tbody>
		<tr><td>Vulnerability Priority Rating (VPR)</td><td>A score.</td></tr><tr><td>National Vulnerability Database</td><td>A list.</td></tr>
		<tr><td>Scan</td><td>A check.</td></tr><tr><td>Scan policy</td><td>Settings.</td></tr></tbody></table>`)

	set, err := s.Settings(ctx, MatchQuery("max hosts"), Filter{Product: "Nessus"}, 10)
	if err != nil || len(set) != 2 || set[0].Name != "Max hosts" || set[0].Default != "30" || set[0].Title != "Scan Settings" {
		t.Errorf("settings = %+v, %v", set, err)
	}
	if set, _ := s.Settings(ctx, MatchQuery("max hosts"), Filter{Product: "Security Center"}, 10); len(set) != 0 {
		t.Errorf("filtered settings = %+v", set)
	}
	if set, _ := s.Settings(ctx, " ", Filter{}, 10); set != nil {
		t.Error("empty query")
	}

	reqs, err := s.Requirements(ctx, RequirementQuery{Filter: Filter{Product: "Nessus"}, Kind: "ports"})
	if err != nil || len(reqs) != 2 || reqs[0].Fields[0].Text != "TCP 8834" || reqs[0].Title != "Port Requirements" {
		t.Errorf("requirements = %+v, %v", reqs, err)
	}
	if reqs, _ := s.Requirements(ctx, RequirementQuery{Words: []string{"443", "updates"}}); len(reqs) != 1 {
		t.Errorf("words = %+v", reqs)
	}
	if reqs, _ := s.Requirements(ctx, RequirementQuery{Kind: "hardware"}); len(reqs) != 0 {
		t.Errorf("kind = %+v", reqs)
	}

	for _, c := range []struct {
		q    ReleaseQuery
		want int
	}{
		{ReleaseQuery{}, 3},
		{ReleaseQuery{Product: "Nessus", Since: "2026-10"}, 2},
		{ReleaseQuery{Since: "2026-10-23"}, 1}, // the month entry counts as October
		{ReleaseQuery{Until: "2026-09"}, 1},
		{ReleaseQuery{Version: "10.12"}, 1},
		{ReleaseQuery{Version: "10.1"}, 0},
		{ReleaseQuery{Words: []string{"monthly"}}, 1},
		{ReleaseQuery{Limit: 1}, 1},
	} {
		got, err := s.Releases(ctx, c.q)
		if err != nil || len(got) != c.want {
			t.Errorf("%+v: %d entries, %v", c.q, len(got), err)
		}
	}
	got, _ := s.Releases(ctx, ReleaseQuery{})
	if got[0].Date != "2026-10-22" || got[2].Version != "10.11.1" {
		t.Errorf("order = %+v", got)
	}

	for term, want := range map[string]string{
		"vpr":                             "Vulnerability Priority Rating (VPR)",
		"VPR":                             "Vulnerability Priority Rating (VPR)",
		"NVD":                             "National Vulnerability Database",
		"scan":                            "Scan",
		"national vulnerability database": "National Vulnerability Database",
	} {
		terms, err := s.Terms(ctx, term, 5)
		if err != nil || len(terms) == 0 || terms[0].Term != want {
			t.Errorf("Terms(%q) = %+v, %v", term, terms, err)
		}
	}
	if terms, _ := s.Terms(ctx, "scan", 5); len(terms) != 2 || terms[1].Term != "Scan policy" {
		t.Errorf("prefix = %+v", terms)
	}
	if terms, _ := s.Terms(ctx, "XYZ", 5); len(terms) != 0 {
		t.Errorf("unknown abbreviation = %+v", terms)
	}
	if terms, _ := s.Terms(ctx, " ", 5); terms != nil {
		t.Error("empty term")
	}

	if err := s.Update(ctx, func(tx *Tx) error { return tx.PutSynonyms("scan", "assessment") }); err != nil {
		t.Fatal(err)
	}
	if syn, err := s.SynonymsOf(ctx, "Scan"); err != nil || len(syn) != 1 || syn[0] != "assessment" {
		t.Errorf("synonyms = %q, %v", syn, err)
	}
	if syn, _ := s.SynonymsOf(ctx, "assessment"); len(syn) != 1 || syn[0] != "scan" {
		t.Errorf("reverse synonyms = %q", syn)
	}
}

func TestInitials(t *testing.T) {
	for term, want := range map[string]string{
		"National Vulnerability Database":     "NVD",
		"Vulnerability Priority Rating (VPR)": "VPR",
		"Center for Internet Security":        "CIS",
		"role-based access control":           "RBAC",
	} {
		if got := initials(term); got != want {
			t.Errorf("initials(%q) = %q", term, got)
		}
	}
	for s, want := range map[string]bool{"NVD": true, "VPR": true, "nvd": false, "A": false, "TOOLONGX": false, "N1": false} {
		if isAbbreviation(s) != want {
			t.Errorf("isAbbreviation(%q)", s)
		}
	}
}
