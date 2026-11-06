// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package extract

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/page"
)

const settingsURL = "https://docs.tenable.com/widget/Content/Settings.htm"

func parseFixture(t *testing.T, name, pageURL string) *page.Page {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	p, err := page.Parse(data, pageURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSettings(t *testing.T) {
	p := parseFixture(t, "settings.htm", settingsURL)
	got := Settings(p)
	general := Place{Section: 0, URL: settingsURL + "#General", Context: "Widget Settings › General Settings"}
	ui := Place{Section: 1, URL: settingsURL + "#UI", Context: "Widget Settings › Interface Fields"}
	want := []Setting{
		{Name: "Listen Port", Default: "8443", Description: "The port the widget listens on.", Values: "1-65535", Group: "Network", Place: general},
		{Name: "max_hosts", Default: "500", Description: "How many hosts to track.", Group: "Network", Place: general},
		{Name: "Name", Description: "Type a name.", Required: "Yes", Other: []Field{{"Notes", "Up to 64 characters."}}, Place: ui},
		{Name: "--key", Values: "The linking key.", Required: "yes",
			Place: Place{Section: 3, URL: settingsURL + "#Commands", Context: "Widget Settings › Commands"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("settings:\n%+v\nwant\n%+v", got, want)
	}
}

func TestRequirements(t *testing.T) {
	p := parseFixture(t, "settings.htm", settingsURL)
	var got []string
	for _, r := range Requirements(p) {
		var fields []string
		for _, f := range r.Fields {
			fields = append(fields, f.Label+"="+f.Text)
		}
		got = append(got, r.Kind+" ["+strings.Join(fields, "; ")+"] "+r.URL)
	}
	sys := " " + settingsURL + "#Ports"
	want := []string{
		"ports [Flow Direction=Inbound; Port=TCP 8443; Purpose=Web interface]" + sys,
		"operating-systems [Operating System=Linux; Supported Versions=Distro A 9 and later]" + sys,
		"hardware [# of Hosts=Up to 500; CPU Cores1=2; Memory=8 GB]" + sys,
		"hardware [# of Hosts=Up to 500; CPU Cores1=4; Memory=16 GB]" + sys,
		"browsers [Browser=Browser A; Version=120 or later]" + sys,
		"dependencies [Software Name=libwidget; Minimum=2.1]" + sys,
		"other [Item=Clock sync; Value=Within 5 minutes]" + sys,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requirements:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestGlossaryAndLinks(t *testing.T) {
	p := parseFixture(t, "settings.htm", settingsURL)
	terms := Glossary(p)
	if want := []Term{{Term: "WMX", Definition: "Widget Manager Exchange", URL: settingsURL}}; !reflect.DeepEqual(terms, want) {
		t.Errorf("terms = %+v", terms)
	}
	glossary, err := page.Parse([]byte(`<div id="mc-main-content"><h1>Glossary</h1><ul class="MCGlossaryProxy_0">
		<li class="GlossaryPageEntry"><div class="GlossaryPageTerm"><a class="GlossaryPageLink" name="x_anchor1">baseline</a></div>
		<div class="GlossaryPageDefinition">A saved state.</div></li>
		<li class="GlossaryPageEntry"><div class="GlossaryPageTerm">unanchored</div><div class="GlossaryPageDefinition">No anchor.</div></li></ul></div>`),
		"https://docs.tenable.com/glossary/Content/glossary.htm", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Term{
		{Term: "baseline", Definition: "A saved state.", URL: "https://docs.tenable.com/glossary/Content/glossary.htm#x_anchor1"},
		{Term: "unanchored", Definition: "No anchor.", URL: "https://docs.tenable.com/glossary/Content/glossary.htm"},
	}
	if got := Glossary(glossary); !reflect.DeepEqual(got, want) {
		t.Errorf("glossary = %+v", got)
	}

	links := LinksOut(p)
	wantLinks := []string{"https://docs.tenable.com/widget/Content/Install.htm", "https://jp.docs.tenable.com/widget/Content/Other.htm"}
	if !reflect.DeepEqual(links, wantLinks) {
		t.Errorf("links = %q", links)
	}
	g := NewGraph()
	g.Add(settingsURL, links)
	g.Add("https://docs.tenable.com/widget/Content/A.htm", []string{wantLinks[0]})
	if got := g.In(wantLinks[0]); !reflect.DeepEqual(got, []string{"https://docs.tenable.com/widget/Content/A.htm", settingsURL}) {
		t.Errorf("in = %q", got)
	}
	g.Add(settingsURL, nil) // the page changed and no longer links out
	if got := g.In(wantLinks[0]); !reflect.DeepEqual(got, []string{"https://docs.tenable.com/widget/Content/A.htm"}) {
		t.Errorf("in after update = %q", got)
	}
	if got := g.Out("https://docs.tenable.com/widget/Content/A.htm"); len(got) != 1 {
		t.Errorf("out = %q", got)
	}
}

func TestReleaseNotes(t *testing.T) {
	const u = "https://docs.tenable.com/release-notes/Content/widget/2026.htm"
	if !IsReleaseNotes(u) || IsReleaseNotes(settingsURL) || IsReleaseNotes("https://docs.tenable.com/release-notes-old/Content/a.htm") {
		t.Error("IsReleaseNotes")
	}
	p := parseFixture(t, "releases.htm", u)
	var got []string
	for _, e := range ReleaseNotes(p) {
		got = append(got, e.Title+" | "+e.Version+" | "+e.Date+" | "+e.URL+" | "+strings.ReplaceAll(e.Markdown, "\n", " "))
	}
	want := []string{
		"Widget Manager 3.2.1 (2026-10-22) | 3.2.1 | 2026-10-22 | " + u + "#w321 | ## Widget Manager 3.2.1 (2026-10-22)  **Bug Fixes**  Fixed a crash.",
		"October 15, 2026 |  | 2026-10-15 | " + u + " | ## October 15, 2026  Changes on that day.  ## New Features  A new feature that belongs to October 15.",
		"September 2026 |  | 2026-09 | " + u + " | ## September 2026  A month without a day.",
		"Widget Hub Patch 202609.2 - 2026-09-03 | 202609.2 | 2026-09-03 | " + u + " | ## Widget Hub Patch 202609.2 - 2026-09-03  A patch.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestParseRelease(t *testing.T) {
	for heading, want := range map[string][2]string{
		"Tenable Nessus 10.12.6 (2026-10-22)":           {"10.12.6", "2026-10-22"},
		"October 28, 2026":                              {"", "2026-10-28"},
		"Sept. 3, 2025":                                 {"", "2025-09-03"},
		"Lumin Exposure View February 15, 2024":         {"", "2024-02-15"},
		"3 March 2026":                                  {"", "2026-03-03"},
		"2026年10月28日":                                   {"", "2026-10-28"},
		"2026년 10월 28일 릴리스":                             {"", "2026-10-28"},
		"28 octobre 2026":                               {"", "2026-10-28"},
		"28. Oktober 2026":                              {"", "2026-10-28"},
		"28 de octubre de 2026":                         {"", "2026-10-28"},
		"Mai 2026":                                      {"", "2026-05"},
		"Version 6.4.1":                                 {"6.4.1", ""},
		"New Features":                                  {"", ""},
		"Tenable Agent 10.8.0 - 11.0.1 (2026-13-40)":    {"10.8.0", ""},
		"Identity Exposure 3.93 (2025-06-30) and later": {"3.93", "2025-06-30"},
	} {
		v, d := ParseRelease(heading)
		if v != want[0] || d != want[1] {
			t.Errorf("ParseRelease(%q) = %q, %q; want %q, %q", heading, v, d, want[0], want[1])
		}
	}
}

func TestPlain(t *testing.T) {
	for in, want := range map[string]string{
		"**Listen Port**":           "Listen Port",
		"`max_hosts`":               "max_hosts",
		"[Name](https://x.example)": "Name",
		`a\_b \* c<br>d`:            "a_b * c d",
		"*Italic*":                  "Italic",
	} {
		if got := Plain(in); got != want {
			t.Errorf("Plain(%q) = %q", in, got)
		}
	}
}
