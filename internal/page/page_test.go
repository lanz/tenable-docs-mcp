// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package page

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const fixtureURL = "https://docs.tenable.com/widget/Content/Install.htm"

// dump writes a page in a form that's easy to review in a golden file.
func dump(p *Page) string {
	var b strings.Builder
	fmt.Fprintf(&b, "URL: %s\nTitle: %s\nDescription: %s\nLang: %s\nProduct: %s %s (%s)\n",
		p.URL, p.Title, p.Description, p.Lang, p.Product, p.ProductVersion, p.GuideType)
	for _, r := range p.Requirements {
		fmt.Fprintf(&b, "Requirement: %q: %q %q\n", r.Label, r.Text, r.Roles)
	}
	for _, s := range p.Sections {
		fmt.Fprintf(&b, "\n=== %s (level %d) %s\n--- %s\n%s\n", s.Heading, s.Level, s.URL, s.Context(), s.Markdown)
	}
	b.WriteString("\n")
	for _, tb := range p.Tables {
		fmt.Fprintf(&b, "Table in section %d, caption %q, headers %q\n", tb.Section, tb.Caption, tb.Headers)
		for _, r := range tb.Rows {
			fmt.Fprintf(&b, "  row (group %q): %q\n", r.Group, r.Cells)
		}
	}
	for _, l := range p.Links {
		fmt.Fprintf(&b, "Link in section %d: %s %q\n", l.Section, l.URL, l.Text)
	}
	for _, term := range p.Terms {
		fmt.Fprintf(&b, "Term in section %d: %q #%s: %q\n", term.Section, term.Term, term.Anchor, term.Definition)
	}
	return b.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	file := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("%s differs; run go test -update and review the diff\n%s", name, got)
	}
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseGolden(t *testing.T) {
	data := readFixture(t, "install.htm")
	p, err := Parse(data, fixtureURL+"#ignored", nil)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "install.golden", dump(p))
	gp, err := Parse(readFixture(t, "glossary.htm"), "https://docs.tenable.com/glossary/Content/glossary.htm", nil)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "glossary.golden", dump(gp))

	// The same page split into smaller sections, at its h3 headings too.
	small, err := Parse(data, fixtureURL, &Options{MaxSectionBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(small.Sections) != len(p.Sections) {
		t.Errorf("an h3 inside a drop-down shouldn't start a section: %d and %d sections", len(small.Sections), len(p.Sections))
	}
	// The page reads the same with Windows line endings, as in a local copy.
	crlf, err := Parse([]byte(strings.ReplaceAll(string(data), "\n", "\r\n")), fixtureURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(crlf, p) {
		t.Error("CRLF line endings change the result")
	}
}

func TestSplitAtH3(t *testing.T) {
	long := strings.Repeat("Words that fill the section. ", 20)
	doc := `<html lang="fr-fr"><head><title>Titre</title></head><body><div id="mc-main-content">
		<p>Intro without a heading.</p>
		<h2 id="a">Part A</h2><p>` + long + `</p>
		<h3 id="a1">A one</h3><p>` + long + `</p>
		<h3>A two</h3><p>` + long + `</p>
		<h2 id="b">Part B</h2><h3 id="b1">B one</h3><p>` + long + `</p><p>` + long + `</p>
		<h4>Small heading</h4><p>Under it.</p>
	</div></body></html>`
	p, err := Parse([]byte(doc), "https://fr.docs.tenable.com/widget/Content/Page.htm", &Options{MaxSectionBytes: 500})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range p.Sections {
		got = append(got, fmt.Sprintf("%d %s %s [%s]", s.Level, s.Heading, s.URL, s.Context()))
	}
	want := []string{
		"1 Titre https://fr.docs.tenable.com/widget/Content/Page.htm [Titre]",
		"2 Part A https://fr.docs.tenable.com/widget/Content/Page.htm#a [Titre › Part A]",
		"3 A one https://fr.docs.tenable.com/widget/Content/Page.htm#a1 [Titre › Part A › A one]",
		"3 A two https://fr.docs.tenable.com/widget/Content/Page.htm [Titre › Part A › A two]",
		"3 B one https://fr.docs.tenable.com/widget/Content/Page.htm#b1 [Titre › Part B › B one]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sections:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.HasPrefix(p.Sections[0].Markdown, "# Titre\n\nIntro") {
		t.Errorf("intro = %q", p.Sections[0].Markdown)
	}
	if last := p.Sections[len(p.Sections)-1].Markdown; !strings.Contains(last, "#### Small heading\n\nUnder it.") {
		t.Errorf("h4 = %q", last)
	}
	if len(p.Requirements) != 0 || p.Lang != "fr-fr" {
		t.Errorf("page = %+v", p)
	}
}

func TestRequirements(t *testing.T) {
	for _, c := range []struct {
		text, lang string
		want       Requirement
	}{
		{"Required User Role: Basic User, Scan Operator, or Administrator.", "en-us",
			Requirement{"Required User Role", "Basic User, Scan Operator, or Administrator.", []string{"Basic User", "Scan Operator", "Administrator"}}},
		{"Required permission: You must have an administrator account on the domain to perform the procedure.", "en-us",
			Requirement{"Required permission", "You must have an administrator account on the domain to perform the procedure.", nil}},
		{"Required User Role: Administrator or organizational user with appropriate permissions. See details.", "en-us",
			Requirement{"Required User Role", "Administrator or organizational user with appropriate permissions. See details.", nil}},
		{"必要なユーザーロール：管理者", "ja-jp", Requirement{"必要なユーザーロール", "管理者", nil}},
		{"No label here", "en", Requirement{Text: "No label here"}},
	} {
		if got := parseRequirement(c.text, c.lang); !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseRequirement(%q) = %#v", c.text, got)
		}
	}
}

func TestProducts(t *testing.T) {
	for _, c := range []struct {
		listed  []string
		guide   string
		product string
		related []string
	}{
		{[]string{"Tenable Core", "OT Exposure"}, "tenable-core/OT-security", "Tenable Core", []string{"OT Exposure"}},
		{[]string{"Nessus"}, "tenable-core/Nessus", "Tenable Core", []string{"Nessus"}},
		{[]string{"Tenable Core"}, "release-notes", "Tenable Core", nil},
		{[]string{"Nessus", "Tenable Agent"}, "nessus/10_8", "Nessus", []string{"Tenable Agent"}},
		{[]string{"A", "B", "C", "D"}, "glossary", "", []string{"A", "B", "C", "D"}},
		{nil, "x", "", nil},
	} {
		p, r := Products(c.listed, c.guide)
		if p != c.product || !reflect.DeepEqual(r, c.related) {
			t.Errorf("Products(%q, %q) = %q %q", c.listed, c.guide, p, r)
		}
	}
}

func TestEdgeCases(t *testing.T) {
	if _, err := Parse([]byte("<p>x</p>"), "/relative", nil); err == nil {
		t.Error("a relative page URL should be an error")
	}
	for name, c := range map[string]struct{ doc, want string }{
		"no main content":   {`<html><body><h1>T</h1><p>Body text.</p></body></html>`, "# T\n\nBody text."},
		"no h1":             {`<title>From title</title><p>Only text.</p>`, "# From title\n\nOnly text."},
		"block inside span": {`<div id="mc-main-content"><h1>T</h1><span><p>One</p><p>Two</p></span></div>`, "# T\n\nOne\n\nTwo"},
		"code with fences":  {"<div id=\"mc-main-content\"><h1>T</h1><pre>a ``` b</pre></div>", "# T\n\n````\na ``` b\n````"},
		"inline backticks":  {"<div id=\"mc-main-content\"><h1>T</h1><p>Run <code>a`b</code>.</p></div>", "# T\n\nRun ``a`b``."},
		"links":             {`<div id="mc-main-content"><h1>T</h1><p><a href="javascript:x()">js</a> <a href="mailto:a@b.example">mail</a> <a href="A (1).htm">paren</a></p></div>`, "# T\n\njs mail [paren](https://docs.tenable.com/widget/Content/A%20%281%29.htm)"},
		"one-row table":     {`<div id="mc-main-content"><h1>T</h1><table><tr><td>a</td><td>b</td></tr></table></div>`, "# T\n\n|  |  |\n| --- | --- |\n| a | b |"},
		"list stray child":  {`<div id="mc-main-content"><h1>T</h1><ol start="3"><li>a</li><ul><li>b</li></ul></ol></div>`, "# T\n\n3. a\n   - b"},
		"note with lists":   {`<div id="mc-main-content"><h1>T</h1><div class="Note"><p>Note:</p><ul><li>x</li></ul></div></div>`, "# T\n\n> Note:\n>\n> - x"},
		"translated links":  {`<html lang="ja-jp"><div id="mc-main-content"><h1>T</h1><p><a href="https://docs.tenable.com/a/Content/B.htm">x</a> <a href="https://www.tenable.com/downloads">y</a></p></div>`, "# T\n\n[x](https://jp.docs.tenable.com/a/Content/B.htm) [y](https://www.tenable.com/downloads)"},
	} {
		pageURL := fixtureURL
		if name == "translated links" {
			pageURL = "https://jp.docs.tenable.com/widget/Content/Install.htm"
		}
		p, err := Parse([]byte(c.doc), pageURL, nil)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var got []string
		for _, s := range p.Sections {
			got = append(got, s.Markdown)
		}
		if g := strings.Join(got, "\n\n"); g != c.want {
			t.Errorf("%s:\n%q\nwant\n%q", name, g, c.want)
		}
	}
}

func TestNotAPage(t *testing.T) {
	for name, doc := range map[string]string{
		"PDF":   "%PDF-1.7\n%binary",
		"empty": ``,
		"frame": `<html><head><title></title><script src="x.js"></script></head><body><div id="navigation"></div></body></html>`,
	} {
		if _, err := Parse([]byte(doc), fixtureURL, nil); !errors.Is(err, ErrNotAPage) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDeepNesting(t *testing.T) {
	// Deeper than the renderer follows (maxDepth), within what the HTML parser accepts.
	deep := strings.Repeat("<div>", 400) + "deep <b>text</b>" + strings.Repeat("</div>", 400)
	p, err := Parse([]byte(`<div id="mc-main-content"><h1>T</h1>`+deep+`</div>`), fixtureURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Sections) != 1 || !strings.HasSuffix(p.Sections[0].Markdown, "deep text") {
		t.Errorf("sections = %+v", p.Sections)
	}
	inline := strings.Repeat("<span>", 400) + "inline" + strings.Repeat("</span>", 400)
	if p, err = Parse([]byte(`<div id="mc-main-content"><h1>T</h1><p>`+inline+`</p></div>`), fixtureURL, nil); err != nil || !strings.HasSuffix(p.Sections[0].Markdown, "inline") {
		t.Errorf("inline nesting = %v, %+v", err, p)
	}
	// Past the parser's own limit, Parse reports an error.
	if _, err := Parse([]byte(strings.Repeat("<div>", 600)), fixtureURL, nil); err == nil {
		t.Error("no error for nesting past the parser's limit")
	}
}

func FuzzParse(f *testing.F) {
	f.Add(readFixture(f, "install.htm"))
	for _, s := range []string{
		`<table><tr><td rowspan="999999" colspan="-1">x</td></tr></table>`,
		`<ol><li value="x">a<ul><li>b</li></ul></li></ol>`,
		`<h2 id="a"><a name="b">t</a></h2><div class="MCDropDown"><p>no head</p></div>`,
		`<p class="Permission">Label：x</p><pre>` + "\r\n" + `code</pre>`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Parse(data, fixtureURL, &Options{MaxSectionBytes: 200})
		if err != nil {
			return
		}
		for _, s := range p.Sections {
			if !strings.HasPrefix(s.URL, fixtureURL) {
				t.Errorf("section URL %q", s.URL)
			}
			if utf8.ValidString(string(data)) && !utf8.ValidString(s.Markdown) {
				t.Errorf("invalid UTF-8 from valid input: %q", s.Markdown)
			}
		}
	})
}
