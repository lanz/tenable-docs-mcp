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
}

type evalCase struct {
	Q string `json:"q"`
	// Expect lists the pages that answer the question, as paths on any of the docs
	// sites; * matches within one path segment.
	Expect  []string `json:"expect"`
	Product string   `json:"product,omitempty"`
}

func (c evalCase) matches(pageURL string) bool {
	u, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	p := strings.TrimPrefix(u.Path, "/")
	for _, e := range c.Expect {
		if ok, _ := path.Match(e, p); ok {
			return true
		}
	}
	return false
}

// TestEval scores search on the evaluation sets: recall at 5 (how often an expected
// page is in the top five) and mean reciprocal rank (1 for a first-place answer, ½ for
// second, and so on, up to tenth).
//
//   - eval-en: English questions ranking was tuned on.
//   - eval-en-holdout: English questions written separately and not tuned on, to show
//     whether the tuning carries over.
//   - eval-ja, eval-fr: Japanese and French questions. French covers only some
//     products, so its set includes one the English docs must answer.
//   - eval-versions: questions about older versions of on-premises products.
//   - eval-wording: questions in customers' words rather than the docs', from support-site
//     searches, which the synonyms and product names answer.
//
// It needs an index: TDM_INDEX_DIR holding one, or TDM_SITE_DIRS to build the shards it
// needs (English current and older versions, Japanese, and French) from local copies of
// the sites, into TDM_INDEX_DIR if set so later runs reuse them.
func TestEval(t *testing.T) {
	ix := evalIndex(t)
	e := NewEngine(ix)
	for _, set := range []string{"eval-en", "eval-en-holdout", "eval-versions", "eval-ja", "eval-fr", "eval-wording"} {
		t.Run(set, func(t *testing.T) { evalSet(t, e, set) })
	}
}

func evalSet(t *testing.T, e *Engine, name string) {
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []evalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	hits5, rr := 0, 0.0
	for _, c := range cases {
		r, err := e.Search(ctx, Request{Query: c.Q, Product: c.Product, Limit: 10})
		if err != nil {
			t.Fatalf("%q: %v", c.Q, err)
		}
		rank := 0
		for i, h := range r.Hits {
			if c.matches(h.PageURL) {
				rank = i + 1
				break
			}
		}
		if rank > 0 && rank <= 5 {
			hits5++
		}
		if rank > 0 {
			rr += 1 / float64(rank)
		}
		mark := "  "
		if rank == 0 || rank > 5 {
			mark = "✗ "
		}
		var top []string
		for _, h := range r.Hits[:min(3, len(r.Hits))] {
			u, _ := url.Parse(h.URL)
			top = append(top, strings.TrimPrefix(u.Path, "/"))
		}
		lang := r.Language
		if r.Version != "" {
			lang += " " + r.Version
		}
		t.Logf("%srank %2d  %-56q [%s] intent %-12s products %v; top: %s", mark, rank, c.Q, lang, r.Parsed.Intent, r.Parsed.Products, strings.Join(top, " | "))
	}
	th := thresholds[name]
	recall, mrr := float64(hits5)/float64(len(cases)), rr/float64(len(cases))
	t.Logf("%d questions: recall at 5 %.2f (threshold %.2f), MRR %.2f (threshold %.2f)", len(cases), recall, th[0], mrr, th[1])
	if recall < th[0] || mrr < th[1] {
		t.Errorf("below the thresholds")
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
