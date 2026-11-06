// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

// TestLocalBuild builds both shards of one language (current and older versions)
// from a local copy of its site, reopens them, and searches them. It's skipped unless
// TDM_SITE_DIRS lists the folders (see site.ParseSiteDirs); TDM_BUILD_SITE picks the
// language (default en), and TDM_INDEX_DIR keeps the result.
func TestLocalBuild(t *testing.T) {
	v := os.Getenv("TDM_SITE_DIRS")
	if v == "" {
		t.Skip("set TDM_SITE_DIRS to read local copies of the sites")
	}
	code := os.Getenv("TDM_BUILD_SITE")
	if code == "" {
		code = "en"
	}
	dirs, err := site.ParseSiteDirs(v)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := site.ByCode(code)
	if !ok || dirs[code] == "" {
		t.Skipf("TDM_SITE_DIRS has no folder for %q", code)
	}
	src, err := site.NewLocal(map[string]string{code: dirs[code]})
	if err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("TDM_INDEX_DIR")
	if dir == "" {
		dir = filepath.Join(t.TempDir(), "index")
	}
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()

	start := time.Now()
	entries, err := site.FetchSitemap(ctx, src, s)
	if err != nil {
		t.Fatal(err)
	}
	builds := map[bool]*Shard{}
	for _, older := range []bool{false, true} {
		if builds[older], err = ix.Build(Key{Site: code, Older: older}); err != nil {
			t.Fatal(err)
		}
	}
	tocs := map[string][]*site.TocEntry{}
	batch := map[bool][]Doc{}
	flush := func(older bool) {
		if len(batch[older]) == 0 {
			return
		}
		put(t, builds[older], batch[older]...)
		batch[older] = nil
	}
	skipped := 0
	for _, e := range entries {
		resp, err := src.Fetch(ctx, site.Request{URL: e.URL})
		if err != nil {
			skipped++
			continue
		}
		p, err := page.Parse(resp.Body, e.URL, nil)
		if errors.Is(err, page.ErrNotAPage) {
			skipped++
			continue
		} else if err != nil {
			t.Errorf("%s: %v", e.URL, err)
			continue
		}
		g, _ := site.GuideOf(e.URL)
		toc, seen := tocs[g.RootURL]
		if !seen && g.RootURL != "" {
			toc, _ = site.FetchToc(ctx, src, g)
			tocs[g.RootURL] = toc
			if toc != nil {
				if err := builds[g.Version != ""].Update(ctx, func(tx *Tx) error { return tx.PutToc(g, toc) }); err != nil {
					t.Fatal(err)
				}
			}
		}
		d := NewDoc(p, s, e, site.TocTrail(toc, e.URL))
		d.LastModified = resp.LastModified
		older := g.Version != ""
		batch[older] = append(batch[older], d)
		if len(batch[older]) >= 500 {
			flush(older)
		}
	}
	for _, older := range []bool{false, true} {
		flush(older)
		b := builds[older]
		if err := b.Update(ctx, func(tx *Tx) error { return tx.SetMeta("built_at", time.Now().UTC().Format(time.RFC3339)) }); err != nil {
			t.Fatal(err)
		}
		if err := b.Optimize(ctx); err != nil {
			t.Fatal(err)
		}
		if err := ix.Install(b); err != nil {
			t.Fatal(err)
		}
	}
	built := time.Since(start)
	ix.Close()

	// Reopen, as a later run would.
	ix, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	for _, k := range []Key{{Site: code}, {Site: code, Older: true}} {
		sh := ix.Shard(k)
		if sh == nil {
			t.Fatalf("%s didn't reopen", k)
		}
		counts, err := sh.Counts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(sh.Path)
		ver, _ := sh.SchemaVersion(ctx)
		t.Logf("%s: schema %d, %.1f MB, %v", k, ver, float64(info.Size())/(1<<20), counts)
		if counts["pages"] == 0 || counts["sections"] == 0 {
			t.Errorf("%s is empty", k)
		}
		q := "install"
		if code != "en" {
			q = "Tenable"
		}
		t0 := time.Now()
		hits, err := sh.SearchSections(ctx, MatchQuery(q), 5)
		if err != nil || len(hits) == 0 {
			t.Errorf("%s: search %q = %d hits, %v", k, q, len(hits), err)
		} else {
			t.Logf("%s: %q found %d hits in %v; first in %s", k, q, len(hits), time.Since(t0).Round(time.Microsecond), hits[0].Location)
		}
	}
	t.Logf("built %d pages (%d other documents skipped) in %v", len(entries)-skipped, skipped, built.Round(time.Second))
}
