// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// Thresholds for the evaluation sets: recall at 5 and mean reciprocal rank. Japanese and
// French are set lower than English and versions: the sets are smaller, and trigram
// search is weaker than stemmed word search.
var thresholds = map[string][2]float64{
	"eval-en":         {0.80, 0.60},
	"eval-en-holdout": {0.80, 0.60},
	"eval-versions":   {0.80, 0.60},
	"eval-ja":         {0.70, 0.50},
	"eval-fr":         {0.70, 0.50},
	"eval-wording":    {0.80, 0.60},
	"eval-common":     {0.80, 0.60},
}

type evalCase struct {
	Q string `json:"q"`
	// Expect lists the pages that answer the question, as paths on any of the docs
	// sites; * matches within one path segment. A path can end in #anchor, the section
	// that answers it. An empty list means no page does, and the case isn't scored.
	Expect  []string `json:"expect"`
	Product string   `json:"product,omitempty"`
	// Note says why a case is the way it is.
	Note string `json:"note,omitempty"`
}

// matches reports whether a page is one of the expected ones.
func (c evalCase) matches(pageURL string) bool {
	p := urlPath(pageURL)
	for _, e := range c.Expect {
		e, _, _ = strings.Cut(e, "#")
		if ok, _ := path.Match(e, p); ok {
			return true
		}
	}
	return false
}

// sections reports whether the case names a section, and matchesSection whether a
// result is one of the sections it names.
func (c evalCase) sections() bool {
	return slices.ContainsFunc(c.Expect, func(e string) bool { return strings.Contains(e, "#") })
}

func (c evalCase) matchesSection(sectionURL string) bool {
	u, err := url.Parse(sectionURL)
	if err != nil {
		return false
	}
	for _, e := range c.Expect {
		e, anchor, ok := strings.Cut(e, "#")
		if !ok {
			continue
		}
		if m, _ := path.Match(e, strings.TrimPrefix(u.Path, "/")); m && strings.EqualFold(u.Fragment, anchor) {
			return true
		}
	}
	return false
}

func urlPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

// sectionThreshold is the share of cases that name a section whose section must be in
// the top five, across every set. Sections rank less well than pages: the right page's
// neighboring section, or its introduction, often comes first.
const sectionThreshold = 0.60

// evalSets are the search evaluation sets, in the order they run.
var evalSets = []string{"eval-en", "eval-en-holdout", "eval-versions", "eval-ja", "eval-fr", "eval-wording", "eval-common"}

// evalResult is a set's scores, as TDM_EVAL_OUT records them.
type evalResult struct {
	Set          string       `json:"set"`
	Questions    int          `json:"questions"`
	Scored       int          `json:"scored"`
	RecallAt5    float64      `json:"recall_at_5"`
	MRR          float64      `json:"mrr"`
	Thresholds   [2]float64   `json:"thresholds"`
	SectionCases int          `json:"section_cases"`
	SectionsAt5  int          `json:"sections_at_5"`
	Cases        []caseResult `json:"cases"`
}

type caseResult struct {
	Q string `json:"q"`
	// Rank is the first expected page's place in the results (0: not in the top ten),
	// and SectionRank the first expected section's, when the case names one.
	Rank        int      `json:"rank"`
	SectionRank int      `json:"section_rank,omitempty"`
	Scored      bool     `json:"scored"`
	Language    string   `json:"language"`
	Top         []string `json:"top"`
}

// TestEval scores search on the evaluation sets: recall at 5 (how often an expected
// page is in the top five) and mean reciprocal rank (1 for a first-place answer, ½ for
// second, and so on, up to tenth). Cases that name the section that answers them are
// also scored on whether that section is in the top five.
//
//   - eval-en: English questions ranking was tuned on.
//   - eval-en-holdout: English questions written separately and not tuned on, to show
//     whether the tuning carries over.
//   - eval-ja, eval-fr: Japanese and French questions. French covers only some
//     products, so its set includes one the English docs must answer.
//   - eval-versions: questions about older versions of on-premises products.
//   - eval-wording: questions in customers' words rather than the docs', which the
//     synonyms and product names answer.
//   - eval-common: common questions about tasks, settings, and roles, including one the
//     docs don't answer.
//
// It needs an index: TDM_INDEX_DIR holding one, or TDM_SITE_DIRS to build the shards it
// needs (English current and older versions, Japanese, and French) from local copies of
// the sites, into TDM_INDEX_DIR if set so later runs reuse them. With TDM_EVAL_OUT set
// to a folder, each set's results are written there as <set>.json, to compare releases.
func TestEval(t *testing.T) {
	ix := evalIndex(t)
	e := NewEngine(ix)
	sections, sectionHits := 0, 0
	for _, set := range evalSets {
		t.Run(set, func(t *testing.T) {
			r := evalSet(t, e, set)
			sections += r.SectionCases
			sectionHits += r.SectionsAt5
		})
	}
	if sections > 0 {
		share := float64(sectionHits) / float64(sections)
		t.Logf("sections: %d of %d expected sections in the top five (%.2f; threshold %.2f)", sectionHits, sections, share, sectionThreshold)
		if share < sectionThreshold {
			t.Errorf("expected sections below the threshold")
		}
	}
}

