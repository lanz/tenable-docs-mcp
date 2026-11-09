// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package llmstxt

import (
	"strings"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

func sites(codes ...string) []site.Site {
	var out []site.Site
	for _, c := range codes {
		s, _ := site.ByCode(c)
		out = append(out, s)
	}
	return out
}

func TestWrite(t *testing.T) {
	const en = "https://docs.tenable.com/"
	current := []index.GuideInfo{
		{Path: "integrations/Splunk", Product: "Nessus", GuideType: "Integration Guide", Pages: 25, Name: "Tenable for Splunk",
			Landing: en + "integrations/Splunk/Content/Welcome.htm"},
		{Path: "nessus", Product: "Nessus", GuideType: "User Guide", Pages: 500, Name: "Tenable Nessus User Guide",
			Landing: en + "nessus/Content/GettingStarted.htm", LandingDescription: "Explains how to get started\n  with Tenable Nessus."},
		{Path: "security-center", Product: "Security Center", GuideType: "User Guide", Pages: 300, Name: "Tenable Security Center User Guide",
			Landing: en + "security-center/Content/Welcome.htm"},
		{Path: "glossary", GuideType: "Reference Guide", Pages: 1, Name: "Tenable [Glossary]"},
		{Path: "tenable-core/Nessus", Product: "Tenable Core", GuideType: "User Guide", Pages: 100, LandingTitle: "Welcome to Tenable Core + Tenable Nessus",
			Landing: en + "tenable-core/Nessus/Content/Welcome.htm"},
	}
	older := []index.GuideInfo{
		{Path: "nessus/10_9", Version: "10.9", Product: "Nessus", Name: "Nessus 10.9", Landing: en + "nessus/10_9/Content/GettingStarted.htm"},
		{Path: "nessus/10_11", Version: "10.11", Product: "Nessus", Name: "Nessus 10.11", Landing: en + "nessus/10_11/Content/GettingStarted.htm"},
		{Path: "agent/11_1", Version: "11.1", Product: "Tenable Agent", Name: "Agent 11.1", Landing: en + "agent/11_1/Content/Welcome.htm"},
	}
	var b strings.Builder
	if err := Write(&b, sites("en")[0], current, older, sites("jp")); err != nil {
		t.Fatal(err)
	}
	want := `# Tenable Documentation

> Product documentation for Tenable One and Tenable's other products: user guides, best practices, reference and integration guides, and release notes.

Each guide below links to its first page, whose table of contents links to the rest.

The documentation in other languages:

- [日本語](https://jp.docs.tenable.com/llms.txt)

## Tenable Nessus

- [Tenable Nessus User Guide](https://docs.tenable.com/nessus/Content/GettingStarted.htm): Explains how to get started with Tenable Nessus.
- [Tenable for Splunk](https://docs.tenable.com/integrations/Splunk/Content/Welcome.htm)

## Tenable Security Center

- [Tenable Security Center User Guide](https://docs.tenable.com/security-center/Content/Welcome.htm)

## Tenable Core

- [Tenable Core + Tenable Nessus](https://docs.tenable.com/tenable-core/Nessus/Content/Welcome.htm)

## Other guides

- [Tenable \[Glossary\]](https://docs.tenable.com/glossary)

## Optional

- [Agent 11.1](https://docs.tenable.com/agent/11_1/Content/Welcome.htm)
- [Nessus 10.11](https://docs.tenable.com/nessus/10_11/Content/GettingStarted.htm)
- [Nessus 10.9](https://docs.tenable.com/nessus/10_9/Content/GettingStarted.htm)
`
	if got := b.String(); got != want {
		t.Errorf("llms.txt:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteTranslated(t *testing.T) {
	jp := sites("jp")[0]
	current := []index.GuideInfo{{Path: "nessus", Product: "Nessus", GuideType: "User Guide", Name: "Tenable Nessus ユーザーガイド",
		Landing: "https://jp.docs.tenable.com/nessus/Content/GettingStarted.htm"}}
	for _, code := range []string{"en", "jp", "zh-cn", "zh-tw", "kr", "fr", "de", "es"} {
		s := sites(code)[0]
		var b strings.Builder
		if err := Write(&b, s, nil, nil, nil); err != nil || !strings.HasPrefix(b.String(), "# "+texts[code].Title+"\n") {
			t.Errorf("%s: %q, %v", code, b.String(), err)
		}
	}
	var b strings.Builder
	if err := Write(&b, jp, current, nil, sites("en")); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); !strings.Contains(got, "- [English](https://docs.tenable.com/llms.txt)") ||
		!strings.Contains(got, "\n## Tenable Nessus\n\n- [Tenable Nessus ユーザーガイド](https://jp.docs.tenable.com/nessus/Content/GettingStarted.htm)\n") ||
		strings.Contains(got, "## Optional") {
		t.Errorf("jp llms.txt:\n%s", got)
	}
	if err := Write(&b, site.Site{Code: "pt"}, nil, nil, nil); err == nil {
		t.Error("a site without text should fail")
	}
}
