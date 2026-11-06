// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

var ctx = context.Background()

func TestParse(t *testing.T) {
	for q, want := range map[string]Parsed{
		"How do I install Nessus on Linux?":         {Terms: []string{"install", "linux"}, Products: []string{"Nessus"}, Intent: HowTo, Install: true},
		"Tenable.sc hardware requirements":          {Terms: []string{"hardware", "requirements"}, Products: []string{"Security Center"}, Intent: Requirements},
		"What's new in Tenable.io?":                 {Products: []string{"Vulnerability Management"}, Intent: WhatsNew},
		`agent "scan window" in tenable io`:         {Terms: []string{"agent"}, Phrases: []string{"scan window"}, Products: []string{"Vulnerability Management"}},
		"nessus network monitor vs nessus":          {Products: []string{"Network Monitor", "Nessus"}},
		"WAS scan was slow":                         {Terms: []string{"scan", "slow"}, Products: []string{"Web App Scanning"}},
		"Tenable Core + Nessus on VMware":           {Terms: []string{"vmware"}, Products: []string{"Tenable Core", "Nessus"}},
		"tenable one vulnerability management tags": {Terms: []string{"tags"}, Products: []string{"Vulnerability Management"}},
		`"Tenable.ad" alerts`:                       {Terms: []string{"alerts"}, Phrases: []string{"Tenable.ad"}, Products: []string{"Identity Exposure"}},
		"lumin cyber exposure score":                {Terms: []string{"cyber", "exposure", "score"}, Products: []string{"Lumin Exposure View"}},
	} {
		got := Parse(q)
		want.Lang = "en"
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Parse(%q) =\n%+v\nwant\n%+v", q, got, want)
		}
	}
	for name, want := range map[string]string{"tenable.sc": "Security Center", "Tenable.OT": "OT Exposure", "Nessus": "Nessus",
		"Tenable Core": "Tenable Core", "tenable.cs": "Cloud Exposure", "Container Security": "Container Security"} {
		if got, ok := ProductNamed(name); !ok || got != want {
			t.Errorf("ProductNamed(%q) = %q %v", name, got, ok)
		}
	}
	if _, ok := ProductNamed("nessus linux"); ok {
		t.Error("a product name plus other words isn't a product name")
	}
}

func TestQuoting(t *testing.T) {
	if got := match([]string{`a"b`, "c"}, []string{"x y"}); got != `"x y" "a""b" "c"` {
		t.Errorf("match = %s", got)
	}
	if got := matchAny([]string{"a", "b"}, []string{"p"}); got != `"p" AND ("a" OR "b")` {
		t.Errorf("matchAny = %s", got)
	}
}

// fixture builds a shard from made-up pages: path, products, guide type, and body.
type fixturePage struct {
	path, guideType, body string
	products              []string
}

