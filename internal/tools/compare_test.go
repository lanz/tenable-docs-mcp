// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

func TestComparePage(t *testing.T) {
	cs := connect(t, buildIndex(t))
	var out compareOut
	text := call(t, cs, "compare_versions", map[string]any{"url": en + "nessus/Content/Install.htm", "from_version": "10.5"}, &out)
	if out.From.Version != "10.5" || out.To.Version != "current" || out.From.URL != en+"nessus/10_5/Content/Install.htm" {
		t.Fatalf("versions = %+v → %+v", out.From, out.To)
	}
	status := map[string]string{}
	for _, s := range out.Sections {
		status[s.Heading] = s.Status
	}
	if status["Install Nessus on Linux"] != "changed" || status["Before you begin"] != "added" || status["Steps"] != "added" {
		t.Errorf("sections = %+v", out.Sections)
	}
	intro := out.Sections[slices.IndexFunc(out.Sections, func(s sectionChange) bool { return s.Heading == "Install Nessus on Linux" })]
	if !slices.Contains(intro.Removed, "Install the older installer package.") || !slices.Contains(intro.Added, "Overview of installing.") {
		t.Errorf("intro = %+v", intro)
	}
	if !strings.Contains(text, "10.5 → current: 1 section changed, 2 added, 0 removed") || !strings.Contains(text, "- Install the older installer package.") {
		t.Errorf("summary = %q", text)
	}
	if !slices.Equal(out.Versions, []string{"10.5", "current"}) {
		t.Errorf("versions available = %v", out.Versions)
	}

	// From the older page's URL, the other way round.
	out = compareOut{}
	call(t, cs, "compare_versions", map[string]any{"url": en + "nessus/10_5/Content/Install.htm", "from_version": "v10.5", "to_version": "current"}, &out)
	if out.From.Version != "10.5" || len(out.Sections) != 3 {
		t.Errorf("from the older URL: %+v", out)
	}
}

func TestCompareErrors(t *testing.T) {
	cs := connect(t, buildIndex(t))
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"url": en + "nessus/Content/Install.htm", "from_version": "9.0"}, "they have: 10.5, current"},
		{map[string]any{"url": en + "nessus/Content/Install.htm", "from_version": "current"}, "give from_version"},
		{map[string]any{"url": en + "nessus/Content/Install.htm", "from_version": "10.5", "to_version": "10_5"}, "the same"},
		{map[string]any{"url": en + "nessus/Content/Install.htm", "setting": "x", "from_version": "10.5"}, "not both"},
		{map[string]any{"from_version": "10.5"}, "give url"},
		{map[string]any{"url": "https://example.com/x.htm", "from_version": "10.5"}, "isn't a page on docs.tenable.com"},
	} {
		msg, isErr := callRaw(t, cs, "compare_versions", c.args, nil)
		if !isErr || !strings.Contains(msg, c.want) {
			t.Errorf("%v: %v %q", c.args, isErr, msg)
		}
	}
}

func TestCompareSetting(t *testing.T) {
	table := func(def, desc, link string) string {
		return `<h1>Advanced Settings</h1><table><thead><tr><th>Setting</th><th>Default</th><th>Description</th></tr></thead><tbody>` +
			`<tr><td>Max hosts</td><td>` + def + `</td><td>` + desc + ` See <a href="` + link + `">limits</a>.</td></tr>` +
			`<tr><td>Timeout</td><td>5</td><td>Seconds to wait.</td></tr></tbody></table>`
	}
	ix := buildIndexFrom(t, []fixturePage{
		{index.Key{Site: "en", Older: true}, "nessus/10_5/Content/Advanced.htm", []string{"Nessus"},
			table("30", "Hosts at once.", "https://docs.tenable.com/nessus/10_5/Content/Limits.htm")},
		{index.Key{Site: "en"}, "nessus/Content/Advanced.htm", []string{"Nessus"},
			table("50", "Hosts at once.", "https://docs.tenable.com/nessus/Content/Limits.htm")},
		// Another page with a row of the same name, which mustn't be compared.
		{index.Key{Site: "en"}, "quick-reference/tuning/Content/Settings.htm", []string{"Nessus"},
			table("100", "Something else entirely.", "https://docs.tenable.com/x.htm")},
	})
	cs := connect(t, ix)
	var out compareOut
	text := call(t, cs, "compare_versions", map[string]any{"setting": "Max hosts", "product": "Nessus", "from_version": "10.5"}, &out)
	if out.To.URL == "" || !strings.HasPrefix(out.To.URL, en+"nessus/Content/Advanced.htm") {
		t.Fatalf("compared with %s", out.To.URL)
	}
	// The link to the same page in each version isn't a change; the default is.
	if len(out.Fields) != 1 || out.Fields[0].Field != "default" || out.Fields[0].From != "30" || out.Fields[0].To != "50" {
		t.Errorf("fields = %+v", out.Fields)
	}
	if !strings.Contains(text, "- 10.5: 30") || !strings.Contains(text, "- current: 50") {
		t.Errorf("summary = %q", text)
	}
}

