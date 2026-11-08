// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package build

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// yieldGuides is the guide read from each language's live site: one product per
// language, the largest that language has.
var yieldGuides = map[string]string{
	"en": "nessus", "jp": "nessus",
	"zh-cn": "identity-exposure/SaaS", "zh-tw": "identity-exposure/SaaS", "kr": "identity-exposure/SaaS",
	"fr": "identity-exposure/SaaS", "de": "identity-exposure/SaaS", "es": "identity-exposure/SaaS",
}

const (
	// minYield is the share of a guide's pages that must parse with content.
	minYield = 0.99
	// maxFetchFailures is the share of a guide's pages that may fail to download, from
	// network trouble, before the check gives up on judging the parser.
	maxFetchFailures = 0.02
	// maxYieldPages caps the pages read per language.
	maxYieldPages = 400
	// minContentWords is how many words besides headings a page needs to count as
	// having content.
	minContentWords = 5
)

// yieldResult is one language's parser yield, as TDM_EVAL_OUT records it.
type yieldResult struct {
	Site    string   `json:"site"`
	Guide   string   `json:"guide"`
	Pages   int      `json:"pages"`
	Content int      `json:"content"`
	Empty   []string `json:"empty,omitempty"`
	NotPage int      `json:"not_page"`
	Missing int      `json:"missing"`
	Failed  []string `json:"failed,omitempty"`
	Yield   float64  `json:"yield"`
	Seconds float64  `json:"seconds"`
}

// contentWords counts a page's words outside its headings.
func contentWords(p *page.Page) int {
	n := 0
	for _, s := range p.Sections {
		for _, line := range strings.Split(s.Markdown, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			n += len(strings.Fields(line))
		}
	}
	return n
}

// TestLiveYield reads one product's guide from each language's live site and checks
// that at least 99% of its pages parse with content, to catch changes to the sites'
// markup. It's skipped unless TDM_LIVE=1. It follows robots.txt and the HTTP source's
// polite limits, and reads at most 400 pages per language. With TDM_EVAL_OUT set, each
// language's results are written there as yield-<site>.json.
func TestLiveYield(t *testing.T) {
	if os.Getenv("TDM_LIVE") != "1" {
		t.Skip("set TDM_LIVE=1 to read the live sites")
	}
	ctx := context.Background()
	src := site.NewHTTP(site.HTTPConfig{UserAgent: "tenable-docs-mcp-nightly (+https://github.com/lanz/tenable-docs-mcp)"})
	for _, s := range site.Sites {
		guide := yieldGuides[s.Code]
		t.Run(s.Code, func(t *testing.T) {
			start := time.Now()
			rules, err := site.FetchRobots(ctx, src, s, "tenable-docs-mcp")
			if err != nil {
				t.Fatal(err)
			}
			entries, err := site.FetchSitemap(ctx, src, s)
			if err != nil {
				t.Fatal(err)
			}
			r := yieldResult{Site: s.Code, Guide: guide}
			for _, e := range entries {
				if r.Pages >= maxYieldPages {
					break
				}
				u, err := url.Parse(e.URL)
				if err != nil || !isPagePath(u.Path) || !rules.Allowed(u.EscapedPath()) {
					continue
				}
				if g, ok := site.GuideOf(e.URL); !ok || g.Path != guide {
					continue
				}
				r.Pages++
				resp, err := src.Fetch(ctx, site.Request{URL: e.URL})
				switch {
				case errors.Is(err, site.ErrNotFound):
					r.Missing++
					continue
				case err != nil:
					r.Failed = append(r.Failed, e.URL+": "+err.Error())
					continue
				}
				p, err := page.Parse(resp.Body, e.URL, nil)
				switch {
				case errors.Is(err, page.ErrNotAPage):
					r.NotPage++
				case err != nil:
					r.Failed = append(r.Failed, e.URL+": "+err.Error())
				case contentWords(p) < minContentWords:
					r.Empty = append(r.Empty, e.URL)
				default:
					r.Content++
				}
			}
			r.Seconds = time.Since(start).Seconds()
			if parsed := r.Content + len(r.Empty); parsed > 0 {
				r.Yield = float64(r.Content) / float64(parsed)
			}
			t.Logf("%s %s: %d pages; %d with content, %d empty, %d not pages, %d missing, %d failed; yield %.3f (%.0fs)",
				s.Host, guide, r.Pages, r.Content, len(r.Empty), r.NotPage, r.Missing, len(r.Failed), r.Yield, r.Seconds)
			for _, u := range r.Empty {
				t.Logf("  empty: %s", u)
			}
			for _, f := range r.Failed[:min(5, len(r.Failed))] {
				t.Logf("  failed: %s", f)
			}
			writeYield(t, r)
			switch {
			case r.Pages == 0:
				t.Errorf("no pages of %s in the sitemap", guide)
			case float64(len(r.Failed)) > maxFetchFailures*float64(r.Pages):
				t.Errorf("%d of %d pages failed to download or parse", len(r.Failed), r.Pages)
			case r.Yield < minYield:
				t.Errorf("yield %.3f is below %.2f", r.Yield, minYield)
			}
		})
	}
}

func writeYield(t *testing.T, r yieldResult) {
	dir := os.Getenv("TDM_EVAL_OUT")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(r, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "yield-"+r.Site+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
