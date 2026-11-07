// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package refresh

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

var ctx = context.Background()

const en = "https://docs.tenable.com/"

// fakeSite answers conditional requests: 304 when the ETag matches.
type fakeSite struct {
	pages    map[string]string // URL → body
	etags    map[string]string
	fail     map[string]bool
	requests int
}

func (f *fakeSite) Fetch(_ context.Context, req site.Request) (*site.Response, error) {
	f.requests++
	if f.fail[req.URL] {
		return nil, fmt.Errorf("fetch %s: server error", req.URL)
	}
	body, ok := f.pages[req.URL]
	if !ok {
		return &site.Response{URL: req.URL, Status: 404}, fmt.Errorf("fetch %s: %w", req.URL, site.ErrNotFound)
	}
	etag := f.etags[req.URL]
	if etag != "" && req.ETag == etag {
		return &site.Response{URL: req.URL, Status: 304, NotModified: true, ETag: etag}, nil
	}
	return &site.Response{URL: req.URL, Status: 200, Body: []byte(body), ETag: etag}, nil
}

func page1(title, text string) string {
	return `<div id="mc-main-content"><h1>` + title + `</h1><p>` + text + `</p></div>`
}

func setup(t *testing.T) (*index.Index, *fakeSite) {
	t.Helper()
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	en0, _ := site.ByCode("en")
	b, err := ix.Build(index.Key{Site: "en"})
	if err != nil {
		t.Fatal(err)
	}
	err = b.Update(ctx, func(tx *index.Tx) error {
		for _, name := range []string{"Same", "Changed", "Gone", "Secret", "Broken"} {
			u := en + "nessus/Content/" + name + ".htm"
			p, err := page.Parse([]byte(page1(name, "Original text.")), u, nil)
			if err != nil {
				return err
			}
			d := index.NewDoc(p, en0, site.Entry{URL: u, Products: []string{"Nessus", "Tenable Agent"}, GuideType: "User Guide", LastMod: "2026-10-01"}, []string{"Welcome", name})
			d.ETag = `"v1"`
			if _, err := tx.Put(d); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Install(b); err != nil {
		t.Fatal(err)
	}
	live := &fakeSite{
		pages: map[string]string{
			en + "robots.txt":                 "User-agent: *\nDisallow: /nessus/Content/Secret.htm\n",
			en + "nessus/Content/Same.htm":    page1("Same", "Original text."),
			en + "nessus/Content/Changed.htm": page1("Changed", "New text about widgets."),
			en + "nessus/Content/Secret.htm":  page1("Secret", "Hidden."),
			en + "nessus/Data/FullToc.xml":    `<CatapultToc><TocEntry Title="Welcome" Link="/Content/Same.htm"/></CatapultToc>`,
		},
		etags: map[string]string{en + "nessus/Content/Same.htm": `"v1"`, en + "nessus/Content/Changed.htm": `"v2"`},
		fail:  map[string]bool{en + "nessus/Content/Broken.htm": true},
	}
	return ix, live
}

func TestPages(t *testing.T) {
	ix, live := setup(t)
	r := &Refresher{Index: ix, Source: live}
	rep, err := r.Pages(ctx, []string{
		en + "nessus/Content/Same.htm#x", en + "nessus/Content/Same.htm",
		en + "nessus/Content/Changed.htm", en + "nessus/Content/Gone.htm",
		en + "nessus/Content/Secret.htm", en + "nessus/Content/Broken.htm",
		en + "nessus/Content/NotIndexed.htm", "https://jp.docs.tenable.com/nessus/Content/Same.htm",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 4 || rep.Unchanged != 1 || rep.Updated != 1 || rep.Removed != 1 || rep.Skipped != 3 || len(rep.Failed) != 1 {
		t.Errorf("report = %+v", rep)
	}
	err = ix.With(index.Key{Site: "en"}, func(s *index.Shard) error {
		info, ok, err := s.PageInfo(ctx, en+"nessus/Content/Changed.htm")
		if err != nil || !ok || info.ETag != `"v2"` || info.Product != "Nessus" || strings.Join(info.Related, ",") != "Tenable Agent" ||
			strings.Join(info.TocTrail, " › ") != "Welcome › Changed" || info.LastMod != "2026-10-01" {
			t.Errorf("updated page = %+v, %v %v", info, ok, err)
		}
		secs, _ := s.Sections(ctx, en+"nessus/Content/Changed.htm")
		if len(secs) == 0 || !strings.Contains(secs[0].Body, "widgets") {
			t.Errorf("sections = %+v", secs)
		}
		if _, ok, _ := s.PageInfo(ctx, en+"nessus/Content/Gone.htm"); ok {
			t.Error("removed page still indexed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Again: everything is unchanged now, and robots.txt isn't fetched again.
	before := live.requests
	rep, _ = r.Pages(ctx, []string{en + "nessus/Content/Same.htm", en + "nessus/Content/Changed.htm"})
	if rep.Unchanged != 2 || live.requests-before != 2 {
		t.Errorf("second run = %+v, %d requests", rep, live.requests-before)
	}
	if _, err := r.Pages(ctx, []string{"https://example.com/x"}); err == nil {
		t.Error("an off-site URL should be an error")
	}
	many := make([]string, MaxPages+1)
	for i := range many {
		many[i] = fmt.Sprintf("%snessus/Content/P%d.htm", en, i)
	}
	if _, err := r.Pages(ctx, many); err == nil {
		t.Error("too many pages should be an error")
	}
}

func TestGuide(t *testing.T) {
	ix, live := setup(t)
	r := &Refresher{Index: ix, Source: live, Agent: "tenable-docs-mcp"}
	rep, err := r.Guide(ctx, index.Key{Site: "en"}, "nessus")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 4 || !rep.TocUpdated {
		t.Errorf("report = %+v", rep)
	}
	err = ix.With(index.Key{Site: "en"}, func(s *index.Shard) error {
		toc, _, ok, err := s.Toc(ctx, "nessus")
		if err != nil || !ok || len(toc) != 1 || toc[0].URL != en+"nessus/Content/Same.htm" {
			t.Errorf("toc = %+v %v %v", toc, ok, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		k     index.Key
		guide string
	}{{index.Key{Site: "en"}, "missing"}, {index.Key{Site: "fr"}, "nessus"}} {
		if _, err := r.Guide(ctx, tc.k, tc.guide); err == nil {
			t.Errorf("%v %s should fail", tc.k, tc.guide)
		}
	}
	// A guide without FullToc.xml (nor Flare's JavaScript files) still refreshes its pages.
	delete(live.pages, en+"nessus/Data/FullToc.xml")
	rep, err = r.Guide(ctx, index.Key{Site: "en"}, "nessus")
	if err != nil || rep.TocUpdated {
		t.Errorf("no toc = %+v, %v", rep, err)
	}
}