func TestWhatsChanged(t *testing.T) {
	ix := buildIndex(t)
	cs := connect(t, ix)

	// No history yet: the sitemap's dates are all there is.
	var out changesOut
	text := call(t, cs, "whats_changed", map[string]any{"since": "2026-10-01", "product": "Nessus"}, &out)
	if len(out.Changes) != 0 || len(out.Dated) == 0 || out.HistorySince != "" {
		t.Fatalf("without history: %+v", out)
	}
	for _, p := range out.Dated {
		if p.Product != "Nessus" || p.LastMod != "2026-10-26" {
			t.Errorf("dated page %+v", p)
		}
	}
	if !strings.Contains(text, "Before the history begins, the sitemap dates") {
		t.Errorf("summary = %q", text)
	}

	// The history begins, and a page is updated in place, as a crawl does.
	k := index.Key{Site: "en"}
	err := ix.With(k, func(sh *index.Shard) error {
		return sh.Update(ctx, func(tx *index.Tx) error {
			if err := tx.StartHistory(); err != nil {
				return err
			}
			s, _ := site.ByCode("en")
			u := en + "nessus/Content/Install.htm"
			p, err := page.Parse([]byte(`<html lang="en-us"><div id="mc-main-content"><h1>Install Nessus on Linux</h1>`+
				`<h2 id="Steps">Steps</h2><p>Run the new installer.</p></div></html>`), u, nil)
			if err != nil {
				return err
			}
			_, err = tx.Put(index.NewDoc(p, s, site.Entry{URL: u, Products: []string{"Nessus"}, GuideType: "User Guide", LastMod: "2026-11-08"}, nil))
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	out = changesOut{}
	text = call(t, cs, "whats_changed", map[string]any{"since": "30d"}, &out)
	if len(out.Changes) != 1 || out.Changes[0].Change != "updated" || out.HistorySince == "" {
		t.Fatalf("with history: %+v", out)
	}
	status := map[string]string{}
	for _, s := range out.Changes[0].Sections {
		status[s.Heading] = s.Status
	}
	if status["Steps"] != "changed" || status["Before you begin"] != "removed" {
		t.Errorf("sections = %+v", out.Changes[0].Sections)
	}
	if !strings.Contains(text, "updated: Install Nessus on Linux (Nessus)") || !strings.Contains(text, "+ Run the new installer.") {
		t.Errorf("summary = %q", text)
	}

	// Errors.
	for _, args := range []map[string]any{{"since": "last week"}, {"since": "2026-10-01", "product": "Nopeduct"}, {"since": "2026-10-01", "language": "xx"}} {
		if msg, isErr := callRaw(t, cs, "whats_changed", args, nil); !isErr {
			t.Errorf("%v: %q", args, msg)
		}
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)
	for in, want := range map[string]string{"2026-10-01": "2026-10-01", "2026-10": "2026-10-01", "30d": "2026-10-09", "7 days": "2026-11-01"} {
		got, err := parseSince(in, now)
		if err != nil || got.Format("2006-01-02") != want {
			t.Errorf("parseSince(%q) = %v, %v", in, got, err)
		}
	}
}
