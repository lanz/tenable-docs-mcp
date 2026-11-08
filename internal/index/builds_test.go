// Copyright (C) 2026 Lance Duvall
// Project: <https://github.com/lanz/tenable-docs-mcp>
// SPDX-License-Identifier: GPL-3.0-or-later

package index

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// installText builds a shard holding one page with text, and installs it.
func installText(t *testing.T, ix *Index, k Key, text string) {
	t.Helper()
	b, err := ix.Build(k)
	if err != nil {
		t.Fatal(err)
	}
	put(t, b, makeDoc(t, k.Site, "https://"+mustSite(t, k.Site).Host+"/widget/Content/A.htm", `<h1>A</h1><p>`+text+`</p>`))
	if err := ix.Install(b); err != nil {
		t.Fatal(err)
	}
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestParseGen(t *testing.T) {
	en := Key{Site: "en"}
	for name, want := range map[string]int{"en-current.db": 0, "en-current.1.db": 1, "en-current.42.db": 42} {
		if got, ok := parseGen(name, en); !ok || got != want {
			t.Errorf("parseGen(%q) = %d, %v", name, got, ok)
		}
	}
	for _, name := range []string{"en-current.building.abc.db", "en-current.0.db", "en-current.01.db", "en-current.-1.db",
		"en-current.x.db", "en-current..db", "en-older.1.db", "en-current.1.db-wal", "zh-cn-current.1.db"} {
		if got, ok := parseGen(name, en); ok {
			t.Errorf("parseGen(%q) = %d, want no match", name, got)
		}
	}
}

func TestInstallNumbersBuilds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	en := Key{Site: "en"}
	installText(t, ix, en, "first")
	installText(t, ix, en, "second")
	installText(t, ix, en, "third")
	got := files(t, dir)
	if !slices.Contains(got, "en-current.3.db") || slices.ContainsFunc(got, func(n string) bool {
		return n == "en-current.1.db" || n == "en-current.2.db" || strings.Contains(n, "building")
	}) {
		t.Errorf("files = %v, want only the third build", got)
	}
	if hits := search(t, ix.Shard(en), "third"); len(hits) != 1 {
		t.Errorf("third build not searched: %v", hits)
	}
}

func TestOpenReadsUnnumberedFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	en := Key{Site: "en"}
	// An index written by an earlier version: one en-current.db.
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	old, err := OpenShard(filepath.Join(dir, "en-current.db"), en)
	if err != nil {
		t.Fatal(err)
	}
	put(t, old, makeDoc(t, "en", "https://docs.tenable.com/widget/Content/A.htm", `<h1>A</h1><p>legacy</p>`))
	old.Close()

	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	if hits := search(t, ix.Shard(en), "legacy"); len(hits) != 1 {
		t.Fatalf("legacy file not opened: %v", hits)
	}
	// The next build is numbered and replaces it.
	installText(t, ix, en, "numbered")
	if got := files(t, dir); slices.Contains(got, "en-current.db") || !slices.Contains(got, "en-current.1.db") {
		t.Errorf("files = %v", got)
	}
}

// Two processes using one folder: what one installs, the other picks up with Reload,
// and the old file goes once neither has it open.
func TestReloadAcrossIndexes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	a, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	en := Key{Site: "en"}
	installText(t, a, en, "original")

	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if changed, err := b.Reload(); err != nil || changed {
		t.Errorf("Reload with nothing new = %v, %v", changed, err)
	}

	// a installs a new build while b has the old one open.
	installText(t, a, en, "updated")
	if hits := search(t, b.Shard(en), "original"); len(hits) != 1 {
		t.Errorf("b lost its open build: %v", hits)
	}
	changed, err := b.Reload()
	if err != nil || !changed {
		t.Fatalf("Reload = %v, %v", changed, err)
	}
	if hits := search(t, b.Shard(en), "updated"); len(hits) != 1 {
		t.Errorf("b doesn't search the new build: %v", hits)
	}
	if got := files(t, dir); slices.Contains(got, "en-current.1.db") {
		t.Errorf("the old build is still there after both moved on: %v", got)
	}

	// A shard that only the other process built appears too.
	installText(t, a, Key{Site: "fr"}, "nouveau")
	if changed, err := b.Reload(); err != nil || !changed || b.Shard(Key{Site: "fr"}) == nil {
		t.Errorf("new shard: changed %v, err %v", changed, err)
	}
}

func TestBuildFilesAreUnique(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index")
	ix, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	en := Key{Site: "en"}
	// An unfinished build left by a process that stopped.
	stale := filepath.Join(dir, buildPrefix(en)+"stale.db")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	b1, err := ix.Build(en)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Discard(b1)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale build not removed: %v", err)
	}
	if b1.Path == stale || !strings.HasPrefix(filepath.Base(b1.Path), buildPrefix(en)) {
		t.Errorf("build path = %s", b1.Path)
	}
}
