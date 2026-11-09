// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lanz/tenable-docs-mcp/internal/build"
	"github.com/lanz/tenable-docs-mcp/internal/index"
	"github.com/lanz/tenable-docs-mcp/internal/page"
	"github.com/lanz/tenable-docs-mcp/internal/site"
)

var ctx = context.Background()

var builtAt = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

// source returns the build package's made-up sites.
func source(t *testing.T) (site.Source, []site.Site) {
	t.Helper()
	enDir, _ := filepath.Abs(filepath.Join("..", "build", "testdata", "en"))
	jpDir, _ := filepath.Abs(filepath.Join("..", "build", "testdata", "jp"))
	src, err := site.NewLocal(map[string]string{"en": enDir, "jp": jpDir})
	if err != nil {
		t.Fatal(err)
	}
	return src, src.Sites()
}

// writeSnapshot builds the made-up sites into a snapshot.
func writeSnapshot(t *testing.T) (string, Manifest) {
	t.Helper()
	src, sites := source(t)
	dir := filepath.Join(t.TempDir(), "snapshot")
	w, err := NewWriter(dir, "tenable-docs-mcp test", builtAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := build.Run(ctx, build.Options{Source: src, Sites: sites}, w); err != nil {
		t.Fatal(err)
	}
	m, err := w.Close()
	if err != nil {
		t.Fatal(err)
	}
	return dir, m
}

// importSnapshot reads every shard of a snapshot into a new index.
func importSnapshot(t *testing.T, dir string, m Manifest) *index.Index {
	t.Helper()
	ix, err := index.Open(filepath.Join(t.TempDir(), "index"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ix.Close() })
	sink := build.NewIndexSink(ix, m.BuiltAt)
	for _, s := range m.Shards {
		if err := Read(ctx, dir, s, sink); err != nil {
			t.Fatal(err)
		}
	}
	if err := sink.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestRoundTrip(t *testing.T) {
	dir, m := writeSnapshot(t)
	if m.Format != Format || m.BuiltAt != "2026-11-07T12:00:00Z" || m.Generator != "tenable-docs-mcp test" || len(m.Shards) != 3 {
		t.Fatalf("manifest = %+v", m)
	}
	var got []string
	for _, s := range m.Shards {
		got = append(got, fmt.Sprintf("%s:%d pages, %d tocs", s.Key, s.Pages, s.Tocs))
		if s.File != s.Key+"-"+s.SHA256[:12]+".jsonl.zst" {
			t.Errorf("file name %s", s.File)
		}
		if err := Verify(dir, s); err != nil {
			t.Errorf("verify %s: %v", s.File, err)
		}
		if len(s.SHA256) != 64 || s.Size == 0 {
			t.Errorf("shard = %+v", s)
		}
	}
	if want := "en-current:2 pages, 1 tocs en-older:1 pages, 0 tocs jp-current:1 pages, 0 tocs"; strings.Join(got, " ") != want {
		t.Errorf("shards = %s", strings.Join(got, " "))
	}
	// The manifest on disk is what Close returned, and no temporary files are left.
	read, err := ReadManifest(dir)
	if err != nil || fmt.Sprint(read) != fmt.Sprint(m) {
		t.Errorf("read manifest = %+v, %v", read, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 4 {
		t.Errorf("files = %v", entries)
	}

	// Importing gives the same index as building directly.
	imported := importSnapshot(t, dir, m)
	direct, err := index.Open(filepath.Join(t.TempDir(), "direct"))
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	src, sites := source(t)
	sink := build.NewIndexSink(direct, m.BuiltAt)
	if _, err := build.Run(ctx, build.Options{Source: src, Sites: sites}, sink); err != nil {
		t.Fatal(err)
	}
	if err := sink.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	for _, k := range direct.Keys() {
		a, b := describe(t, direct, k), describe(t, imported, k)
		if a != b {
			t.Errorf("%s differs:\ndirect   %s\nimported %s", k, a, b)
		}
	}
}

// describe summarizes a shard's counts and one page, for comparing indexes.
func describe(t *testing.T, ix *index.Index, k index.Key) string {
	t.Helper()
	var out string
	err := ix.With(k, func(s *index.Shard) error {
		counts, err := s.Counts(ctx)
		if err != nil {
			return err
		}
		out = fmt.Sprint(counts)
		pages := map[index.Key]string{
			{Site: "en"}:              "https://docs.tenable.com/widget/Content/Setup.htm",
			{Site: "en", Older: true}: "https://docs.tenable.com/widget/10_1/Content/Start.htm",
			{Site: "jp"}:              "https://jp.docs.tenable.com/widget/Content/Start.htm",
		}
		info, ok, err := s.PageInfo(ctx, pages[k])
		if err != nil || !ok {
			return fmt.Errorf("%s: %v %v", pages[k], ok, err)
		}
		info.IndexedAt = ""
		secs, err := s.Sections(ctx, pages[k])
		if err != nil {
			return err
		}
		j, _ := json.Marshal(struct {
			Info     *index.PageInfo
			Sections any
		}{info, secs})
		out += " " + string(j)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUnchangedPagesUnchangedFiles(t *testing.T) {
	_, first := writeSnapshot(t)
	// A fresh clone gives every file a new modification time; the snapshot doesn't change.
	later := time.Now().Add(time.Hour)
	root := filepath.Join("..", "build", "testdata")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		return os.Chtimes(p, later, later)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, second := writeSnapshot(t)
	for i := range first.Shards {
		if first.Shards[i].SHA256 != second.Shards[i].SHA256 {
			t.Errorf("%s changed", first.Shards[i].Key)
		}
	}
}

func TestTampered(t *testing.T) {
	dir, m := writeSnapshot(t)
	s := m.Shards[0]
	file := filepath.Join(dir, s.File)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Read(ctx, dir, s, discard{}); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Errorf("a changed file = %v", err)
	}
	if err := os.WriteFile(file, data[:10], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dir, s); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Errorf("a short file = %v", err)
	}
	os.Remove(file)
	if err := Verify(dir, s); err == nil {
		t.Error("a missing file should fail")
	}
}

func TestBadManifest(t *testing.T) {
	dir, m := writeSnapshot(t)
	write := func(m Manifest) {
		data, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir, ManifestFile), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	newer := m
	newer.Format = Format + 1
	write(newer)
	if _, err := ReadManifest(dir); err == nil || !strings.Contains(err.Error(), "update it") {
		t.Errorf("a newer format = %v", err)
	}
	for _, change := range []func(*Shard){
		func(s *Shard) { s.File = "../outside.jsonl.zst" },
		func(s *Shard) { s.Site = "pt" },
		func(s *Shard) { s.Older = !s.Older },
	} {
		bad := m
		bad.Shards = append([]Shard(nil), m.Shards...)
		change(&bad.Shards[0])
		write(bad)
		if _, err := ReadManifest(dir); err == nil {
			t.Errorf("%+v should fail", bad.Shards[0])
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(dir); err == nil {
		t.Error("a broken manifest should fail")
	}
}

func TestPageInTheWrongShard(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(dir, "test", builtAt)
	if err != nil {
		t.Fatal(err)
	}
	p := build.Page{Page: &page.Page{URL: "https://jp.docs.tenable.com/widget/Content/Start.htm", Title: "x"}}
	if err := w.Page(ctx, index.Key{Site: "en"}, p); err != nil {
		t.Fatal(err)
	}
	m, err := w.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := Read(ctx, dir, m.Shards[0], discard{}); err == nil || !strings.Contains(err.Error(), "doesn't belong") {
		t.Errorf("a page in the wrong shard = %v", err)
	}
}

type discard struct{}

func (discard) Page(context.Context, index.Key, build.Page) error { return nil }
func (discard) Toc(context.Context, index.Key, site.Guide, []*site.TocEntry) error {
	return nil
}

func TestGuideNamesRoundTrip(t *testing.T) {
	src, sites := source(t)
	dir := filepath.Join(t.TempDir(), "snapshot")
	w, err := NewWriter(dir, "tenable-docs-mcp test", builtAt)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]map[string]string{"en": {"widget": "Widget Manager User Guide"}}
	if _, err := build.Run(ctx, build.Options{Source: src, Sites: sites, GuideNames: names}, w); err != nil {
		t.Fatal(err)
	}
	m, err := w.Close()
	if err != nil {
		t.Fatal(err)
	}
	ix := importSnapshot(t, dir, m)
	err = ix.With(index.Key{Site: "en"}, func(s *index.Shard) error {
		g, ok, err := s.Guide(ctx, "widget")
		if err != nil || !ok || g.Name != "Widget Manager User Guide" {
			t.Errorf("guide = %+v %v %v", g, ok, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
