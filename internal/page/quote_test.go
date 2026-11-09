// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package page

import (
	"strings"
	"testing"
	"time"
)

// Deeply nested blockquotes are rendered to a few levels, so a hostile page can't
// make rendering cost gigabytes.
func TestNestedBlockquotes(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<div id="mc-main-content"><h1>Quotes</h1>`)
	b.WriteString(strings.Repeat("<blockquote>", 190))
	for range 20000 {
		b.WriteString("<p>line</p>")
	}
	b.WriteString(strings.Repeat("</blockquote>", 190))
	b.WriteString(`</div>`)
	start := time.Now()
	p, err := Parse([]byte(b.String()), "https://docs.tenable.com/nessus/Content/Q.htm", nil)
	if err != nil {
		t.Fatal(err)
	}
	var out int
	deepest := 0
	for _, s := range p.Sections {
		out += len(s.Markdown)
		for _, l := range strings.Split(s.Markdown, "\n") {
			deepest = max(deepest, strings.Count(l, ">"))
		}
	}
	if deepest > maxQuotes || out > 2<<20 || time.Since(start) > 10*time.Second {
		t.Errorf("%d levels, %d bytes, %v", deepest, out, time.Since(start))
	}
	// Two levels still render as two.
	p, _ = Parse([]byte(`<div id="mc-main-content"><h1>Q</h1><blockquote><p>a</p><blockquote><p>b</p></blockquote></blockquote></div>`), "https://docs.tenable.com/nessus/Content/Q.htm", nil)
	if body := p.Sections[0].Markdown; !strings.Contains(body, "> > b") {
		t.Errorf("body = %q", body)
	}
}