func evalSet(t *testing.T, e *Engine, name string) evalResult {
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []evalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	res := evalResult{Set: name, Questions: len(cases), Thresholds: thresholds[name]}
	hits5, rr := 0, 0.0
	for _, c := range cases {
		r, err := e.Search(ctx, Request{Query: c.Q, Product: c.Product, Limit: 10})
		if err != nil {
			t.Fatalf("%q: %v", c.Q, err)
		}
		cr := caseResult{Q: c.Q, Scored: len(c.Expect) > 0, Language: r.Language}
		for i, h := range r.Hits {
			if cr.Rank == 0 && c.matches(h.PageURL) {
				cr.Rank = i + 1
			}
			if cr.SectionRank == 0 && c.matchesSection(h.URL) {
				cr.SectionRank = i + 1
			}
		}
		for _, h := range r.Hits[:min(3, len(r.Hits))] {
			u, _ := url.Parse(h.URL)
			top := strings.TrimPrefix(u.Path, "/")
			if u.Fragment != "" {
				top += "#" + u.Fragment
			}
			cr.Top = append(cr.Top, top)
		}
		res.Cases = append(res.Cases, cr)
		lang := r.Language
		if r.Version != "" {
			lang += " " + r.Version
		}
		if !cr.Scored {
			t.Logf("   not scored  %-56q [%s]: %s", c.Q, lang, c.Note)
			continue
		}
		res.Scored++
		if cr.Rank > 0 && cr.Rank <= 5 {
			hits5++
		}
		if cr.Rank > 0 {
			rr += 1 / float64(cr.Rank)
		}
		section := ""
		if c.sections() {
			res.SectionCases++
			section = fmt.Sprintf(" section %2d", cr.SectionRank)
			if cr.SectionRank > 0 && cr.SectionRank <= 5 {
				res.SectionsAt5++
			} else {
				section += " ✗"
			}
		}
		mark := "  "
		if cr.Rank == 0 || cr.Rank > 5 {
			mark = "✗ "
		}
		t.Logf("%srank %2d%s  %-56q [%s] intent %-12s products %v; top: %s", mark, cr.Rank, section, c.Q, lang, r.Parsed.Intent, r.Parsed.Products, strings.Join(cr.Top, " | "))
	}
	th := thresholds[name]
	if res.Scored > 0 {
		res.RecallAt5, res.MRR = float64(hits5)/float64(res.Scored), rr/float64(res.Scored)
	}
	t.Logf("%d questions (%d scored): recall at 5 %.2f (threshold %.2f), MRR %.2f (threshold %.2f); %d of %d expected sections in the top five",
		len(cases), res.Scored, res.RecallAt5, th[0], res.MRR, th[1], res.SectionsAt5, res.SectionCases)
	if res.RecallAt5 < th[0] || res.MRR < th[1] {
		t.Errorf("below the thresholds")
	}
	writeEvalResult(t, res)
	return res
}

// writeEvalResult writes a set's results to TDM_EVAL_OUT, when it's set.
func writeEvalResult(t *testing.T, r evalResult) {
	dir := os.Getenv("TDM_EVAL_OUT")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, r.Set+".json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// evalIndex opens or builds the index the evaluation sets need.
func evalIndex(t *testing.T) *index.Index {
	t.Helper()
	need := []index.Key{{Site: "en"}, {Site: "en", Older: true}, {Site: "jp"}, {Site: "fr"}}
	dir := os.Getenv("TDM_INDEX_DIR")
	v := os.Getenv("TDM_SITE_DIRS")
	if dir == "" && v == "" {
		t.Skip("set TDM_INDEX_DIR to an index, or TDM_SITE_DIRS to build one")
	}
	if dir == "" {
		dir = filepath.Join(t.TempDir(), "index")
	}
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	var dirs map[string]string
	if v != "" {
		if dirs, err = site.ParseSiteDirs(v); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range need {
		if ix.Shard(k) != nil {
			continue
		}
		if dirs[k.Site] == "" {
			t.Skipf("the index has no %s shard, and TDM_SITE_DIRS has no folder to build it from", k)
		}
		start := time.Now()
		if err := buildShard(ix, k, dirs[k.Site]); err != nil {
			t.Fatal(err)
		}
		t.Logf("built %s in %v", k, time.Since(start).Round(time.Second))
	}
	return ix
}

// buildShard builds a shard from a local copy of its site.
func buildShard(ix *index.Index, k index.Key, dir string) error {
	src, err := site.NewLocal(map[string]string{k.Site: dir})
	if err != nil {
		return err
	}
	s, _ := site.ByCode(k.Site)
	entries, err := site.FetchSitemap(ctx, src, s)
	if err != nil {
		return err
	}
	b, err := ix.Build(k)
	if err != nil {
		return err
	}
	tocs := map[string][]*site.TocEntry{}
	var batch []index.Doc
	flush := func() error {
		err := b.Update(ctx, func(tx *index.Tx) error {
			for _, d := range batch {
				if _, err := tx.Put(d); err != nil {
					return err
				}
			}
			return nil
		})
		batch = nil
		return err
	}
	for _, e := range entries {
		g, ok := site.GuideOf(e.URL)
		if (ok && g.Version != "") != k.Older {
			continue
		}
		resp, err := src.Fetch(ctx, site.Request{URL: e.URL})
		if err != nil {
			continue
		}
		p, err := page.Parse(resp.Body, e.URL, nil)
		if errors.Is(err, page.ErrNotAPage) {
			continue
		} else if err != nil {
			return fmt.Errorf("%s: %w", e.URL, err)
		}
		toc, seen := tocs[g.RootURL]
		if ok && !seen {
			toc, _ = site.FetchToc(ctx, src, g)
			tocs[g.RootURL] = toc
		}
		batch = append(batch, index.NewDoc(p, s, e, site.TocTrail(toc, e.URL)))
		if len(batch) >= 500 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := b.Optimize(ctx); err != nil {
		return err
	}
	return ix.Install(b)
}
