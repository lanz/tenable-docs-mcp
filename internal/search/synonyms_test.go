// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestShippedSynonymsParse(t *testing.T) {
	s := DefaultSynonyms()
	if len(s.groups) < 10 {
		t.Errorf("%d groups", len(s.groups))
	}
	if got := s.Of("Whitelist"); !slices.Equal(got, []string{"allowlist", "allow list"}) {
		t.Errorf("Of(Whitelist) = %q", got)
	}
	if got := s.Of("allow list"); !slices.Equal(got, []string{"whitelist", "allowlist"}) {
		t.Errorf("Of(allow list) = %q", got)
	}
}

func TestParseSynonyms(t *testing.T) {
	s, err := ParseSynonyms(strings.NewReader("# a comment\n\n  Foo Bar ,  baz,foo  bar\nqux, quux\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.groups) != 2 || !slices.Equal(s.groups[0], []string{"foo bar", "baz"}) {
		t.Errorf("groups = %q", s.groups)
	}
	for _, bad := range []string{"lonely\n", "same, Same\n", ", ,\n"} {
		if _, err := ParseSynonyms(strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestAlternatives(t *testing.T) {
	s := DefaultSynonyms()
	queries := func(q string) []string {
		var out []string
		for _, a := range s.Alternatives(q) {
			out = append(out, a.Query)
		}
		return out
	}
	if got := queries("Nessus WHITELIST files"); !slices.Equal(got, []string{"Nessus allowlist files", "Nessus allow list files"}) {
		t.Errorf("whitelist: %q", got)
	}
	if got := queries("nessus paranoid mode"); !slices.Equal(got, []string{"nessus show potential false alarms"}) {
		t.Errorf("phrase: %q", got)
	}
	// Whole words and phrases only.
	for _, q := range []string{"sslv3 ciphers", "aes encryption", "what was scanned", "was scanning slow", "eolian"} {
		if got := queries(q); len(got) != 0 {
			t.Errorf("%q: %q", q, got)
		}
	}
	// "was scanner" is its own phrase, not "was scan" with "ner" left over.
	if got := queries("was scanner offline"); !slices.Equal(got, []string{"web app scanning scanner offline"}) {
		t.Errorf("was scanner: %q", got)
	}
	// A question with many synonyms gets at most maxAlternatives rewordings.
	if got := queries("whitelist blacklist ssl eol tcpdump activation key"); len(got) != maxAlternatives {
		t.Errorf("%d alternatives", len(got))
	}
	var none *Synonyms
	if got := none.Alternatives("whitelist"); got != nil {
		t.Errorf("nil synonyms: %v", got)
	}
}

func TestLoadSynonyms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "synonyms.txt")

	// No file: the shipped synonyms.
	s, err := LoadSynonyms(path)
	if err != nil || len(s.Of("whitelist")) == 0 {
		t.Fatalf("missing file: %v", err)
	}

	// The user's groups are added to the shipped ones.
	if err := os.WriteFile(path, []byte("# mine\nvuln, vulnerability\nwhitelist, safe list\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = LoadSynonyms(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Of("vuln"); !slices.Equal(got, []string{"vulnerability"}) {
		t.Errorf("user group: %q", got)
	}
	if got := s.Of("whitelist"); !slices.Equal(got, []string{"allowlist", "allow list", "safe list"}) {
		t.Errorf("merged groups: %q", got)
	}

	// A broken file is reported, and the shipped synonyms still work.
	if err := os.WriteFile(path, []byte("ok, fine\nlonely\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = LoadSynonyms(path)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("err = %v", err)
	}
	if s == nil || len(s.Of("whitelist")) == 0 || len(s.Of("ok")) != 0 {
		t.Error("a broken file should leave just the shipped synonyms")
	}
}

func TestEngineSearchesWithSynonyms(t *testing.T) {
	e := newEngine(t, []langPage{
		{key: enCur, path: "nessus/Content/Allowlist.htm", title: "File and Process Allow List", products: []string{"Nessus"},
			body: `<p>Add files and processes to the allow list so scans skip them.</p>`},
		{key: enCur, path: "nessus/Content/Capture.htm", title: "Advanced Debugging - Packet Capture", products: []string{"Nessus"},
			body: `<p>Capture network packets during a scan to debug it.</p>`},
		{key: enCur, path: "nessus/Content/Scan.htm", title: "Scan Settings", products: []string{"Nessus"},
			body: `<p>Configure scan settings, including the whitelist of approved ports.</p>`},
	})

	// The question's own word finds one page; the docs' wording finds the right one.
	res, err := e.Search(ctx, Request{Query: "nessus whitelist"})
	if err != nil {
		t.Fatal(err)
	}
	var urls []string
	for _, h := range res.Hits {
		urls = append(urls, strings.TrimPrefix(h.PageURL, "https://docs.tenable.com/"))
	}
	if !slices.Contains(urls, "nessus/Content/Allowlist.htm") || !slices.Contains(urls, "nessus/Content/Scan.htm") {
		t.Errorf("hits = %q", urls)
	}
	if !slices.ContainsFunc(res.Notes, func(n string) bool { return strings.Contains(n, `"allow list" for "whitelist"`) }) {
		t.Errorf("notes = %q", res.Notes)
	}

	// Nothing has the question's word: the synonym's results are the answer.
	res, err = e.Search(ctx, Request{Query: "tcpdump"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 || res.Hits[0].Title != "Advanced Debugging - Packet Capture" || len(res.Suggestions) != 0 || res.NoResultsShown != "" {
		t.Errorf("tcpdump: %+v", res)
	}

	// Synonyms off: only the question's words.
	e.Synonyms = nil
	res, err = e.Search(ctx, Request{Query: "tcpdump"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 {
		t.Errorf("without synonyms: %d hits", len(res.Hits))
	}
}

func TestWasIsntGerman(t *testing.T) {
	if got := DetectLanguage("was scan aborted"); got != "en" {
		t.Errorf("was scan aborted: %s", got)
	}
	if got := DetectLanguage("Was ist neu in Nessus?"); got != "de" {
		t.Errorf("Was ist neu: %s", got)
	}
}

func TestNewProductNames(t *testing.T) {
	for name, want := range map[string]string{"tpm": "Tenable Patch Management", "ACAS": "Security Center"} {
		if got, ok := ProductNamed(name); !ok || got != want {
			t.Errorf("ProductNamed(%q) = %q, %v", name, got, ok)
		}
	}
}

// A product the question names gets a search of its own when none of the results are
// for it.
func TestNamedProductSearchedOnItsOwn(t *testing.T) {
	e := newEngine(t, []langPage{
		{key: enCur, path: "security-center/Content/Storage.htm", title: "Storage Best Practices", products: []string{"Security Center"},
			body: `<p>Best practices for rolling out storage.</p>`},
		{key: enCur, path: "integrations/Tenable-Patch-Management/Content/best-practices.htm", title: "Best Practices for Tenable Patch Express",
			products: []string{"Tenable Patch Management"}, body: `<p>Best practices for deploying patches.</p>`},
	})
	res, err := e.Search(ctx, Request{Query: "best practices for rolling out Tenable Patch Management"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 || res.Hits[0].Product != "Tenable Patch Management" {
		t.Fatalf("hits = %+v", res.Hits)
	}
	if !slices.ContainsFunc(res.Notes, func(n string) bool { return strings.Contains(n, "which the question names") }) {
		t.Errorf("notes = %q", res.Notes)
	}
	// With a product filter, the caller has chosen; nothing is added.
	res, err = e.Search(ctx, Request{Query: "best practices for rolling out", Product: "Security Center"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 || res.Hits[0].Product != "Security Center" || len(res.Notes) != 0 {
		t.Errorf("with a filter: %+v, notes %q", res.Hits, res.Notes)
	}
}
