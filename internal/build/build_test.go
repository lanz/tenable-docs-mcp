// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package build

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

var ctx = context.Background()

const en = "https://docs.tenable.com/"

// Source returns the made-up sites in testdata: English and Japanese.
func Source(t *testing.T) (site.Source, []site.Site) {
	t.Helper()
	enDir, _ := filepath.Abs(filepath.Join("testdata", "en"))
	jpDir, _ := filepath.Abs(filepath.Join("testdata", "jp"))
	src, err := site.NewLocal(map[string]string{"en": enDir, "jp": jpDir})
	if err != nil {
		t.Fatal(err)
	}
	return src, src.Sites()
}

func TestRunIntoIndex(t *testing.T) {
	src, sites := Source(t)
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	var progress []string
	sink := NewIndexSink(ix, "2026-11-07T00:00:00Z")
	fetched := &countingSource{Source: src}
	rep, err := Run(ctx, Options{Source: fetched, Sites: sites, Progress: func(code string, done, total int) {
		progress = append(progress, fmt.Sprintf("%s %d/%d", code, done, total))
	}}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(rep.Pages) != "map[en-current:2 en-older:1 jp-current:1]" || rep.Total() != 4 || rep.Blocked != 1 || rep.Skipped != 2 ||
		rep.Tocs != 1 || len(rep.Failed) != 0 {
		t.Errorf("report = %+v", rep)
	}
	if got := strings.Join(progress, ", "); got != "en 1/6, en 2/6, en 3/6, en 4/6, en 5/6, en 6/6, jp 1/1" {
		t.Errorf("progress = %s", got)
	}
	// Documents that aren't pages, such as PDFs, aren't even read.
	for _, u := range fetched.urls {
		if strings.HasSuffix(u, ".pdf") {
			t.Errorf("read %s", u)
		}
	}
	if got := fmt.Sprint(ix.Keys()); got != "[en-current en-older jp-current]" {
		t.Errorf("shards = %s", got)
	}
	err = ix.With(index.Key{Site: "en"}, func(s *index.Shard) error {
		info, ok, err := s.PageInfo(ctx, en+"widget/Content/Setup.htm")
		if err != nil || !ok {
			return fmt.Errorf("setup page: %v %v", ok, err)
		}
		if strings.Join(info.TocTrail, " › ") != "Welcome to Widget Manager › Set Up Widgets" || info.Product != "Widget Manager" ||
			info.GuideType != "User Guide" || info.LastMod != "2026-10-02" || info.LastModified != "" || len(info.Roles) != 1 {
			t.Errorf("setup page = %+v", info)
		}
		if _, ok, _ := s.PageInfo(ctx, en+"secret/Content/Hidden.htm"); ok {
			t.Error("robots.txt disallows the hidden page")
		}
		built, _ := s.Meta(ctx, "built_at")
		if built != "2026-11-07T00:00:00Z" {
			t.Errorf("built_at = %q", built)
		}
		toc, _, ok, err := s.Toc(ctx, "widget")
		if err != nil || !ok || len(toc) != 1 || len(toc[0].Children) != 1 {
			t.Errorf("toc = %+v %v %v", toc, ok, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDiscard(t *testing.T) {
	src, sites := Source(t)
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	sink := NewIndexSink(ix, "now")
	if _, err := Run(ctx, Options{Source: src, Sites: sites}, sink); err != nil {
		t.Fatal(err)
	}
	sink.Discard()
	if len(ix.Keys()) != 0 {
		t.Errorf("a discarded build installed %v", ix.Keys())
	}
}

func TestRunErrors(t *testing.T) {
	src, sites := Source(t)
	// A site the source has no folder for has no sitemap.
	fr, _ := site.ByCode("fr")
	if _, err := Run(ctx, Options{Source: src, Sites: append(sites, fr)}, discardSink{}); err == nil {
		t.Error("a site without a sitemap should fail")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Run(cancelled, Options{Source: src, Sites: sites}, discardSink{}); err == nil {
		t.Error("a cancelled build should fail")
	}
}

// countingSource records what it's asked for.
type countingSource struct {
	site.Source
	urls []string
}

func (c *countingSource) Fetch(ctx context.Context, req site.Request) (*site.Response, error) {
	c.urls = append(c.urls, req.URL)
	return c.Source.Fetch(ctx, req)
}

// pageSink collects pages.
type pageSink struct {
	discardSink
	pages []Page
}

func (p *pageSink) Page(_ context.Context, _ index.Key, pg Page) error {
	p.pages = append(p.pages, pg)
	return nil
}

func TestLiveKeepsLastModified(t *testing.T) {
	src, sites := Source(t)
	for _, live := range []bool{false, true} {
		var sink pageSink
		if _, err := Run(ctx, Options{Source: src, Sites: sites[:1], Live: live}, &sink); err != nil {
			t.Fatal(err)
		}
		for _, p := range sink.pages {
			if (p.LastModified != "") != live {
				t.Errorf("live %v: %s last modified %q", live, p.Page.URL, p.LastModified)
			}
		}
	}
}

type discardSink struct{}

func (discardSink) Page(context.Context, index.Key, Page) error { return nil }
func (discardSink) Toc(context.Context, index.Key, site.Guide, []*site.TocEntry) error {
	return nil
}