func newSearcher(t testing.TB, pages []fixturePage) *Searcher {
	t.Helper()
	sh, err := index.OpenShard(filepath.Join(t.TempDir(), "en-current.db"), index.Key{Site: "en"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sh.Close() })
	en, _ := site.ByCode("en")
	err = sh.Update(ctx, func(tx *index.Tx) error {
		for _, fp := range pages {
			u := "https://docs.tenable.com/" + fp.path
			p, err := page.Parse([]byte(`<div id="mc-main-content">`+fp.body+`</div>`), u, nil)
			if err != nil {
				return err
			}
			if _, err := tx.Put(index.NewDoc(p, en, site.Entry{URL: u, Products: fp.products, GuideType: fp.guideType}, nil)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sh.Optimize(ctx); err != nil {
		t.Fatal(err)
	}
	return New(sh)
}

var fixture = []fixturePage{
	{"nessus/Content/InstallLinux.htm", "User Guide", `<h1>Install Nessus on Linux</h1><p>Install the package and start the scanner service.</p>`, []string{"Nessus"}},
	{"security-center/Content/InstallLinux.htm", "User Guide", `<h1>Install Nessus on Linux</h1><p>Install a scanner for Security Center to manage.</p>`, []string{"Security Center"}},
	{"nessus/Content/Ports.htm", "User Guide", `<h1>Port Requirements</h1><p>The scanner needs these ports.</p>
		<table><thead><tr><th>Port</th><th>Traffic</th></tr></thead><tbody><tr><td>8834</td><td>Web interface of the scanner</td></tr></tbody></table>`, []string{"Nessus"}},
	{"nessus/Content/Scanner.htm", "User Guide", `<h1>About the Scanner</h1><p>The scanner runs scans. The scanner uses ports.</p>`, []string{"Nessus"}},
	{"release-notes/Content/nessus/2026.htm", "Release Notes", `<h1>Nessus 2026 Release Notes</h1><h2>Nessus 10.12.6 (2026-10-22)</h2><p>The scanner adds new scan templates.</p>`, []string{"Nessus"}},
	{"nessus/Content/Templates.htm", "User Guide", `<h1>Scan Templates</h1><p>Use scan templates to create scans with the scanner.</p>`, []string{"Nessus"}},
	{"vulnerability-management/Content/Keys.htm", "User Guide", `<h1>Generate API Keys</h1><p>Generate API keys for your user account.</p>`, []string{"Vulnerability Management"}},
	{"tenable-core/Nessus/Content/Deploy.htm", "User Guide", `<h1>Deploy Tenable Core in VMware</h1><p>Deploy the appliance image in VMware.</p>`, []string{"Tenable Core", "Nessus"}},
	{"nessus/Content/Long.htm", "User Guide", `<h1>Scanner Settings</h1>
		<h2 id="a">Scanner one</h2><p>Scanner settings, part one.</p>
		<h2 id="b">Scanner two</h2><p>Scanner settings, part two.</p>
		<h2 id="c">Scanner three</h2><p>Scanner settings, part three.</p>`, []string{"Nessus"}},
	{"glossary/Content/glossary.htm", "Reference Guide", `<h1>Glossary</h1><p>Credentials are logins a scanner uses. Store credentials safely.</p>`, []string{"A", "B", "C", "D"}},
}

func urls(r *Result) []string {
	var out []string
	for _, h := range r.Hits {
		out = append(out, strings.TrimPrefix(h.URL, "https://docs.tenable.com/"))
	}
	return out
}

func TestRanking(t *testing.T) {
	s := newSearcher(t, fixture)
	run := func(req Request) *Result {
		t.Helper()
		r, err := s.Search(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// A product the question names wins, and same-titled pages in other products are
	// grouped under it.
	r := run(Request{Query: "install nessus on linux"})
	if len(r.Hits) == 0 || r.Hits[0].PageURL != "https://docs.tenable.com/nessus/Content/InstallLinux.htm" {
		t.Fatalf("hits = %q", urls(r))
	}
	if len(r.Hits[0].AlsoIn) != 1 || r.Hits[0].AlsoIn[0].Product != "Security Center" {
		t.Errorf("also in = %+v", r.Hits[0].AlsoIn)
	}
	// A retired product name finds the current product's page.
	r = run(Request{Query: "install scanner on linux in tenable.sc"})
	if len(r.Hits) == 0 || r.Hits[0].Product != "Security Center" {
		t.Errorf("tenable.sc hits = %q", urls(r))
	}
	// A product filter is strict, and takes retired names.
	r = run(Request{Query: "install linux", Product: "Tenable.sc"})
	if len(r.Hits) != 1 || r.Hits[0].Product != "Security Center" || r.Product != "Security Center" {
		t.Errorf("filtered = %q", urls(r))
	}
	if _, err := s.Search(ctx, Request{Query: "x", Product: "Nope"}); err == nil {
		t.Error("an unknown product filter should be an error")
	}
	// Requirements questions prefer pages with requirements tables.
	r = run(Request{Query: "which ports does the scanner use"})
	if len(r.Hits) == 0 || !strings.HasSuffix(r.Hits[0].PageURL, "Ports.htm") {
		t.Errorf("ports = %q", urls(r))
	}
	// "What's new" prefers release notes; how-to questions prefer user guides.
	r = run(Request{Query: "what's new with scan templates"})
	if len(r.Hits) == 0 || !strings.Contains(r.Hits[0].URL, "release-notes") {
		t.Errorf("what's new = %q", urls(r))
	}
	r = run(Request{Query: "how do I create scans with scan templates"})
	if len(r.Hits) == 0 || !strings.HasSuffix(r.Hits[0].PageURL, "Templates.htm") {
		t.Errorf("how-to = %q", urls(r))
	}
	// Tenable Core is its own product.
	r = run(Request{Query: "deploy vmware", Product: "Tenable Core"})
	if len(r.Hits) != 1 || r.Hits[0].Product != "Tenable Core" {
		t.Errorf("Tenable Core = %q", urls(r))
	}
	// At most two sections a page.
	r = run(Request{Query: "scanner settings", Limit: 10})
	n := 0
	for _, h := range r.Hits {
		if strings.HasSuffix(h.PageURL, "Long.htm") {
			n++
		}
	}
	if n != maxPerPage {
		t.Errorf("%d sections from one page: %q", n, urls(r))
	}
	// Snippets mark the matched words.
	if r.Hits[0].Snippet == "" || !strings.Contains(r.Hits[0].Snippet, "«") {
		t.Errorf("snippet = %q", r.Hits[0].Snippet)
	}
	// A question naming only a product searches for the product.
	r = run(Request{Query: "Tenable Core"})
	if len(r.Hits) == 0 || r.Hits[0].Product != "Tenable Core" {
		t.Errorf("product only = %q", urls(r))
	}
	// FTS syntax in a question is read as text.
	for _, q := range []string{`scanner AND NOT "`, `scan* OR (`, `NEAR(scanner ports)`, `-scanner ^ports`} {
		if _, err := s.Search(ctx, Request{Query: q}); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
}

func TestRelaxedAndNoResults(t *testing.T) {
	s := newSearcher(t, fixture)
	// No section has every word, so the results have most of them.
	r, err := s.Search(ctx, Request{Query: "scanner ports zebra"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Relaxed || len(r.Hits) == 0 {
		t.Errorf("relaxed = %v, %q", r.Relaxed, urls(r))
	}
	// A misspelling gets a suggestion.
	r, _ = s.Search(ctx, Request{Query: "scannr tempaltes"})
	if len(r.Hits) != 0 && !r.Relaxed {
		t.Fatalf("hits = %q", urls(r))
	}
	r, _ = s.Search(ctx, Request{Query: "xylophone credentails"})
	if len(r.Hits) != 0 {
		t.Fatalf("hits = %q", urls(r))
	}
	if len(r.Suggestions) != 1 || r.Suggestions[0] != "xylophone credentials" || !strings.Contains(r.NoResultsShown, `Did you mean "xylophone credentials"?`) {
		t.Errorf("suggestions = %q, %q", r.Suggestions, r.NoResultsShown)
	}
	// With a product filter, the products that do have matches.
	r, _ = s.Search(ctx, Request{Query: "api keys", Product: "Nessus"})
	if len(r.Hits) != 0 || !reflect.DeepEqual(r.OtherProducts, []string{"Vulnerability Management"}) ||
		!strings.Contains(r.NoResultsShown, "No documentation found for \"api keys\" in Nessus.") {
		t.Errorf("other products = %q, %q", r.OtherProducts, r.NoResultsShown)
	}
	// Only filler words.
	r, _ = s.Search(ctx, Request{Query: "how do I do it?"})
	if len(r.Hits) != 0 || r.NoResultsShown == "" {
		t.Errorf("filler = %+v", r)
	}
}

// FuzzSearch checks that no question makes a search fail: FTS5 syntax, quotes, and
// any other text are read as words.
func FuzzSearch(f *testing.F) {
	for _, q := range []string{`scanner AND "`, `"unclosed`, `NEAR(a b)`, `*`, `col:value`, `a OR b NOT c`,
		`“smart quotes”`, `tenable.io's "agent" ^x -y +z`, "\x00", `""`, `'`, `{a b}`} {
		f.Add(q)
	}
	s := newSearcher(f, fixture)
	f.Fuzz(func(t *testing.T, q string) {
		if _, err := s.Search(ctx, Request{Query: q, Limit: 3}); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	})
}

func TestEditDistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"credentials", "credentails", 1}, {"scanner", "scannr", 1}, {"abc", "abc", 0},
		{"kitten", "sitting", 3}, {"", "abc", 3}, {"ab", "ba", 1},
	} {
		if got := editDistance([]rune(c.a), []rune(c.b), 10); got != c.want {
			t.Errorf("editDistance(%q, %q) = %d", c.a, c.b, got)
		}
	}
	if got := editDistance([]rune("kitten"), []rune("sitting"), 1); got != 2 {
		t.Errorf("early exit = %d", got)
	}
	vocab := map[string]int{"credentials": 50, "credential": 5, "scanner": 9}
	if got := closest("credentails", vocab); got != "credentials" {
		t.Errorf("closest = %q", got)
	}
	if got := closest("zzzzzz", vocab); got != "" {
		t.Errorf("closest = %q", got)
	}
}
