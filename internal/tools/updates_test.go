// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lanz/tenable-docs-mcp/internal/crawl"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/published"
)

func emptyIndex(t *testing.T) *index.Index {
	t.Helper()
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	return ix
}

func TestStatusAndErrorsWhileDownloading(t *testing.T) {
	var st published.Status
	env := NewEnv(Info{Name: "tenable-docs-mcp", Version: "test"}, emptyIndex(t))
	env.Updates = func() published.Status { return st }
	cs := connectEnv(t, env)
	src := published.DefaultURL

	st = published.Status{State: published.State{URL: src}, Updating: true, Step: "Downloading en-current-abc.jsonl.zst (5.0 MB)"}
	var out statusOut
	text := call(t, cs, "index_status", nil, &out)
	if out.Index.State != "downloading" || !out.Updates.On || !out.Updates.Updating || out.Updates.Source != src {
		t.Errorf("status = %+v", out)
	}
	if !strings.Contains(text, "Updating from "+src+": Downloading en-current") {
		t.Errorf("summary = %q", text)
	}
	msg, isErr := callRaw(t, cs, "search_docs", map[string]any{"query": "install nessus"}, nil)
	if !isErr || !strings.Contains(msg, "being built for the first time (downloading en-current") {
		t.Errorf("search while downloading: %v %q", isErr, msg)
	}

	st = published.Status{State: published.State{URL: src, LastError: "manifest: HTTP 503", ErrorAt: "2026-11-08T12:00:00Z"}}
	out = statusOut{}
	text = call(t, cs, "index_status", nil, &out)
	if out.Index.State != "not built" || out.Updates.LastError != "manifest: HTTP 503" || !strings.Contains(out.Note, "tried again") {
		t.Errorf("status after a failure = %+v", out)
	}
	if !strings.Contains(text, "The last check failed at 2026-11-08T12:00:00Z: manifest: HTTP 503") {
		t.Errorf("summary = %q", text)
	}
	msg, isErr = callRaw(t, cs, "search_docs", map[string]any{"query": "install nessus"}, nil)
	if !isErr || !strings.Contains(msg, "downloading it from docs.tenable.com failed (manifest: HTTP 503)") {
		t.Errorf("search after a failure: %v %q", isErr, msg)
	}
}

func TestStatusWithUpdatesOff(t *testing.T) {
	cs := connectEnv(t, NewEnv(Info{Name: "tenable-docs-mcp", Version: "test"}, emptyIndex(t)))
	var out statusOut
	text := call(t, cs, "index_status", nil, &out)
	if out.Updates.On || !strings.Contains(out.Note, "index pull") || !strings.Contains(text, "Automatic updates are off.") {
		t.Errorf("status = %+v; %q", out, text)
	}
}

func TestStatusWhenReady(t *testing.T) {
	env := NewEnv(Info{Name: "tenable-docs-mcp", Version: "test"}, buildIndex(t))
	env.Updates = func() published.Status {
		return published.Status{State: published.State{URL: published.DefaultURL, CheckedAt: "2026-11-08T12:00:00Z", BuiltAt: "2026-11-08T05:47:43Z"}}
	}
	var out statusOut
	text := call(t, connectEnv(t, env), "index_status", nil, &out)
	if out.Index.State != "ready" || out.Updates.CheckedAt != "2026-11-08T12:00:00Z" || out.Updates.SnapshotBuiltAt != "2026-11-08T05:47:43Z" {
		t.Errorf("status = %+v", out)
	}
	if !strings.Contains(text, "last checked 2026-11-08T12:00:00Z; snapshot built 2026-11-08T05:47:43Z") {
		t.Errorf("summary = %q", text)
	}
}

