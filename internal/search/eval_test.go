// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package search

import (
	"encoding/json"
	"errors"
	"fmt"
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

// First thresholds for the English evaluation set.
const (
	minRecallAt5 = 0.80
	minMRR       = 0.60
)

type evalCase struct {
	Q string `json:"q"`
	// Expect lists the pages that answer the question, as paths on docs.tenable.com;
	// * matches within one path segment.
	Expect  []string `json:"expect"`
	Product string   `json:"product,omitempty"`
}

func (c evalCase) matches(pageURL string) bool {
	p := strings.TrimPrefix(pageURL, "https://docs.tenable.com/")
	for _, e := range c.Expect {
		if ok, _ := path.Match(e, p); ok {
			return true
		}
	}
	return false
}

// TestEvalEnglish scores search on the English evaluation sets: recall at 5 (how often
// an expected page is in the top five) and mean reciprocal rank (1 for a first-place
// answer, ½ for second, and so on, up to tenth). eval-en.json is the set ranking was
// tuned on; eval-en-holdout.json was written separately and not tuned on, to show
// whether the tuning carries over. It needs an English index: either TDM_INDEX_DIR
// holding one, or TDM_SITE_DIRS to build one from a local copy of the site (into
// TDM_INDEX_DIR if set, so later runs reuse it).
func TestEvalEnglish(t *testing.T) {
	sh := evalShard(t)
	s := New(sh)
	for _, set := range []string{"eval-en.json", "eval-en-holdout.json"} {
		t.Run(strings.TrimSuffix(set, ".json"), func(t *testing.T) { evalSet(t, s, set) })
	}
}

func evalSet(t *testing.T, s *Searcher, file string) {
	data, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	var cases []evalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	hits5, rr := 0, 0.0
	for _, c := range cases {
		r, err := s.Search(ctx, Request{Query: c.Q, Product: c.Product, Limit: 10})
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
			top = append(top, strings.TrimPrefix(h.URL, "https://docs.tenable.com/"))
		}
		t.Logf("%srank %2d  %-62q intent %-12s products %v; top: %s", mark, rank, c.Q, r.Parsed.Intent, r.Parsed.Products, strings.Join(top, " | "))
	}
	recall, mrr := float64(hits5)/float64(len(cases)), rr/float64(len(cases))
	t.Logf("%d questions: recall at 5 %.2f (threshold %.2f), MRR %.2f (threshold %.2f)", len(cases), recall, minRecallAt5, mrr, minMRR)
	if recall < minRecallAt5 || mrr < minMRR {
		t.Errorf("below the thresholds")
	}
}

func evalShard(t *testing.T) *index.Shard {
	t.Helper()
	k := index.Key{Site: "en"}
	dir := os.Getenv("TDM_INDEX_DIR")
	if dir != "" {
		if _, err := os.Stat(filepath.Join(dir, k.FileName())); err == nil {
			ix, err := index.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ix.Close() })
			return ix.Shard(k)
		}
	}
	v := os.Getenv("TDM_SITE_DIRS")
	if v == "" {
		t.Skip("set TDM_INDEX_DIR to an index, or TDM_SITE_DIRS to build one")
	}
	dirs, err := site.ParseSiteDirs(v)
	if err != nil || dirs["en"] == "" {
		t.Skipf("TDM_SITE_DIRS has no folder for en (%v)", err)
	}
	if dir == "" {
		dir = filepath.Join(t.TempDir(), "index")
	}
	ix, err := index.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	start := time.Now()
	if err := buildCurrent(ix, dirs["en"]); err != nil {
		t.Fatal(err)
	}
	t.Logf("built the English index in %v", time.Since(start).Round(time.Second))
	return ix.Shard(k)
}

// buildCurrent builds the English shard of current guide versions from a local copy.
func buildCurrent(ix *index.Index, dir string) error {
	src, err := site.NewLocal(map[string]string{"en": dir})
	if err != nil {
		return err
	}
	en := site.Sites[0]
	entries, err := site.FetchSitemap(ctx, src, en)
	if err != nil {
		return err
	}
	b, err := ix.Build(index.Key{Site: "en"})
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
		if ok && g.Version != "" {
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
		batch = append(batch, index.NewDoc(p, en, e, site.TocTrail(toc, e.URL)))
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