// After the first download, the guides' tables of contents appear as resources, and
// a guide that's gone from a later update is removed.
func TestGuidesRegisteredAfterUpdate(t *testing.T) {
	ix := emptyIndex(t)
	env := NewEnv(Info{Name: "tenable-docs-mcp", Version: "test"}, ix)
	s := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0"}, nil)
	Register(s, env)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tocs := func() []string {
		t.Helper()
		list, err := cs.ListResources(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, r := range list.Resources {
			if strings.HasPrefix(r.Name, "toc/") {
				names = append(names, r.Name)
			}
		}
		return names
	}
	if got := tocs(); len(got) != 0 {
		t.Fatalf("before the download: %v", got)
	}

	fillIndex(t, ix, fixture)
	RegisterGuides(s, env)
	if got := tocs(); !slices.Contains(got, "toc/nessus") {
		t.Fatalf("after the download: %v", got)
	}

	// A later build without the guide.
	b, err := ix.Build(index.Key{Site: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Install(b); err != nil {
		t.Fatal(err)
	}
	RegisterGuides(s, env)
	if got := tocs(); len(got) != 0 {
		t.Errorf("after the guide went: %v", got)
	}
}

// read_doc reads a page that's new since the index was updated by fetching it from
// the live site, and follows a redirect a crawl recorded.
func TestReadDocOnDemandAndRedirects(t *testing.T) {
	ix := buildIndex(t)
	fresh := en + "nessus/Content/Fresh.htm"
	live := liveSite{fresh: `<html lang="en-us"><head><title>Fresh page</title></head><body><div id="mc-main-content"><h1>Fresh page</h1><p>Published this morning.</p></div></body></html>`}
	env := NewEnv(Info{Name: "tenable-docs-mcp", Version: "test"}, ix)
	env.FetchPage = (&crawl.OnDemand{Crawler: &crawl.Crawler{Index: ix, Source: live}}).Page
	cs := connectEnv(t, env)

	var out readOut
	call(t, cs, "read_doc", map[string]any{"url": fresh}, &out)
	if out.Title != "Fresh page" || !strings.Contains(out.Markdown, "Published this morning.") {
		t.Errorf("on-demand page = %+v", out)
	}
	// It's in the index now, for searches too.
	if _, ok, _ := ix.Shard(index.Key{Site: "en"}).PageInfo(ctx, fresh); !ok {
		t.Error("the fetched page wasn't stored")
	}

	// A page the live site doesn't have either.
	msg, isErr := callRaw(t, cs, "read_doc", map[string]any{"url": en + "nessus/Content/Nowhere.htm"}, nil)
	if !isErr || !strings.Contains(msg, "isn't in the index") {
		t.Errorf("missing page: %v %q", isErr, msg)
	}

	// A recorded redirect, with fetching off.
	old := en + "nessus/Content/InstallOld.htm"
	err := ix.With(index.Key{Site: "en"}, func(sh *index.Shard) error {
		return sh.Update(ctx, func(tx *index.Tx) error { return tx.PutRedirect(old, en+"nessus/Content/Install.htm") })
	})
	if err != nil {
		t.Fatal(err)
	}
	env.FetchPage = nil
	out = readOut{}
	call(t, cs, "read_doc", map[string]any{"url": old}, &out)
	if !strings.HasPrefix(out.URL, en+"nessus/Content/Install.htm") {
		t.Errorf("redirected read = %+v", out)
	}
}

func TestStatusWhenCrawling(t *testing.T) {
	env := NewEnv(Info{Name: "tenable-docs-mcp", Version: "test"}, buildIndex(t))
	env.Updates = func() published.Status {
		return published.Status{State: published.State{URL: published.DefaultURL, LastError: "manifest: HTTP 403", ErrorAt: "2026-11-08T12:00:00Z",
			Source: published.SourceLive, CrawledAt: "2026-11-08T12:05:00Z"}}
	}
	var out statusOut
	text := call(t, connectEnv(t, env), "index_status", nil, &out)
	if out.Updates.From != "live" || out.Updates.CrawledAt != "2026-11-08T12:05:00Z" {
		t.Errorf("status = %+v", out.Updates)
	}
	if !strings.Contains(text, "kept up to date from the live sites; last read 2026-11-08T12:05:00Z") {
		t.Errorf("summary = %q", text)
	}
}
